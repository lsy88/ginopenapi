package ginopenapi

import (
	"regexp"
	"strings"
)

var (
	ginParamRegexp    = regexp.MustCompile(`:([a-zA-Z0-9_]+)`)
	ginWildcardRegexp = regexp.MustCompile(`\*([a-zA-Z0-9_]+)`)
)

// normalizePath 将 Gin path 转成 OpenAPI path。
//
// /users/:id
//
//	↓
//
// /users/{id}
//
// /files/*path
//
//	↓
//
// /files/{path}
func normalizePath(path string) string {
	path = ginParamRegexp.ReplaceAllString(path, "{$1}")
	path = ginWildcardRegexp.ReplaceAllString(path, "{$1}")

	return path
}

// pathParameters 返回 path 中的参数名称。
//
// /users/{id}/posts/{postID}
//
// => ["id", "postID"]
func pathParameters(path string) []string {
	var result []string

	for _, segment := range strings.Split(path, "/") {
		if strings.HasPrefix(segment, "{") &&
			strings.HasSuffix(segment, "}") {

			name := strings.TrimSuffix(
				strings.TrimPrefix(segment, "{"),
				"}",
			)

			if name != "" {
				result = append(result, name)
			}
		}
	}

	return result
}
