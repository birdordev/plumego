package rest_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/router"
	"github.com/birdordev/plumego/x/rest"
)

type exampleUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type exampleUserRepo struct {
	users []exampleUser
}

func (r exampleUserRepo) FindAll(_ context.Context, _ *rest.QueryParams) ([]exampleUser, int64, error) {
	return r.users, int64(len(r.users)), nil
}

func (exampleUserRepo) FindByID(_ context.Context, _ string) (*exampleUser, error) {
	return nil, nil
}

func (exampleUserRepo) Create(_ context.Context, _ *exampleUser) error {
	return nil
}

func (exampleUserRepo) Update(_ context.Context, _ string, _ *exampleUser) error {
	return nil
}

func (exampleUserRepo) Delete(_ context.Context, _ string) error {
	return nil
}

func (exampleUserRepo) Count(_ context.Context, _ *rest.QueryParams) (int64, error) {
	return 0, nil
}

func (exampleUserRepo) Exists(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func ExampleRegisterResourceRoutes() {
	r := router.NewRouter()
	spec := rest.DefaultResourceSpec("users").WithPrefix("/api/users")
	repository := exampleUserRepo{
		users: []exampleUser{{ID: "u_1", Name: "Ada"}},
	}
	controller := rest.NewDBResource[exampleUser](spec, repository)

	if err := rest.RegisterResourceRoutes(r, spec.Prefix, controller, rest.DefaultRouteOptions()); err != nil {
		panic(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var resp struct {
		Data []exampleUser `json:"data"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)

	fmt.Printf("GET /api/users -> %d (%d item)\n", rec.Code, len(resp.Data))
	fmt.Println("content-type:", rec.Header().Get(contract.HeaderContentType))
	for _, route := range r.Routes() {
		fmt.Printf("%s %s\n", route.Method, route.Path)
	}

	// Output:
	// GET /api/users -> 200 (1 item)
	// content-type: application/json
	// DELETE /api/users/:id
	// DELETE /api/users/batch
	// GET /api/users
	// GET /api/users/:id
	// HEAD /api/users
	// HEAD /api/users/:id
	// OPTIONS /api/users
	// OPTIONS /api/users/:id
	// PATCH /api/users/:id
	// POST /api/users
	// POST /api/users/batch
	// PUT /api/users/:id
}
