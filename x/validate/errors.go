package validate

import "github.com/birdordev/plumego/contract"

// ValidationError adapts validator failures to Plumego's contract error shape.
type ValidationError struct {
	contract.APIError
	Err error
}

// Error returns the underlying validator error message.
func (e ValidationError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.APIError.Error()
}

// Unwrap returns the underlying validator error.
func (e ValidationError) Unwrap() error {
	return e.Err
}

// As exposes the safe transport error while preserving the validation cause.
func (e ValidationError) As(target any) bool {
	if dest, ok := target.(*contract.APIError); ok {
		*dest = e.APIError
		return true
	}
	return false
}

func newValidationError(err error) ValidationError {
	return ValidationError{
		APIError: contract.NewErrorBuilder().
			Type(contract.TypeBadRequest).
			Code(contract.CodeValidationError).
			Message("validation failed").
			FieldViolations(err).
			Build(),
		Err: err,
	}
}
