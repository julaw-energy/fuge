package fuge

import (
	"context"
	"errors"
	"fmt"

	"github.com/centrifugal/centrifuge"
)

// Application error codes. Centrifuge reserves the low ranges for itself, so
// keep custom codes >= 400.
const (
	CodeTimeout    uint32 = 408
	CodeNotFound   uint32 = 404
	CodeConflict   uint32 = 409
	CodeValidation uint32 = 422
)

// Error is a client-visible RPC error. Only Code, Message, and Temporary are
// sent over the wire; the wrapped cause stays server-side (logs only).
type Error struct {
	Code      uint32
	Message   string
	Temporary bool
	cause     error
}

var (
	ErrNotFound   = NewError(CodeNotFound, "not found")
	ErrConflict   = NewError(CodeConflict, "conflict")
	ErrValidation = NewError(CodeValidation, "validation failed")
	ErrTimeout    = &Error{Code: CodeTimeout, Message: "request timed out", Temporary: true}
)

func NewError(code uint32, message string) *Error {
	return &Error{Code: code, Message: message}
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("rpc error %d: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.cause }

// Is makes errors.Is(err, ErrNotFound) work for any error with the same code,
// including copies produced by WithMessage / Wrap.
func (e *Error) Is(target error) bool {
	var t *Error
	ok := errors.As(target, &t)
	return ok && t.Code == e.Code
}

// WithMessage returns a copy with a different client-visible message.
func (e *Error) WithMessage(msg string) *Error {
	c := *e
	c.Message = msg
	return &c
}

// Wrap returns a copy that carries a server-side cause (never sent to clients).
func (e *Error) Wrap(cause error) *Error {
	c := *e
	c.cause = cause
	return &c
}

// ErrorMapper lets you translate domain errors (pgx.ErrNoRows, your own
// sentinel errors...) into client-visible errors. Return ok=false to fall
// through to the default handling.
type ErrorMapper func(err error) (*Error, bool)

func (r *Router) mapError(info CallInfo, err error) error {
	// Already a Centrifuge error: pass through untouched.
	var ce *centrifuge.Error
	if errors.As(err, &ce) {
		return ce
	}

	var re *Error
	if !errors.As(err, &re) && r.cfg.mapper != nil {
		if mapped, ok := r.cfg.mapper(err); ok {
			re = mapped
		}
	}
	if re == nil && errors.Is(err, context.DeadlineExceeded) {
		re = ErrTimeout
	}

	if re != nil {
		return &centrifuge.Error{Code: re.Code, Message: re.Message, Temporary: re.Temporary}
	}

	// Unknown error: never leak details to the client.
	r.cfg.logger.Error("fuge: unhandled error",
		"method", info.Method, "request_id", info.RequestID,
		"client_id", info.ClientID, "user_id", info.UserID, "err", err)
	return centrifuge.ErrorInternal
}
