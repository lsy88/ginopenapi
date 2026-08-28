// Command ginopenapi-gen statically analyzes Gin route registrations and their
// handlers, then generates a Go file that fills in OpenAPI metadata (request
// bodies, response schemas, summaries) for every resolvable route.
//
// Usage:
//
//	//go:generate ginopenapi-gen -patterns ./internal/... -out ./internal/router/openapi_gen.go
//
// It uses go/packages + go/types so handlers written as struct-method
// selectors (e.g. app.AuthHandler.Login) can be resolved across packages. Each
// handler body is inspected for c.ShouldBindJSON / c.BindJSON / c.JSON calls.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/constant"
	"go/format"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

const ginImport = "github.com/gin-gonic/gin"

var debugMode bool

var routeMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true,
	"DELETE": true, "HEAD": true, "OPTIONS": true,
}

// fdKey couples a handler's FuncDecl with the TypesInfo of its package so its
// body can be analyzed later.
type fdKey struct {
	decl *ast.FuncDecl
	info *types.Info
}

type route struct {
	method      string // GET / POST / ...
	path        string // full gin path, e.g. /api/v1/auth/login
	handler     fdKey
	summaryName string // handler method name humanized (ListNodes -> List nodes)
}

type schemaResults struct {
	req       types.Type
	responses map[int]types.Type
}

// rendered is a route ready to emit.
type rendered struct {
	r    route
	req  string
	resp map[int]string
}

func main() {
	patterns := flag.String("patterns", "./internal/...", "package patterns to load and analyze")
	out := flag.String("out", "./internal/router/openapi_gen.go", "output .go file path")
	debug := flag.Bool("debug", false, "print handler resolution diagnostics")
	flag.Parse()
	debugMode = *debug

	// Resolve relative paths against the module root (the dir containing go.mod)
	// so the tool behaves identically whether invoked from the root or via
	// `go generate` from a sub-package.
	root := moduleRoot(".")
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedImports |
			packages.NeedDeps | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo,
		Tests: false,
		Dir:   root,
	}

	outPath := *out
	if !filepath.IsAbs(outPath) {
		outPath = filepath.Join(root, outPath)
	}

	pkgs, err := packages.Load(cfg, strings.Fields(*patterns)...)
	if err != nil {
		fatalf("load packages: %v", err)
	}
	// Type errors are expected when the generated file has been deleted/rotated
	// (e.g. router.go references RegisterOpenAPIMetadata that we are about to
	// recreate). go/types recovers, so route analysis still works; regenerate and
	// the errors disappear.
	if n := countLoadErrors(pkgs); n > 0 {
		fmt.Fprintf(os.Stderr, "ginopenapi-gen: warning: %d package load error(s) (stale/missing generated file?)\n", n)
	}

	routes, outName, err := collectRoutes(pkgs)
	if err != nil {
		fatalf("collect routes: %v", err)
	}
	if len(routes) == 0 {
		fatalf("no routes discovered; check -patterns")
	}

	em := &emitter{used: map[string]string{}, aliasUsed: map[string]bool{}}
	em.register("net/http", "http")
	em.register("ginopenapi", "ginopenapi")

	var finals []rendered
	for _, r := range routes {
		f := rendered{r: r, resp: map[int]string{}}
		var si schemaResults
		if r.handler.decl != nil && r.handler.decl.Body != nil {
			si = analyzeFunc(r.handler)
		}
		if si.req != nil {
			f.req = em.zeroValue(si.req)
		}
		for st, t := range si.responses {
			if expr := em.zeroValue(t); expr != "" {
				f.resp[st] = expr
			}
		}
		finals = append(finals, f)
	}

	body := render(em, outName, finals)

	src, err := format.Source([]byte(body))
	if err != nil {
		fatalf("format generated output: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(outPath, src, 0o644); err != nil {
		fatalf("write %s: %v", outPath, err)
	}
	fmt.Fprintf(os.Stderr, "ginopenapi-gen: wrote %d route(s) to %s\n", len(routes), outPath)
}

// moduleRoot walks up from dir until it finds go.mod, returning that directory.
func moduleRoot(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	for {
		if info, err := os.Stat(filepath.Join(abs, "go.mod")); err == nil && !info.IsDir() {
			return abs
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return dir
		}
		abs = parent
	}
}

