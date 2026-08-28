package ginopenapi

import (
	_ "embed"
	"html"
	"net/http"
	"strings"
)

// Scalar 的 standalone 渲染脚本，内置进二进制，避免依赖 CDN。
//
//go:embed static/apiref.js
var scalarStandalone []byte

// DocsHandler 返回 Scalar 文档 UI 的 http.Handler。
//
// jsonPath 是 OpenAPI JSON 的地址，jsPath 是内置 standalone.js 的地址。
func (a *API) DocsHandler(jsonPath, jsPath string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		title := "Scalar in HTML"

		a.mu.RLock()

		if a.openapi != nil &&
			a.openapi.Info != nil &&
			a.openapi.Info.Title != "" {
			title = a.openapi.Info.Title + " Reference"
		}

		a.mu.RUnlock()

		// 和 Huma 的 Scalar renderer 保持一致。
		body := `<!doctype html>
<html lang="en">

<head>
	<meta charset="utf-8">
	<meta name="referrer" content="no-referrer">
	<meta
		name="viewport"
		content="width=device-width, initial-scale=1"
	>

	<title>` + html.EscapeString(title) + `</title>
</head>

<body>

<script
	id="api-reference"
	data-url="` + html.EscapeString(jsonPath) + `"
></script>

<script src="` + html.EscapeString(jsPath) + `"></script>

</body>

</html>`

		csp := strings.Join([]string{
			"default-src 'none'",
			"base-uri 'none'",
			"connect-src 'self'",
			"form-action 'none'",
			"frame-ancestors 'none'",
			"sandbox allow-same-origin allow-scripts allow-popups allow-popups-to-escape-sandbox allow-downloads",
			"script-src 'unsafe-eval' 'self'",
			"style-src 'unsafe-inline'",
		}, "; ")

		w.Header().Set(
			"Content-Security-Policy",
			csp,
		)

		w.Header().Set(
			"Content-Type",
			"text/html; charset=utf-8",
		)

		_, _ = w.Write([]byte(body))
	})
}