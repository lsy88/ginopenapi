package ginopenapi

import (
	"net/http"
	"reflect"
)

// Operation 表示一个 OpenAPI Operation。
//
// 注意：
// Operation 只是 OpenAPI metadata，
// 不参与 Gin 的实际 HTTP 请求处理。
type Operation struct {
	Method string
	Path   string

	// Gin handler 信息，仅用于关联已有 Gin route。
	Handler     string
	HandlerFunc any

	OperationID string
	Summary     string
	Description string

	Tags       []string
	Deprecated bool
	Hidden     bool

	Parameters []ParameterSpec

	Request *RequestSpec

	Responses map[int]ResponseSpec
}

// ParameterSpec 描述 OpenAPI parameter。
type ParameterSpec struct {
	Name string

	// path / query / header / cookie
	In string

	Description string

	Required bool

	// 用于生成 JSON Schema。
	//
	// 例如：
	//
	// int(0)
	// string("")
	// uuid.UUID{}
	Type any

	Example any
}

// RequestSpec 描述 OpenAPI request body。
type RequestSpec struct {
	Body any

	ContentType string

	Required bool
}

// ResponseSpec 描述 OpenAPI response。
//
// 注意这里叫 ResponseSpec，避免与 Response()
// DSL 函数发生 Go 命名冲突。
type ResponseSpec struct {
	Description string

	Body any

	ContentType string

	Headers map[string]HeaderSpec
}

// HeaderSpec 描述 response header。
type HeaderSpec struct {
	Description string

	Type any

	Required bool
}

// OperationOption 修改 Operation。
type OperationOption func(*Operation)

// Describe 根据 handler 查找已有 Gin route。
//
// 原来的 Gin 代码不需要修改：
//
//	r.GET("/users/:id", GetUser)
//
// 然后：
//
//	api.Describe(
//	    GetUser,
//	    Summary("获取用户"),
//	    Response(200, User{}),
//	)
func (a *API) Describe(
	handler any,
	options ...OperationOption,
) *API {
	a.mu.Lock()
	defer a.mu.Unlock()

	fn := reflect.ValueOf(handler)

	if fn.Kind() != reflect.Func {
		panic("ginopenapi: handler must be a function")
	}

	ptr := fn.Pointer()

	op, ok := a.handlers[ptr]
	if !ok {
		panic(
			"ginopenapi: handler route not found; " +
				"call Scan() first",
		)
	}

	for _, option := range options {
		option(op)
	}

	a.rebuildAll()

	return a
}

// DescribeRoute 根据 HTTP method + Gin path 查找 route。
//
// 例如：
//
//	api.DescribeRoute(
//	    http.MethodGet,
//	    "/users/:id",
//	    Summary("获取用户"),
//	    Response(200, User{}),
//	)
func (a *API) DescribeRoute(
	method string,
	path string,
	options ...OperationOption,
) *API {
	a.mu.Lock()
	defer a.mu.Unlock()

	op, ok := a.routes[routeKey{
		Method: method,
		Path:   path,
	}]

	if !ok {
		panic(
			"ginopenapi: route not found: " +
				method + " " + path,
		)
	}

	for _, option := range options {
		option(op)
	}

	a.rebuildAll()

	return a
}

// OperationID 设置 operationId。
func OperationID(id string) OperationOption {
	return func(op *Operation) {
		op.OperationID = id
	}
}

// Summary 设置 summary。
func Summary(summary string) OperationOption {
	return func(op *Operation) {
		op.Summary = summary
	}
}

// Description 设置 description。
func Description(description string) OperationOption {
	return func(op *Operation) {
		op.Description = description
	}
}

// Tags 设置 tags。
func Tags(tags ...string) OperationOption {
	return func(op *Operation) {
		op.Tags = append([]string(nil), tags...)
	}
}

// Deprecated 将 API 标记为 deprecated。
func Deprecated() OperationOption {
	return func(op *Operation) {
		op.Deprecated = true
	}
}