func countLoadErrors(pkgs []*packages.Package) int {
	n := 0
	for _, p := range loadAll(pkgs) {
		n += len(p.Errors)
	}
	return n
}

func loadAll(pkgs []*packages.Package) []*packages.Package {
	var all []*packages.Package
	seen := map[string]bool{}
	var walk func(*packages.Package)
	walk = func(p *packages.Package) {
		if seen[p.ID] {
			return
		}
		seen[p.ID] = true
		all = append(all, p)
		for _, imp := range p.Imports {
			walk(imp)
		}
	}
	for _, p := range pkgs {
		walk(p)
	}
	return all
}

// collectRoutes finds every function taking a *gin.Engine, reconstructs full
// paths through .Group() chains, and resolves each handler to its FuncDecl.
// Returns the routes plus the name of the package that owns them.
func collectRoutes(pkgs []*packages.Package) ([]route, string, error) {
	// Resolve each FuncDecl to its *types.Func via ObjectOf (object identity),
	// which is robust against position off-by-ones.
	funcByObj := map[*types.Func]fdKey{}

	for _, p := range loadAll(pkgs) {
		if p.Syntax == nil || p.TypesInfo == nil {
			continue
		}
		for _, f := range p.Syntax {
			ast.Inspect(f, func(n ast.Node) bool {
				fd, ok := n.(*ast.FuncDecl)
				if ok && fd.Body != nil {
					if fn, ok := p.TypesInfo.ObjectOf(fd.Name).(*types.Func); ok {
						funcByObj[fn] = fdKey{fd, p.TypesInfo}
					}
				}
				return true
			})
		}
	}

	var routes []route
	outName := ""

	for _, p := range loadAll(pkgs) {
		if p.Syntax == nil || p.TypesInfo == nil {
			continue
		}
		var pkgRoutes []route
		for _, f := range p.Syntax {
			ast.Inspect(f, func(n ast.Node) bool {
				fd, ok := n.(*ast.FuncDecl)
				if !ok || fd.Body == nil || !hasGinEngineParam(fd, p.TypesInfo) {
					return true
				}
				collectFromFunc(fd, p.TypesInfo, funcByObj, &pkgRoutes)
				return false
			})
		}
		if len(pkgRoutes) > 0 && outName == "" {
			outName = p.Name
		}
		routes = append(routes, pkgRoutes...)
	}

	if outName == "" {
		outName = "router"
	}

	sort.Slice(routes, func(i, j int) bool {
		if routes[i].path != routes[j].path {
			return routes[i].path < routes[j].path
		}
		return routes[i].method < routes[j].method
	})

	return routes, outName, nil
}

func collectFromFunc(decl *ast.FuncDecl, info *types.Info, funcByObj map[*types.Func]fdKey, out *[]route) {
	syms := map[string]string{}
	for _, p := range decl.Type.Params.List {
		for _, name := range p.Names {
			if isGinEngine(info.TypeOf(name)) {
				syms[name.Name] = ""
			}
		}
	}
	walkStmts(decl.Body.List, syms, info, funcByObj, out)
}

// walkStmts tracks .Group() prefixes and extracts route registrations.
func walkStmts(list []ast.Stmt, syms map[string]string, info *types.Info, funcByObj map[*types.Func]fdKey, out *[]route) {
	for _, st := range list {
		switch s := st.(type) {
		case *ast.BlockStmt:
			walkStmts(s.List, syms, info, funcByObj, out)

		case *ast.AssignStmt:
			if len(s.Lhs) != 1 || len(s.Rhs) != 1 || s.Tok != token.DEFINE {
				continue
			}
			lhs, ok := s.Lhs[0].(*ast.Ident)
			if !ok {
				continue
			}
			if prefix, ok := groupPrefix(s.Rhs[0], syms, info); ok {
				syms[lhs.Name] = prefix
			}

		case *ast.DeclStmt:
			gd, ok := s.Decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
					continue
				}
				if prefix, ok := groupPrefix(vs.Values[0], syms, info); ok {
					syms[vs.Names[0].Name] = prefix
				}
			}

		case *ast.ExprStmt:
			call, ok := s.X.(*ast.CallExpr)
			if !ok {
				continue
			}
			if r, ok := routeFromCall(call, syms, info, funcByObj); ok {
				*out = append(*out, r)
			}
		}
	}
}

