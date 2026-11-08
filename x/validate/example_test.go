package validate_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/birdordev/plumego/x/validate"
)

type userRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type userValidator struct{}

func (v userValidator) Validate(value any) error {
	req, ok := value.(userRequest)
	if !ok {
		return fmt.Errorf("unexpected type")
	}
	if req.Name == "" {
		return fmt.Errorf("name is required")
	}
	if req.Email == "" {
		return fmt.Errorf("email is required")
	}
	return nil
}

// ExampleBind demonstrates decoding and validating a JSON request body.
func ExampleBind() {
	body := `{"name":"Alice","email":"alice@example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(body))

	user, err := validate.Bind[userRequest](req, userValidator{})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("name:", user.Name)
	fmt.Println("email:", user.Email)

	// Output:
	// name: Alice
	// email: alice@example.com
}

// ExampleBind_missingField demonstrates validation failure for a missing field.
func ExampleBind_missingField() {
	body := `{"name":""}`
	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(body))

	_, err := validate.Bind[userRequest](req, userValidator{})
	if err != nil {
		fmt.Println("validation failed:", err)
		return
	}
	fmt.Println("ok")

	// Output:
	// validation failed: name is required
}

// ExampleBindJSON demonstrates decoding a JSON body without validation.
func ExampleBindJSON() {
	body := `{"name":"Bob","email":"bob@example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(body))

	user, err := validate.BindJSON[userRequest](req)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("name:", user.Name)

	// Output:
	// name: Bob
}
