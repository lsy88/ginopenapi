package main

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/lsy88/ginopenapi"
)

type User struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type CreateUserRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ============================================================
// 原来的 Gin Handler
//
// 这里完全不需要修改。
// ============================================================

func GetUser(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))

	c.JSON(http.StatusOK, User{
		ID:    id,
		Name:  "Alice",
		Email: "alice@example.com",
	})
}

func ListUsers(c *gin.Context) {
	c.JSON(http.StatusOK, []User{
		{
			ID:    1,
			Name:  "Alice",
			Email: "alice@example.com",
		},
		{
			ID:    2,
			Name:  "Bob",
			Email: "bob@example.com",
		},
	})
}

func CreateUser(c *gin.Context) {
	var req CreateUserRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Code:    "BAD_REQUEST",
			Message: err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, User{
		ID:    100,
		Name:  req.Name,
		Email: req.Email,
	})
}

func DeleteUser(c *gin.Context) {
	c.Status(http.StatusNoContent)
}

// ============================================================
// Router
//
// 仍然是纯 Gin。
// ============================================================

func registerRoutes(r *gin.Engine) {
	r.GET("/users", ListUsers)
	r.GET("/users/:id", GetUser)
	r.POST("/users", CreateUser)
	r.DELETE("/users/:id", DeleteUser)
}

// ============================================================
// OpenAPI
//
// 这一层只是给已经存在的 Gin API 补充 OpenAPI metadata。
// 不参与真正的 HTTP 请求处理。
// ============================================================

func registerOpenAPI(api *ginopenapi.API) {
	api.DescribeRoute(
		http.MethodGet,
		"/users",

		ginopenapi.OperationID("listUsers"),
		ginopenapi.Summary("List users"),
		ginopenapi.Tags("Users"),

		ginopenapi.Response(
			http.StatusOK,
			[]User{},
		),
	)

	api.DescribeRoute(
		http.MethodGet,
		"/users/:id",

		ginopenapi.OperationID("getUser"),
		ginopenapi.Summary("Get user"),
		ginopenapi.Description(
			"Get a user by ID.",
		),
		ginopenapi.Tags("Users"),

		ginopenapi.PathParam(
			"id",
			int(0),
		),

		ginopenapi.Response(
			http.StatusOK,
			User{},
		),

		ginopenapi.Response(
			http.StatusNotFound,
			ErrorResponse{},
		),
	)

	api.DescribeRoute(
		http.MethodPost,
		"/users",

		ginopenapi.OperationID("createUser"),
		ginopenapi.Summary("Create user"),
		ginopenapi.Tags("Users"),

		ginopenapi.Body(
			CreateUserRequest{},
		),

		ginopenapi.Response(
			http.StatusCreated,
			User{},
		),

		ginopenapi.Response(
			http.StatusBadRequest,
			ErrorResponse{},
		),
	)

	api.DescribeRoute(
		http.MethodDelete,
		"/users/:id",

		ginopenapi.OperationID("deleteUser"),
		ginopenapi.Summary("Delete user"),
		ginopenapi.Tags("Users"),

		ginopenapi.PathParam(
			"id",
			int(0),
		),

		ginopenapi.Response(
			http.StatusNoContent,
			nil,
		),
	)
}

func main() {
	// ========================================================
	// 1. 创建 Gin
	// ========================================================

	r := gin.Default()

	// ========================================================
	// 2. 注册原来的 Gin API
	// ========================================================

	registerRoutes(r)

	// ========================================================
	// 3. 创建 ginopenapi
	// ========================================================

	api := ginopenapi.New(
		r,

		ginopenapi.WithTitle(
			"User Service",
		),

		ginopenapi.WithVersion(
			"1.0.0",
		),

		ginopenapi.WithDescription(
			"User Service API",
		),
	)

	// ========================================================
	// 4. 扫描已经存在的 Gin routes
	// ========================================================

	api.Scan()

	// ========================================================
	// 5. 补充 OpenAPI metadata
	// ========================================================

	registerOpenAPI(api)

	// ========================================================
	// 6. 注册 OpenAPI JSON / YAML / Scalar Docs
	// ========================================================

	api.Serve()

	// ========================================================
	// 7. 启动 Gin
	// ========================================================

	if err := r.Run(":8080"); err != nil {
		panic(err)
	}
}