// groupPrefix returns the accumulated path for an expr of the form
// recv.Group("/prefix") given the receiver symbol's existing prefix.
func groupPrefix(expr ast.Expr, syms map[string]string, info *types.Info) (string, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Group" || !isGinMethod(sel, info) {
		return "", false
	}
	recv, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	prefix, known := syms[recv.Name]
	if !known {
		return "", false
	}
	if len(call.Args) == 1 {
		prefix += stringLiteral(call.Args[0])
	}
	return prefix, true
}

func routeFromCall(call *ast.CallExpr, syms map[string]string, info *types.Info, funcByObj map[*types.Func]fdKey) (route, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return route{}, false
	}
	verb := sel.Sel.Name
	if !routeMethods[verb] || !isGinMethod(sel, info) || len(call.Args) < 2 {
		return route{}, false
	}
	recv, ok := sel.X.(*ast.Ident)
	if !ok {
		return route{}, false
	}
	prefix, known := syms[recv.Name]
	if !known {
		return route{}, false
	}

	path := prefix + stringLiteral(call.Args[0])

	// Skip handlers we cannot resolve as named funcs (closures, factories).
	var hk fdKey
	if expr := call.Args[1]; isNamedHandler(expr) {
		hk = resolveHandler(expr, info, funcByObj)
		if debugMode && hk.decl == nil {
			fmt.Fprintf(os.Stderr, "ginopenapi-gen: [debug] unresolved handler %v for %s %s\n", expr, verb, path)
		}
	}

	sum := ""
	if hk.decl != nil {
		sum = humanize(hk.decl.Name.Name)
	}

	return route{method: verb, path: path, handler: hk, summaryName: sum}, true
}

func isNamedHandler(expr ast.Expr) bool {
	switch expr.(type) {
	case *ast.Ident, *ast.SelectorExpr:
		return true
	}
	return false
}

func resolveHandler(expr ast.Expr, info *types.Info, funcByObj map[*types.Func]fdKey) fdKey {
	var obj types.Object
	switch e := expr.(type) {
	case *ast.Ident:
		obj = info.ObjectOf(e)
	case *ast.SelectorExpr:
		obj = info.ObjectOf(e.Sel)
		if sel, ok := info.Selections[e]; ok {
			obj = sel.Obj()
		}
	}
	if fn, ok := obj.(*types.Func); ok {
		return funcByObj[fn]
	}
	return fdKey{}
}

func isGinMethod(sel *ast.SelectorExpr, info *types.Info) bool {
	if info == nil {
		return false
	}
	obj := info.ObjectOf(sel.Sel)
	if s, ok := info.Selections[sel]; ok {
		obj = s.Obj()
	}
	fn, ok := obj.(*types.Func)
	if !ok {
		return false
	}
	return fn.Pkg() != nil && fn.Pkg().Path() == ginImport
}

func hasGinEngineParam(fd *ast.FuncDecl, info *types.Info) bool {
	for _, p := range fd.Type.Params.List {
		for _, name := range p.Names {
			if isGinEngine(info.TypeOf(name)) {
				return true
			}
		}
	}
	return false
}

func isGinEngine(t types.Type) bool {
	pt, ok := t.(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := pt.Elem().(*types.Named)
	if !ok {
		return false
	}
	return named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == ginImport && named.Obj().Name() == "Engine"
}

func stringLiteral(e ast.Expr) string {
	bl, ok := e.(*ast.BasicLit)
	if !ok {
		return ""
	}
	s, err := strconv.Unquote(bl.Value)
	if err != nil {
		return ""
	}
	return s
}

// analyzeFunc extracts request body + response schemas from a handler body via
// c.ShouldBindJSON / c.BindJSON / c.JSON.
func analyzeFunc(hk fdKey) schemaResults {
	var si schemaResults
	si.responses = map[int]types.Type{}
	info := hk.info

	ast.Inspect(hk.decl, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !isGinContext(sel.X, info) {
			return true
		}
		switch sel.Sel.Name {
		case "ShouldBindJSON", "BindJSON":
			if len(call.Args) != 1 {
				return true
			}
			if t := deref(info.TypeOf(call.Args[0])); t != nil {
				si.req = t
			}
		case "JSON":
			if len(call.Args) != 2 {
				return true
			}
			status, ok := statusCode(call.Args[0], info)
			if !ok {
				return true
			}
			si.responses[status] = info.TypeOf(call.Args[1])
		}
		return true
	})

	return si
}

