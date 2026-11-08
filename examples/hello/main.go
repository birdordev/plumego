package main

import (
	"log"
	"net/http"

	"github.com/birdordev/plumego"
	"github.com/birdordev/plumego/contract"
)

func main() {
	app := plumego.New()

	app.Get("/ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := contract.WriteResponse(w, r, http.StatusOK, map[string]string{
			"message": "pong",
		}, nil); err != nil {
			http.Error(w, "write response", http.StatusInternalServerError)
		}
	}))

	log.Println("server started at :8080")
	log.Fatal(http.ListenAndServe(":8080", app))
}