// Hidden 隐藏这个 API。
//
// Gin 中的 route 仍然正常工作，
// 只是不会出现在 OpenAPI 中。
func Hidden() OperationOption {
	return func(op *Operation) {
		op.Hidden = true
	}
}

// Param 添加一个通用 OpenAPI parameter。
//
// in 可以是：
//
//	path
//	query
//	header
//	cookie
func Param(
	name string,
	in string,
	typ any,
) OperationOption {
	return func(op *Operation) {
		op.Parameters = append(
			op.Parameters,
			ParameterSpec{
				Name:     name,
				In:       in,
				Type:     typ,
				Required: in == "path",
			},
		)
	}
}

// PathParam 添加 path parameter。
func PathParam(
	name string,
	typ any,
) OperationOption {
	return Param(
		name,
		"path",
		typ,
	)
}

// QueryParam 添加 query parameter。
func QueryParam(
	name string,
	typ any,
) OperationOption {
	return Param(
		name,
		"query",
		typ,
	)
}

// HeaderParam 添加 header parameter。
func HeaderParam(
	name string,
	typ any,
) OperationOption {
	return Param(
		name,
		"header",
		typ,
	)
}

// CookieParam 添加 cookie parameter。
func CookieParam(
	name string,
	typ any,
) OperationOption {
	return Param(
		name,
		"cookie",
		typ,
	)
}

// Body 设置 JSON request body。
//
// 示例：
//
//	Body(CreateUserRequest{})
func Body(body any) OperationOption {
	return func(op *Operation) {
		op.Request = &RequestSpec{
			Body:        body,
			ContentType: "application/json",
			Required:    true,
		}
	}
}

// BodyOptional 设置非 required request body。
func BodyOptional(body any) OperationOption {
	return func(op *Operation) {
		op.Request = &RequestSpec{
			Body:        body,
			ContentType: "application/json",
			Required:    false,
		}
	}
}

// ContentType 修改 request body Content-Type。
//
// 一般用于：
//
//	application/json
//	multipart/form-data
//	application/x-www-form-urlencoded
func ContentType(contentType string) OperationOption {
	return func(op *Operation) {
		if op.Request == nil {
			op.Request = &RequestSpec{}
		}

		op.Request.ContentType = contentType
	}
}

// Response 添加 HTTP response。
//
// 示例：
//
//	Response(200, User{})
//
//	Response(201, User{})
//
//	Response(204, nil)
func Response(
	status int,
	body any,
) OperationOption {
	return func(op *Operation) {
		if op.Responses == nil {
			op.Responses = make(
				map[int]ResponseSpec,
			)
		}

		op.Responses[status] = ResponseSpec{
			Description: http.StatusText(status),
			Body:        body,
			ContentType: "application/json",
		}
	}
}

// ResponseDescription 修改 response description。
func ResponseDescription(
	status int,
	description string,
) OperationOption {
	return func(op *Operation) {
		if op.Responses == nil {
			op.Responses = make(
				map[int]ResponseSpec,
			)
		}

		resp := op.Responses[status]

		resp.Description = description

		op.Responses[status] = resp
	}
}

// ResponseContentType 修改 response Content-Type。
func ResponseContentType(
	status int,
	contentType string,
) OperationOption {
	return func(op *Operation) {
		if op.Responses == nil {
			op.Responses = make(
				map[int]ResponseSpec,
			)
		}

		resp := op.Responses[status]

		resp.ContentType = contentType

		op.Responses[status] = resp
	}
}

// ResponseHeader 添加 response header。
func ResponseHeader(
	status int,
	name string,
	header HeaderSpec,
) OperationOption {
	return func(op *Operation) {
		if op.Responses == nil {
			op.Responses = make(
				map[int]ResponseSpec,
			)
		}

		resp := op.Responses[status]

		if resp.Headers == nil {
			resp.Headers = make(
				map[string]HeaderSpec,
			)
		}

		resp.Headers[name] = header

		op.Responses[status] = resp
	}
}