func isGinContext(e ast.Expr, info *types.Info) bool {
	if info == nil {
		return false
	}
	id, ok := e.(*ast.Ident)
	if !ok {
		return false
	}
	pt, ok := info.TypeOf(id).(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := pt.Elem().(*types.Named)
	if !ok {
		return false
	}
	return named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == ginImport && named.Obj().Name() == "Context"
}

func deref(t types.Type) types.Type {
	if p, ok := t.(*types.Pointer); ok {
		return p.Elem()
	}
	return t
}

// statusCode folds a constant expression (e.g. http.StatusOK, 200) to an int.
func statusCode(e ast.Expr, info *types.Info) (int, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind == token.INT {
			n, err := strconv.Atoi(v.Value)
			return n, err == nil
		}
	case *ast.Ident, *ast.SelectorExpr:
		var obj types.Object
		if sel, ok := e.(*ast.SelectorExpr); ok {
			obj = info.ObjectOf(sel.Sel)
			if s, ok2 := info.Selections[sel]; ok2 {
				obj = s.Obj()
			}
		} else {
			obj = info.ObjectOf(e.(*ast.Ident))
		}
		c, ok := obj.(*types.Const)
		if !ok {
			return 0, false
		}
		if c.Val().Kind() == constant.Int {
			if n, ok := constant.Int64Val(c.Val()); ok {
				return int(n), true
			}
		}
	}
	return 0, false
}

// ---------- code generation ----------

type emitter struct {
	used      map[string]string // import path -> alias
	aliasUsed map[string]bool
}

func (em *emitter) register(path, name string) {
	if _, ok := em.used[path]; ok {
		return
	}
	alias := name
	if em.aliasUsed[alias] {
		for i := 2; ; i++ {
			cand := fmt.Sprintf("%s%d", name, i)
			if !em.aliasUsed[cand] {
				alias = cand
				break
			}
		}
	}
	em.aliasUsed[alias] = true
	em.used[path] = alias
}

func (em *emitter) qualifier(p *types.Package) string {
	if p == nil {
		return ""
	}
	em.register(p.Path(), p.Name())
	return em.used[p.Path()]
}

func (em *emitter) typeString(t types.Type) string {
	return types.TypeString(t, em.qualifier)
}

// zeroValue produces a non-nil, reflectable expression for t, or "" if the type
// cannot be built (interface, generic dict like gin.H).
func (em *emitter) zeroValue(t types.Type) string {
	if t == nil {
		return ""
	}
	switch v := t.(type) {
	case *types.Pointer:
		inner := em.zeroValue(v.Elem())
		if inner == "" {
			return ""
		}
		return "&" + inner

	case *types.Named:
		if em.isGenericSchema(v) {
			return ""
		}
		ts := em.typeString(v)
		switch v.Underlying().(type) {
		case *types.Struct, *types.Map, *types.Slice, *types.Array:
			return ts + "{}"
		default:
			return ""
		}

	case *types.Slice:
		et := em.concreteTS(v.Elem())
		if et == "" {
			return ""
		}
		return "[]" + et + "{}"
	case *types.Map:
		kt := em.concreteTS(v.Key())
		vt := em.concreteTS(v.Elem())
		if kt == "" || vt == "" {
			return ""
		}
		return "map[" + kt + "]" + vt + "{}"
	case *types.Array:
		et := em.concreteTS(v.Elem())
		if et == "" {
			return ""
		}
		return fmt.Sprintf("[%s]%s{}", strconv.FormatInt(v.Len(), 10), et)

	default:
		return ""
	}
}

// concreteTS returns the type expression for t (registering its package), or ""
// if t is generic/interface and should be skipped as a schema.
func (em *emitter) concreteTS(t types.Type) string {
	if em.isGenericSchema(t) {
		return ""
	}
	return em.typeString(t)
}

