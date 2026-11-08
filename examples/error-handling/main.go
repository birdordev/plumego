package main

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/birdordev/plumego"
	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/middleware/recovery"
)

func main() {
	app := plumego.New()

	// Add recovery middleware to handle panics
	app.Use(recovery.Handler())

	// Route that validates input and returns an error for invalid data
	app.Post("/users", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name  string `json:"name"`
			Email string `json:"email"`
			Age   int    `json:"age"`
		}

		// Bind and return error if JSON is invalid
		if err := contract.BindJSON(r, &req); err != nil {
			contract.WriteError(w, r, err)
			return
		}

		// Validate required fields
		if req.Name == "" {
			contract.WriteError(w, r, errors.New("name is required"))
			return
		}
		if req.Email == "" {
			contract.WriteError(w, r, errors.New("email is required"))
			return
		}

		// Validate age is reasonable
		if req.Age < 0 || req.Age > 150 {
			contract.WriteError(w, r, errors.New("age must be between 0 and 150"))
			return
		}

		// Success
		contract.WriteResponse(w, r, http.StatusCreated, map[string]any{
			"message": "user created",
			"user":    req,
		}, nil)
	}))

	// Route that intentionally panics to show recovery middleware
	app.Get("/panic", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("this is a panic from a handler")
	}))

	// Route that returns a custom error
	app.Get("/error/{id}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idStr := contract.Param(r, "id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			contract.WriteError(w, r, errors.New("invalid ID format"))
			return
		}

		// Simulate not found
		if id > 100 {
			contract.WriteError(w, r, errors.New("user not found"))
			return
		}

		contract.WriteResponse(w, r, http.StatusOK, map[string]any{
			"id": id,
		}, nil)
	}))

	log.Println("server started at :8080")
	log.Println("Try:")
	log.Println("  curl -X POST http://localhost:8080/users -H 'Content-Type: application/json' -d '{\"name\":\"Alice\",\"email\":\"alice@example.com\",\"age\":30}'")
	log.Println("  curl -X POST http://localhost:8080/users -H 'Content-Type: application/json' -d '{\"name\":\"Bob\"}'  # Missing email")
	log.Println("  curl http://localhost:8080/error/200  # Not found")
	log.Println("  curl http://localhost:8080/panic  # Panic recovery")

	log.Fatal(http.ListenAndServe(":8080", app))
}
