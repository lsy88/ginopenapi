package ginopenapi

import (
	"path"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

type config struct {
	OpenAPI *huma.OpenAPI

	// skip 判断某个 Gin route 是否应从 OpenAPI 排除。
	//
	// 接收的是 Gin 原始 method + path（例如
	// "GET" + "/assets/*filepath"）。
	skip func(method, path string) bool
}

type Option func(*config)

func defaultConfig() config {
	return config{
		OpenAPI: &huma.OpenAPI{
			OpenAPI: "3.1.0",

			Info: &huma.Info{
				Title:   "Gin API",
				Version: "1.0.0",
			},

			Components: &huma.Components{
				Schemas: huma.NewMapRegistry(
					"#/components/schemas/",
					huma.DefaultSchemaNamer,
				),
			},
		},
	}
}

// WithSkipPaths 从 OpenAPI 中排除指定路径的路由（任意 HTTP method）。
//
// path 匹配的是 Gin 路由的原始路径，支持 glob 通配，
// 规则与标准库 path.Match 一致（* 匹配任意非 / 字符、? 匹配单个字符、[..]）。
// 精确写法始终可用。静态资源常用的写法：
//
//	r.Static("/assets", "./web/dist/assets")   // 注册为 GET/HEAD /assets/*filepath
//	r.StaticFile("/favicon.ico", "./favicon.ico")
//	r.StaticFile("/icon.png", "./icon.png")
//
// 对应排除：
//
//	New(r,
//	    WithSkipPaths("/assets/*filepath", "/favicon*", "/icon*"),
//	)
//
// 需要更灵活的逻辑（如按 method 判断）时，用 WithSkipRoute。
func WithSkipPaths(paths ...string) Option {
	var matchers []func(method, path string) bool

	for _, p := range paths {
		pattern := p

		if strings.ContainsAny(pattern, "*?[") {
			matchers = append(matchers, func(_, p string) bool {
				ok, _ := path.Match(pattern, p)
				return ok
			})
		} else {
			matchers = append(matchers, func(_, p string) bool {
				return p == pattern
			})
		}
	}

	return func(c *config) {
		prev := c.skip

		c.skip = func(method, path string) bool {
			if prev != nil && prev(method, path) {
				return true
			}

			for _, m := range matchers {
				if m(method, path) {
					return true
				}
			}

			return false
		}
	}
}

// WithSkipRoute 通过自定义谓词从 OpenAPI 中排除路由。
//
// 谓词接收 Gin 原始 method + path，返回 true 表示排除。
// 多次调用会叠加，任一谓词命中即排除。
func WithSkipRoute(fn func(method, path string) bool) Option {
	return func(c *config) {
		prev := c.skip

		c.skip = func(method, path string) bool {
			if prev != nil && prev(method, path) {
				return true
			}

			return fn != nil && fn(method, path)
		}
	}
}

func WithTitle(title string) Option {
	return func(c *config) {
		c.OpenAPI.Info.Title = title
	}
}

func WithVersion(version string) Option {
	return func(c *config) {
		c.OpenAPI.Info.Version = version
	}
}

func WithDescription(description string) Option {
	return func(c *config) {
		c.OpenAPI.Info.Description = description
	}
}

func WithServer(url string) Option {
	return func(c *config) {
		c.OpenAPI.Servers = append(
			c.OpenAPI.Servers,
			&huma.Server{
				URL: url,
			},
		)
	}
}

func WithOpenAPI(openapi *huma.OpenAPI) Option {
	return func(c *config) {
		c.OpenAPI = openapi

		if c.OpenAPI.Components == nil {
			c.OpenAPI.Components = &huma.Components{}
		}

		if c.OpenAPI.Components.Schemas == nil {
			c.OpenAPI.Components.Schemas = huma.NewMapRegistry(
				"#/components/schemas/",
				huma.DefaultSchemaNamer,
			)
		}
	}
}