// isGenericSchema reports whether t should not be used as an OpenAPI schema:
// bare interfaces, generic dicts (gin.H, map[string]interface{}, ...), or
// unexported types that another package cannot reference.
func (em *emitter) isGenericSchema(t types.Type) bool {
	for {
		if p, ok := t.(*types.Pointer); ok {
			t = p.Elem()
			continue
		}
		break
	}

	if _, ok := t.(*types.Interface); ok {
		return true
	}
	if n, ok := t.(*types.Named); ok {
		if _, ok := n.Underlying().(*types.Interface); ok {
			return true
		}
		if isGenericDict(n) {
			return true
		}
		if !n.Obj().Exported() {
			return true
		}
	}
	return false
}

// isGenericDict reports whether a named type is a dict-like generic (gin.H,
// map[string]interface{}, etc.) that makes a poor OpenAPI schema.
func isGenericDict(t *types.Named) bool {
	switch u := t.Underlying().(type) {
	case *types.Map:
		return isInterfaceOrGeneric(u.Elem())
	case *types.Slice:
		return isInterfaceOrGeneric(u.Elem())
	case *types.Array:
		return isInterfaceOrGeneric(u.Elem())
	}
	return false
}

func isInterfaceOrGeneric(t types.Type) bool {
	if _, ok := t.(*types.Interface); ok {
		return true
	}
	if n, ok := t.(*types.Named); ok {
		if _, ok := n.Underlying().(*types.Interface); ok {
			return true
		}
	}
	return false
}

func render(em *emitter, pkgName string, finals []rendered) string {
	var b strings.Builder

	b.WriteString("// Code generated by ginopenapi-gen. DO NOT EDIT.\n\n")
	fmt.Fprintf(&b, "package %s\n\n", pkgName)

	b.WriteString("import (\n")
	paths := make([]string, 0, len(em.used))
	for p := range em.used {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		alias := em.used[p]
		if p == "ginopenapi" {
			alias = "ginopenapi"
		}
		if p == "net/http" {
			alias = "http"
		}
		if alias == "" || alias == p {
			fmt.Fprintf(&b, "\t%s\n", strconv.Quote(p))
		} else {
			fmt.Fprintf(&b, "\t%s %s\n", alias, strconv.Quote(p))
		}
	}
	b.WriteString(")\n\n")

	b.WriteString("func RegisterOpenAPIMetadata(api *ginopenapi.API) {\n")
	for _, f := range finals {
		b.WriteString(renderRoute(f))
	}
	b.WriteString("}\n")

	return b.String()
}

var methodConst = map[string]string{
	"GET": "http.MethodGet", "POST": "http.MethodPost", "PUT": "http.MethodPut",
	"PATCH": "http.MethodPatch", "DELETE": "http.MethodDelete",
	"HEAD": "http.MethodHead", "OPTIONS": "http.MethodOptions",
}

func renderRoute(f rendered) string {
	var b strings.Builder

	consts, ok := methodConst[f.r.method]
	if !ok {
		consts = strconv.Quote(f.r.method)
	}

	fmt.Fprintf(&b, "\tapi.DescribeRoute(\n")
	fmt.Fprintf(&b, "\t\t%s, %q,\n", consts, f.r.path)
	if f.r.summaryName != "" {
		fmt.Fprintf(&b, "\t\tginopenapi.Summary(%q),\n", f.r.summaryName)
	}
	if f.req != "" {
		fmt.Fprintf(&b, "\t\tginopenapi.Body(%s),\n", f.req)
	}
	statuses := make([]int, 0, len(f.resp))
	for st := range f.resp {
		statuses = append(statuses, st)
	}
	sort.Ints(statuses)
	for _, st := range statuses {
		fmt.Fprintf(&b, "\t\tginopenapi.Response(%d, %s),\n", st, f.resp[st])
	}
	b.WriteString("\t)\n")

	return b.String()
}

func humanize(name string) string {
	if name == "" {
		return ""
	}
	runes := []rune(name)
	var out []rune
	for i, r := range runes {
		if i > 0 && r >= 'A' && r <= 'Z' && !(runes[i-1] >= 'A' && runes[i-1] <= 'Z') {
			out = append(out, ' ')
		}
		out = append(out, r)
	}
	return string(out)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ginopenapi-gen: "+format+"\n", args...)
	os.Exit(1)
}
