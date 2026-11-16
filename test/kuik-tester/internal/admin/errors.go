package admin

import (
	"errors"
	"fmt"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/dest"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
)

// ErrNotFound is an unknown id, name, host or quota; the API maps it to 404.
var ErrNotFound = errors.New("not found")

// ValidationError is a rejected input; the API maps it to 400.
type ValidationError struct {
	Msg   string
	cause error // the error it reports, when it comes from a lower layer
}

func (e *ValidationError) Error() string { return e.Msg }
func (e *ValidationError) Unwrap() error { return e.cause }

func invalid(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// backendError marks a failure of a destination backend; the API answers 502.
type backendError struct{ err error }

func (e *backendError) Error() string { return e.err.Error() }
func (e *backendError) Unwrap() error { return e.err }

// destErr maps the error of a destination action: dest.ErrInvalid is a
// ValidationError, dest.ErrBackend a backendError; anything else (an unknown
// host) is returned as is.
func destErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, dest.ErrInvalid):
		return &ValidationError{Msg: err.Error(), cause: err}
	case errors.Is(err, dest.ErrBackend):
		return &backendError{err}
	}
	return err
}

// importErr turns an unreadable import into a ValidationError.
func importErr(err error) error {
	if _, bad := errors.AsType[*state.InvalidError](err); bad {
		return &ValidationError{Msg: err.Error()}
	}
	return err
}
