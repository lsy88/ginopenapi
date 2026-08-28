package ginopenapi

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
)

type ServeConfig struct {
	JSONPath string
	YAMLPath string
	DocsPath string
	DocsJS   string
}

type ServeOption func(*ServeConfig)

func defaultServeConfig() ServeConfig {
	return ServeConfig{
		JSONPath: "/openapi.json",
		YAMLPath: "/openapi.yaml",
		DocsPath: "/docs",
		DocsJS:   "/docs/scalar.js",
	}
}

func WithJSONPath(path string) ServeOption {
	return func(c *ServeConfig) {
		c.JSONPath = path
	}
}

func WithYAMLPath(path string) ServeOption {
	return func(c *ServeConfig) {
		c.YAMLPath = path
	}
}

func WithDocsPath(path string) ServeOption {
	return func(c *ServeConfig) {
		c.DocsPath = path
	}
}

// WithDocsJSPath 修改内置 Scalar standalone.js 的访问路径。
//
// 传入空字符串可禁用文档 UI 脚本入口。
func WithDocsJSPath(path string) ServeOption {
	return func(c *ServeConfig) {
		c.DocsJS = path
	}
}

// Serve 将 OpenAPI JSON/YAML 和文档 UI 挂到构造时传入的 Gin Engine 上。
func (a *API) Serve(options ...ServeOption) {
	cfg := defaultServeConfig()

	for _, option := range options {
		option(&cfg)
	}

	// 把实际 serve 路径写回 API，Scan/Refresh 会自动排除这些自曝路径，
	// 避免 /openapi.json 出现在它自己的文档里。
	a.jsonPath = cfg.JSONPath
	a.yamlPath = cfg.YAMLPath
	a.docsPath = cfg.DocsPath
	a.jsPath = cfg.DocsJS

	// OpenAPI JSON
	if cfg.JSONPath != "" {
		a.engine.GET(cfg.JSONPath, func(c *gin.Context) {
			a.mu.RLock()
			data, err := json.MarshalIndent(
				a.OpenAPI(),
				"",
				"  ",
			)
			a.mu.RUnlock()

			if err != nil {
				c.AbortWithStatusJSON(
					http.StatusInternalServerError,
					gin.H{
						"error": err.Error(),
					},
				)
				return
			}

			c.Data(
				http.StatusOK,
				"application/json; charset=utf-8",
				data,
			)
		})
	}

	// OpenAPI YAML
	if cfg.YAMLPath != "" {
		a.engine.GET(cfg.YAMLPath, func(c *gin.Context) {
			a.mu.RLock()
			data, err := a.OpenAPI().YAML()
			a.mu.RUnlock()

			if err != nil {
				c.AbortWithStatusJSON(
					http.StatusInternalServerError,
					gin.H{
						"error": err.Error(),
					},
				)
				return
			}

			c.Data(
				http.StatusOK,
				"application/yaml; charset=utf-8",
				data,
			)
		})
	}

	// Scalar Docs UI
	if cfg.DocsPath != "" {
		// 内置的 standalone.js，由浏览器请求（自带缓存头）。
		if cfg.DocsJS != "" {
			a.engine.GET(cfg.DocsJS, func(c *gin.Context) {
				c.Header(
					"Cache-Control",
					"public, max-age=604800",
				)

				c.Data(
					http.StatusOK,
					"application/javascript; charset=utf-8",
					scalarStandalone,
				)
			})
		}

		a.engine.GET(
			cfg.DocsPath,
			gin.WrapH(
				a.DocsHandler(cfg.JSONPath, cfg.DocsJS),
			),
		)
	}
}
