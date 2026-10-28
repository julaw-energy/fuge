package fuge

import (
	"context"
	"fmt"
	"reflect"

	"github.com/centrifugal/centrifuge"
)

// Register binds a typed handler and documents it, mirroring huma.Register.
//
//	fuge.Register(api, fuge.Operation{
//	    OperationID: "create-country",
//	    Method:      "country.create",
//	    Summary:     "Create a new country",
//	}, createCountry) // func(ctx, *CreateCountryRequest) (*Country, error)
//
// Req and Res are inferred from the handler. Use struct{} for "no payload".
// Returning a nil *Res replies with no data. Misconfiguration (duplicate
// names, unknown security scheme, conflicting schemas) panics at startup.
func Register[Req, Res any](r Registrar, op Operation, fn func(ctx context.Context, req *Req) (*Res, error)) {
	a := r.api()
	op = r.prepare(op)

	codec := a.codec
	method := op.Method
	_, hasValidator := any(new(Req)).(Validator)

	base := func(ctx context.Context, data []byte) ([]byte, error) {
		in := new(Req)
		if len(data) > 0 {
			if err := codec.Unmarshal(data, in); err != nil {
				// Detail stays server-side; the client only sees "bad request".
				return nil, fmt.Errorf("decode %s: %v: %w", method, err, centrifuge.ErrorBadRequest)
			}
		}
		if v, ok := any(in).(Validator); ok {
			if err := v.Validate(); err != nil {
				return nil, &centrifuge.Error{Code: CodeValidation, Message: err.Error()}
			}
		}
		out, err := fn(ctx, in)
		if err != nil {
			return nil, err
		}
		if out == nil {
			return nil, nil
		}
		b, err := codec.Marshal(out)
		if err != nil {
			return nil, fmt.Errorf("encode %s: %w", method, err)
		}
		return b, nil
	}

	a.bind(op, reflect.TypeFor[Req](), reflect.TypeFor[Res](), hasValidator, base)
}

func (a *API) bind(op Operation, reqT, resT reflect.Type, hasValidator bool, base OpHandler) {
	if op.Method == "" {
		panic("fuge: operation without Method")
	}
	if op.OperationID == "" {
		op.OperationID = op.Method
	}
	if err := validateName(op.Method); err != nil {
		panic(fmt.Sprintf("fuge: invalid method %q: %v", op.Method, err))
	}
	if err := validateName(op.OperationID); err != nil {
		panic(fmt.Sprintf("fuge: invalid operation id %q: %v", op.OperationID, err))
	}

	opCopy := op // stable pointer handed to middleware and stored in context
	mws := a.commit(&opCopy, reqT, resT, hasValidator)

	h := base
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](&opCopy, h)
	}
	wrapped := func(ctx context.Context, data []byte) ([]byte, error) {
		return h(context.WithValue(ctx, opKey{}, &opCopy), data)
	}

	var opts []RouteOption
	if opCopy.Timeout > 0 {
		opts = append(opts, WithTimeout(opCopy.Timeout))
	}
	// Adapt the operation-level, byte-in/byte-out handler onto the Router's
	// own Request/Response-shaped Handler — the one place these two layers meet.
	a.router.Handle(opCopy.Method, func(ctx context.Context, req Request) (Response, error) {
		out, err := wrapped(ctx, req.Data)
		if err != nil {
			return Response{}, err
		}
		return Response{Data: out}, nil
	}, opts...)
}
