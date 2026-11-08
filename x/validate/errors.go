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

func newValidationError(err error) ValidationError {
	return ValidationError{
		APIError: contract.NewErrorBuilder().
			Type(contract.TypeBadRequest).
			Code(contract.CodeValidationError).
			Message("validation failed").
			Build(),
		Err: err,
	}
}
