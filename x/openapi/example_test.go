package openapi_test

import (
	"fmt"
	"net/http"

	"github.com/birdordev/plumego/router"
	"github.com/birdordev/plumego/x/openapi"
)

// ExampleNew demonstrates generating an OpenAPI document from route info.
func ExampleNew() {
	gen := openapi.New()
	doc := gen.Generate(
		[]router.RouteInfo{
			{Method: http.MethodGet, Path: "/users"},
			{Method: http.MethodPost, Path: "/users"},
			{Method: http.MethodGet, Path: "/users/:id"},
		},
		map[string]openapi.Op{
			"GET /users": {
				Summary: "List users",
				Responses: map[string]openapi.Response{
					"200": {Description: "A paginated list of users"},
				},
			},
			"POST /users": {
				Summary: "Create a user",
				Responses: map[string]openapi.Response{
					"201": {Description: "User created"},
				},
			},
			"GET /users/:id": {
				Summary: "Show a user",
				Params: []openapi.Param{
					openapi.PathParam("id", openapi.String),
				},
				Responses: map[string]openapi.Response{
					"200": {Description: "The requested user"},
				},
			},
		},
	)

	fmt.Println("OpenAPI version:", doc.OpenAPI)
	fmt.Println("Paths:", len(doc.Paths))

	// Route paths are converted to OpenAPI templates.
	userPath := doc.Paths["/users/{id}"]
	fmt.Println("Show user summary:", userPath.Get.Summary)

	// Output:
	// OpenAPI version: 3.1.0
	// Paths: 2
	// Show user summary: Show a user
}

// ExampleMarshalJSON demonstrates serializing an OpenAPI document as JSON.
func ExampleMarshalJSON() {
	doc := openapi.Document{
		OpenAPI: "3.1.0",
		Info:    openapi.Info{Title: "Example API", Version: "1.0.0"},
	}

	data, err := openapi.MarshalJSON(doc)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(string(data))

	// Output:
	// {
	//   "openapi": "3.1.0",
	//   "info": {
	//     "title": "Example API",
	//     "version": "1.0.0"
	//   },
	//   "paths": null
	// }
}
