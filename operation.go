package fuge

import (
	"context"
	"time"
)

// Operation describes one RPC method. It mirrors huma.Operation: the fields
// below are what developers fill in; Register translates them into AsyncAPI.
type Operation struct {
	// OperationID is the stable identifier in the document. Defaults to Method.
	OperationID string
	// Method is the wire name the client passes to client.rpc(). Required.
	Method      string
	Summary     string
	Description string
	Tags        []string

	// Security lists acceptable schemes, drop-in compatible with huma's type:
	// each map is one alternative (OR). A map holding more than one scheme
	// (AND) cannot be expressed in AsyncAPI 3 and panics at registration.
	// Scopes are ignored: AsyncAPI 3 carries them on the scheme itself.
	Security []map[string][]string

	// Errors are extra Centrifuge error codes this method may return. Internal
	// error (100) is always documented; bad request (107) is added when the
	// request has a payload and validation (422) when it implements Validator.
	Errors []uint32

	// Extensions are "x-" keys added to the AsyncAPI operation.
	Extensions map[string]any

	// Timeout overrides the router default for this method.
	Timeout time.Duration
}

// OpHandler is the raw, codec-level handler an Operation is bound to.
type OpHandler func(ctx context.Context, data []byte) ([]byte, error)

// OpMiddleware wraps an OpHandler and can inspect the operation being called,
// which is what lets a security middleware read op.Security or op.Extensions.
type OpMiddleware func(op *Operation, next OpHandler) OpHandler

type opKey struct{}

// OperationFrom returns the operation being executed. Available to OpMiddleware
// and OpHandler (not to Middleware, which runs one layer lower, in the router).
func OperationFrom(ctx context.Context) (*Operation, bool) {
	op, ok := ctx.Value(opKey{}).(*Operation)
	return op, ok
}
