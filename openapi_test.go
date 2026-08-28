package ginopenapi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

func newTestEngine(t *testing.T) *gin.Engine {
	t.Helper()

	gin.SetMode(gin.TestMode)

	r := gin.New()

	dir := t.TempDir()

	for _, name := range []string{"app.js", "favicon.ico"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// 静态资源路由
	r.Static("/assets", dir)
	r.StaticFile("/favicon.ico", filepath.Join(dir, "favicon.ico"))

	// 正常 API 路由
	r.GET("/users/:id", func(c *gin.Context) {})

	return r
}

func TestScanSkipsConfiguredStaticRoutes(t *testing.T) {
	r := newTestEngine(t)

	a := New(
		r,
		WithSkipPaths(
			"/assets/*filepath",
			"/favicon.ico",
		),
	)

	a.Scan()

	// 静态资源不应收录。
	for _, path := range []string{"/assets/{filepath}", "/favicon.ico"} {
		if a.openapi.Paths[path] != nil {
			t.Errorf("expected %q to be excluded, but got an operation", path)
		}
	}

	// 正常路由保留。
	if a.openapi.Paths["/users/{id}"] == nil {
		t.Error("expected /users/{id} operation to be present")
	}
}

func TestScanAutoExcludesServePaths(t *testing.T) {
	r := newTestEngine(t)

	a := New(r)

	a.Scan()

	// Serve 会把 /openapi.json 等注册到 engine 上。
	a.Serve()

	// Refresh 重扫后，自曝路径不应出现在自己的文档里。
	a.Refresh()

	for _, path := range []string{"/openapi.json", "/openapi.yaml", "/docs", "/docs/scalar.js"} {
		if a.openapi.Paths[path] != nil {
			t.Errorf("expected serve path %q to be auto-excluded, but got an operation", path)
		}
	}

	if a.openapi.Paths["/users/{id}"] == nil {
		t.Error("expected /users/{id} operation to remain after Refresh")
	}
}

func TestWithSkipRoutePredicate(t *testing.T) {
	r := newTestEngine(t)

	a := New(
		r,
		WithSkipRoute(func(method, path string) bool {
			return path == "/favicon.ico"
		}),
	)

	a.Scan()

	if a.openapi.Paths["/favicon.ico"] != nil {
		t.Error("expected predicate-skipped /favicon.ico to be excluded")
	}

	if a.openapi.Paths["/users/{id}"] == nil {
		t.Error("expected /users/{id} operation to be present")
	}
}

func TestWithSkipPathsGlob(t *testing.T) {
	r := gin.New()

	dir := t.TempDir()

	for _, name := range []string{"favicon.ico", "favicon-32x32.png", "icon.png", "icon-128x128.png", "app.js"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	r.StaticFile("/favicon.ico", filepath.Join(dir, "favicon.ico"))
	r.StaticFile("/favicon-32x32.png", filepath.Join(dir, "favicon-32x32.png"))
	r.StaticFile("/icon.png", filepath.Join(dir, "icon.png"))
	r.StaticFile("/icon-128x128.png", filepath.Join(dir, "icon-128x128.png"))
	r.GET("/users/:id", func(c *gin.Context) {})

	a := New(
		r,
		WithSkipPaths("/favicon*", "/icon*"),
	)

	a.Scan()

	for _, path := range []string{"/favicon.ico", "/favicon-32x32.png", "/icon.png", "/icon-128x128.png"} {
		if a.openapi.Paths[path] != nil {
			t.Errorf("expected glob-skipped %q to be excluded", path)
		}
	}

	if a.openapi.Paths["/users/{id}"] == nil {
		t.Error("expected /users/{id} operation to be present")
	}
}