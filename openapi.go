package ginopenapi

import (
	"reflect"
	"strings"
	"sync"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gin-gonic/gin"
)

// API 是挂在 Gin 之上的 OpenAPI 文档层。
// 它不会接管 Gin 的任何请求。
type API struct {
	engine   *gin.Engine
	openapi  *huma.OpenAPI
	registry huma.Registry

	mu sync.RWMutex

	// handler -> metadata
	handlers map[uintptr]*Operation

	// method + path -> metadata
	routes map[routeKey]*Operation

	// 按注册顺序记录 operation，保证生成的文档顺序稳定。
	ordered []*Operation

	// 是否已经扫描 Gin routes。
	scanned bool

	// 排除规则，见 WithSkipPaths / WithSkipRoute。
	skip func(method, path string) bool

	// Serve 自曝的路径，Scan/Refresh 时自动排除，
	// 避免 /openapi.json 出现在它自己的文档里。
	jsonPath string
	yamlPath string
	docsPath string
	jsPath   string
}

type routeKey struct {
	Method string
	Path   string
}

// New 创建一个 OpenAPI 文档层。
//
// 注意：
//   - 不会修改 Gin Engine
//   - 不会注册 Huma handler
//   - 不会修改 Gin middleware
//   - 不会接管 HTTP 请求
func New(engine *gin.Engine, opts ...Option) *API {
	cfg := defaultConfig()

	for _, opt := range opts {
		opt(&cfg)
	}

	return &API{
		engine:   engine,
		openapi:  cfg.OpenAPI,
		registry: cfg.OpenAPI.Components.Schemas,
		handlers: make(map[uintptr]*Operation),
		routes:   make(map[routeKey]*Operation),
		skip:     cfg.skip,
	}
}

// OpenAPI 返回底层 Huma OpenAPI 对象.
func (a *API) OpenAPI() *huma.OpenAPI {
	return a.openapi
}

// Scan 扫描当前 Gin Engine 中已经注册的 routes。
//
// 例如:
//
//	r.GET("/users/:id", GetUser)
//
// 会被记录为:
//
//	GET /users/{id}
func (a *API) Scan() *API {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.scanRoutes()

	a.scanned = true

	return a
}

// scanRoutes 遍历当前 Gin routes，录入 OpenAPI。
//
// 调用方需持有 a.mu。
func (a *API) scanRoutes() {
	for _, route := range a.engine.Routes() {
		key := routeKey{
			Method: route.Method,
			Path:   route.Path,
		}

		// 跳过静态资源、文档自曝路径等不应收录的路由。
		if a.shouldSkip(route.Method, route.Path) {
			continue
		}

		// Gin 不应该出现完全相同的 method + path，
		// 但这里仍然保护一下，避免重复 AddOperation。
		if _, exists := a.routes[key]; exists {
			continue
		}

		op := &Operation{
			Method:      route.Method,
			Path:        route.Path,
			Handler:     route.Handler,
			HandlerFunc: route.HandlerFunc,
			OperationID: scanOperationID(route.Method, route.Path),
		}

		// 记录注册顺序，保证生成文档的顺序稳定。
		a.ordered = append(a.ordered, op)
		a.routes[key] = op

		// Scan 阶段先创建 OpenAPI operation。
		a.openapi.AddOperation(
			op.toHumaOperation(a.registry),
		)

		// 保存 handler -> metadata
		if route.HandlerFunc != nil {
			ptr := reflect.ValueOf(route.HandlerFunc).Pointer()

			if _, exists := a.handlers[ptr]; !exists {
				a.handlers[ptr] = op
			}
		}
	}
}

// shouldSkip 判断某个 Gin route 是否应排除在 OpenAPI 之外。
//
// 自动排除 Serve 自曝的路径（/openapi.json 等），
// 再应用使用者通过 WithSkipPaths / WithSkipRoute 配置的规则。
func (a *API) shouldSkip(method, path string) bool {
	if a.jsonPath != "" && path == a.jsonPath {
		return true
	}

	if a.yamlPath != "" && path == a.yamlPath {
		return true
	}

	if a.docsPath != "" && path == a.docsPath {
		return true
	}

	if a.jsPath != "" && path == a.jsPath {
		return true
	}

	if a.skip != nil && a.skip(method, path) {
		return true
	}

	return false
}

// Refresh 重新扫描 Gin routes。
func (a *API) Refresh() *API {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 清空 OpenAPI paths。
	a.openapi.Paths = make(map[string]*huma.PathItem)

	a.handlers = make(map[uintptr]*Operation)
	a.routes = make(map[routeKey]*Operation)
	a.ordered = nil

	a.scanned = false

	a.scanRoutes()

	a.scanned = true

	return a
}

// scanOperationID 为 Gin route 生成稳定且不会因为 hash collision
// 导致 panic 的 OperationID。
//
// 例如:
//
//	GET    /users       -> get-users
//	GET    /users/:id   -> get-users-id
//	POST   /users       -> post-users
//	DELETE /users/:id   -> delete-users-id
func scanOperationID(method, path string) string {
	method = strings.ToLower(method)

	// Gin:
	//	/users/:id
	//
	// 转成:
	//
	//	users-id
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "/")

	if path == "" {
		return method + "-root"
	}

	parts := strings.Split(path, "/")

	for i, part := range parts {
		if word, ok := strings.CutPrefix(part, ":"); ok {
			parts[i] = word
			continue
		}

		if word, ok := strings.CutPrefix(part, "*"); ok {
			parts[i] = word
			continue
		}

		// OpenAPI / Gin route 中可能出现的字符，
		// 统一转换成合法、可读的 OperationID。
		part = strings.ReplaceAll(part, ".", "-")

		parts[i] = part
	}

	name := strings.Join(parts, "-")

	name = strings.Trim(name, "-")

	if name == "" {
		name = "root"
	}

	return method + "-" + name
}
