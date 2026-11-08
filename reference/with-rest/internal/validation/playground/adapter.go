package playground

import (
	"errors"
	"fmt"
	"strings"

	validator "github.com/go-playground/validator/v10"
	plumego "github.com/birdordev/plumego/x/validate"
)

var _ plumego.Validator = (*Validator)(nil)

// Option configures Validator.
type Option func(*Validator)

// Validator adapts go-playground/validator to x/validate.Validator.
type Validator struct {
	validate  *validator.Validate
	configErr error
}

// FieldError describes one failed struct field validation.
type FieldError struct {
	Field           string `json:"field"`
	Namespace       string `json:"namespace,omitempty"`
	StructNamespace string `json:"struct_namespace,omitempty"`
	Tag             string `json:"tag"`
	Param           string `json:"param,omitempty"`
	Message         string `json:"message"`
}

// Error wraps go-playground field validation errors in a stable shape.
type Error struct {
	Fields []FieldError `json:"fields"`
}

func (e Error) Error() string {
	if len(e.Fields) == 0 {
		return "validation failed"
	}
	if len(e.Fields) == 1 {
		return e.Fields[0].Message
	}
	return fmt.Sprintf("validation failed for %d fields", len(e.Fields))
}

// NewValidator creates a go-playground-backed validator.
func NewValidator(opts ...Option) *Validator {
	v := &Validator{validate: validator.New()}
	for _, opt := range opts {
		if opt != nil {
			opt(v)
		}
	}
	return v
}

// WithTagName sets the struct tag namespace used by go-playground/validator.
func WithTagName(name string) Option {
	return func(v *Validator) {
		if name == "" {
			return
		}
		v.validate.SetTagName(name)
	}
}

// WithValidation registers a custom go-playground validation function.
func WithValidation(tag string, fn validator.Func, callValidationEvenIfNull ...bool) Option {
	return func(v *Validator) {
		if tag == "" || fn == nil {
			return
		}
		if err := v.validate.RegisterValidation(tag, fn, callValidationEvenIfNull...); err != nil {
			v.configErr = errors.Join(v.configErr, err)
		}
	}
}

// Validate delegates to go-playground/validator.
func (v *Validator) Validate(value any) error {
	if v == nil {
		return nil
	}
	if v.configErr != nil {
		return v.configErr
	}
	err := v.validate.Struct(value)
	if err == nil {
		return nil
	}

	var fieldErrors validator.ValidationErrors
	if !errors.As(err, &fieldErrors) {
		return err
	}

	out := Error{Fields: make([]FieldError, 0, len(fieldErrors))}
	for _, fieldErr := range fieldErrors {
		out.Fields = append(out.Fields, FieldError{
			Field:           fieldErr.Field(),
			Namespace:       fieldErr.Namespace(),
			StructNamespace: fieldErr.StructNamespace(),
			Tag:             fieldErr.Tag(),
			Param:           fieldErr.Param(),
			Message:         fieldMessage(fieldErr),
		})
	}
	return out
}

func fieldMessage(err validator.FieldError) string {
	field := err.Field()
	if field == "" {
		field = err.StructField()
	}
	switch err.Tag() {
	case "required":
		return field + " is required"
	case "email":
		return field + " must be a valid email address"
	case "min":
		return field + " must be at least " + err.Param()
	case "max":
		return field + " must be at most " + err.Param()
	default:
		var builder strings.Builder
		builder.WriteString(field)
		builder.WriteString(" failed ")
		builder.WriteString(err.Tag())
		builder.WriteString(" validation")
		if err.Param() != "" {
			builder.WriteString(" with parameter ")
			builder.WriteString(err.Param())
		}
		return builder.String()
	}
}
