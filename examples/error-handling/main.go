package main

import (
	"log"
	"net/http"
	"strconv"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/core"
	"github.com/birdordev/plumego/middleware/recovery"
	"github.com/birdordev/plumego/router"
)

func main() {
	app := core.New(core.DefaultConfig(), core.AppDependencies{})

	// Add recovery middleware to handle panics
	recoverPanics, err := recovery.Middleware(recovery.Config{Logger: app.Logger()})
	if err != nil {
		log.Fatal(err)
	}
	if err := app.Use(recoverPanics); err != nil {
		log.Fatal(err)
	}

	// Route that validates input and returns an error for invalid data
	if err := app.Post("/users", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name  string `json:"name"`
			Email string `json:"email"`
			Age   int    `json:"age"`
		}

		// Bind and return error if JSON is invalid
		if err := contract.DecodeJSONRequest(r, &req, contract.JSONRequestOptions{}); err != nil {
			apiErr, _ := contract.AsAPIError(err)
			_ = contract.WriteError(w, r, apiErr)
			return
		}

		// Validate required fields
		if req.Name == "" {
			_ = contract.WriteError(w, r, contract.NewErrorBuilder().Type(contract.TypeRequired).Message("name is required").Build())
			return
		}
		if req.Email == "" {
			_ = contract.WriteError(w, r, contract.NewErrorBuilder().Type(contract.TypeRequired).Message("email is required").Build())
			return
		}

		// Validate age is reasonable
		if req.Age < 0 || req.Age > 150 {
			_ = contract.WriteError(w, r, contract.NewErrorBuilder().Type(contract.TypeValidation).Message("age must be between 0 and 150").Build())
			return
		}

		// Success
		_ = contract.WriteResponse(w, r, http.StatusCreated, map[string]any{
			"message": "user created",
			"user":    req,
		}, nil)
	})); err != nil {
		log.Fatal(err)
	}

	// Route that intentionally panics to show recovery middleware
	if err := app.Get("/panic", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("this is a panic from a handler")
	})); err != nil {
		log.Fatal(err)
	}

	// Route that returns a custom error
	if err := app.Get("/error/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idStr := router.Param(r, "id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			_ = contract.WriteError(w, r, contract.NewErrorBuilder().Type(contract.TypeBadRequest).Message("invalid ID format").Build())
			return
		}

		// Simulate not found
		if id > 100 {
			_ = contract.WriteError(w, r, contract.NewErrorBuilder().Type(contract.TypeNotFound).Message("user not found").Build())
			return
		}

		_ = contract.WriteResponse(w, r, http.StatusOK, map[string]any{
			"id": id,
		}, nil)
	})); err != nil {
		log.Fatal(err)
	}

	log.Println("server started at :8080")
	log.Println("Try:")
	log.Println("  curl -X POST http://localhost:8080/users -H 'Content-Type: application/json' -d '{\"name\":\"Alice\",\"email\":\"alice@example.com\",\"age\":30}'")
	log.Println("  curl -X POST http://localhost:8080/users -H 'Content-Type: application/json' -d '{\"name\":\"Bob\"}'  # Missing email")
	log.Println("  curl http://localhost:8080/error/200  # Not found")
	log.Println("  curl http://localhost:8080/panic  # Panic recovery")

	log.Fatal(http.ListenAndServe(":8080", app))
}
