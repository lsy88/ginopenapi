package ginopenapi

import (
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// rebuildAll 根据当前 routes registry 重新构建 OpenAPI Paths。
//
// 采用“整体重建”而非往旧 Paths 中 AddOperation，
// 这样 DescribeRoute 修改 metadata 后不会产生重复的 OpenAPI path。
// 按注册顺序（a.ordered）遍历，保证生成文档的顺序稳定。
func (a *API) rebuildAll() {
	// 重新创建 Paths。
	//
	// 非常重要：
	// AddOperation() 下面面对的是一个全新的 Paths。
	a.openapi.Paths = make(map[string]*huma.PathItem)

	for _, op := range a.ordered {
		if op == nil {
			continue
		}

		if op.Hidden {
			continue
		}

		humaOp := op.toHumaOperation(a.registry)

		a.openapi.AddOperation(humaOp)
	}
}

// toHumaOperation 将 ginopenapi.Operation 转换成 Huma Operation。
// 这里只负责 OpenAPI metadata 转换，
// 不会注册任何 HTTP handler。
func (op *Operation) toHumaOperation(
	registry huma.Registry,
) *huma.Operation {

	result := &huma.Operation{
		OperationID: op.OperationID,
		Method:      op.Method,
		Path:        normalizePath(op.Path),
		Summary:     op.Summary,
		Description: op.Description,
		Tags:        append([]string(nil), op.Tags...),
		Deprecated:  op.Deprecated,

		Parameters: nil,
		Responses:  make(map[string]*huma.Response),
	}

	// ============================================================
	// Parameters
	// ============================================================

	for _, p := range op.Parameters {
		param := &huma.Param{
			Name:        p.Name,
			In:          p.In,
			Description: p.Description,
			Required:    p.Required,
		}

		if p.Type != nil {
			param.Schema = schemaFromValue(
				registry,
				p.Type,
			)
		}

		if p.Example != nil {
			param.Example = p.Example
		}

		result.Parameters = append(
			result.Parameters,
			param,
		)
	}

	// ============================================================
	// 自动从 Path 中发现 path parameters
	// ============================================================

	knownParams := make(map[string]bool)

	for _, p := range result.Parameters {
		if p.In == "path" {
			knownParams[p.Name] = true
		}
	}

	for _, name := range pathParameters(result.Path) {
		if knownParams[name] {
			continue
		}

		result.Parameters = append(
			result.Parameters,
			&huma.Param{
				Name:     name,
				In:       "path",
				Required: true,
				Schema: &huma.Schema{
					Type: huma.TypeString,
				},
			},
		)
	}

	// ============================================================
	// Request Body
	// ============================================================

	if op.Request != nil && op.Request.Body != nil {
		contentType := op.Request.ContentType

		if contentType == "" {
			contentType = "application/json"
		}

		result.RequestBody = &huma.RequestBody{
			Required: op.Request.Required,

			Content: map[string]*huma.MediaType{
				contentType: {
					Schema: schemaFromValue(
						registry,
						op.Request.Body,
					),
				},
			},
		}
	}

	// ============================================================
	// Responses
	// ============================================================

	for status, response := range op.Responses {
		description := response.Description

		if description == "" {
			description = http.StatusText(status)
		}

		if description == "" {
			description = fmt.Sprintf(
				"HTTP %d response",
				status,
			)
		}

		humaResponse := &huma.Response{
			Description: description,
		}

		// --------------------------------------------------------
		// Response Body
		// --------------------------------------------------------

		if response.Body != nil {
			contentType := response.ContentType

			if contentType == "" {
				contentType = "application/json"
			}

			humaResponse.Content = map[string]*huma.MediaType{
				contentType: {
					Schema: schemaFromValue(
						registry,
						response.Body,
					),
				},
			}
		}

		// --------------------------------------------------------
		// Response Headers
		// --------------------------------------------------------

		if len(response.Headers) > 0 {
			humaResponse.Headers = make(
				map[string]*huma.Param,
			)

			for name, header := range response.Headers {
				humaResponse.Headers[name] = &huma.Param{
					Name:        name,
					Description: header.Description,
					In:          "header",
					Required:    header.Required,
					Schema: schemaFromValue(
						registry,
						header.Type,
					),
				}
			}
		}

		result.Responses[fmt.Sprintf("%d", status)] = humaResponse
	}

	// ============================================================
	// 默认 Response
	// ============================================================

	if len(result.Responses) == 0 {
		result.Responses["200"] = &huma.Response{
			Description: "OK",
		}
	}

	return result
}
