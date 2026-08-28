# ginopenapi-gen

`//go:generate` 静态分析器：扫描 Gin 路由注册与其 handler 源码，自动生成填好请求体 / 响应 schema 的 OpenAPI 描述代码（`ginopenapi` 配套工具）。

它解决的是**手工为大量接口补元数据**的重复劳动。原理是静态分析：直接读源码，把每个 handler 函数体里的 `c.ShouldBindJSON(&req)`、`c.JSON(status, resp)` 提取成类型，产出 `api.DescribeRoute(...)` 调用。

## 为什么不能只用运行时反射

Gin 的 handler 是 `func(*gin.Context)`，函数体内的 `c.JSON(200, User{})` 里的 `User` 类型在**运行时拿不到**（反射只能看到 `*gin.Context` 参数）。只有静态分析源码才能拿到真实 schema。且实际项目中 handler 常写成 `app.AuthHandler.Login` 这种**跨包 struct 方法选择器**，纯 AST 名字匹配不可行 —— 所以要基于 `go/packages` + `go/types` 做完整类型解析。

## 构建 / 安装

模块路径：`github.com/lsy88/ginopenapi/tools/ginopenapi-gen`。

直接安装（放到 `$GOPATH/bin`，通常已加入 `PATH`）：

```bash
go install github.com/lsy88/ginopenapi/tools/ginopenapi-gen@latest
```

本地构建：

```bash
cd ginopenapi/tools/ginopenapi-gen
go build -o ginopenapi-gen.exe .
```

> 子模块（嵌套在仓库下的独立 go.mod）打版本时，Go 用 `tools/ginopenapi-gen/vX.Y.Z` 形式的后缀 tag，不是主模块的 `vX.Y.Z`。

## 集成到项目

在你的 Gin 项目里加一条 `//go:generate` 指令（放在路由包）：

```go
//go:generate ginopenapi-gen -patterns ./internal/... -out ./internal/router/openapi_gen.go
```

重新生成：

```bash
go generate ./internal/router/
```

工具会自动定位模块根目录（`go generate` 从任意子包运行都能正确解析模块路径）。

### 在 `Setup` 里调用生成的函数

```go
api := ginopenapi.New(r, ginopenapi.WithTitle("Achilles"), ...)
api.Scan()
RegisterOpenAPIMetadata(api) // 生成的文件提供的函数
api.Serve()
```

生成的 `openapi_gen.go` 顶部标着 `DO NOT EDIT`，改路由或 handler 后重跑 `go generate` 即可。

## 参数

| 参数 | 默认 | 说明 |
|---|---|---|
| `-patterns` | `./internal/...` | 要加载/分析的包（空格分隔），须覆盖 handler / service / model 所在包 |
| `-out` | `./internal/router/openapi_gen.go` | 输出文件 |
| `-debug` | `false` | 打印 handler 解析诊断 |

## 工作原理

1. **加载**：`packages.Load` 加载 `-patterns`，拿到每个包的 `Syntax` + `TypesInfo`。
2. **找路由**：遍历每个带 `*gin.Engine` 参数的函数，跟踪 `.Group("/x")` 链还原完整路径，识别 `r.METHOD("/path", handler)` 调用（校验方法确属 gin 包，避免误判）。
3. **解析 handler**：把 `app.X.Method` 选择器经 `go/types` 解析成 `(*types.Func)`，再用**对象身份**（`ObjectOf`）回链到对应 `FuncDecl` —— 比按位置匹配更稳。
4. **函数体分析**：
   - `c.ShouldBindJSON(&req)` / `c.BindJSON(&req)` → 请求体 schema（解引用）
   - `c.JSON(<status>, <value>)` → 响应 schema（status 常量折叠，`http.StatusOK` → 200）
   - 多种 status 都会记录（200/201/400/…）
5. **生成零值表达式**：`&T{}`、`[]T{}`、`map[K]V{}`、实例化泛型 `&handler.ListResponse[model.Project]{}` 等，保证可反射出类型、且生成代码可编译。
6. **输出**：gofmt 格式化、幂等（重复运行字节一致）。

## 生成的代码示例

```go
api.DescribeRoute(
    http.MethodPost, "/api/v1/auth/login",
    ginopenapi.Summary("Login"),
    ginopenapi.Body(service.LoginRequest{}),
    ginopenapi.Response(200, &service.LoginResponse{}),
    ginopenapi.Response(400, &handler.ErrorResponse{}),
    ginopenapi.Response(401, &handler.ErrorResponse{}),
)
```

- **Summary**：由 handler 方法名人性化（`ListNodes` → `List nodes`）。
- **OperationID / path 参数**：不再重复生成，沿用 `Scan` 自动推导（方法+路径）。
- **静态资源 / 文档自曝路径 / favicon**：仍由 `WithSkipPaths` 排除，生成器不会触及这些路由。

## 支持的边界 / 当前限制

- **不填 schema 的类型**：`gin.H`、`map[string]interface{}`、interface、**未导出的类型**（另一包无法引用，如 `handler.policyRuleRequest`）。这些路由会**保留**，只是不填 schema（响应降级为默认 `200`）。
- **工厂闭包**（如 `handleWebSocket(...)` 返回 `gin.HandlerFunc`）：解析到的是调用表达式而非命名 handler，会生成最小的 `DescribeRoute`（不填 schema）。
- `c.File` / 流式返回 / WebSocket 这类非 JSON 响应不产出响应 schema。
- 仅分析属于被加载模块的包；第三方模块里的 handler 需要额外 `-patterns`。

## 验证思路

- `go generate` 后 `go build` 应零改动通过；连跑两次输出应逐字节一致（幂等）。
- 启动服务访问 `/openapi.json`，抽查 `POST /api/v1/auth/login` 是否带上了 `LoginRequest` 请求体与 `LoginResponse` 响应。