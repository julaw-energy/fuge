package fuge

import (
	"context"
	"errors"
	"time"

	"github.com/centrifugal/centrifuge"
)

// chain wraps h so that mws[0] is the outermost middleware.
func chain(h Handler, mws ...Middleware) Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

func withTimeout(d time.Duration) Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, req Request) (Response, error) {
			ctx, cancel := context.WithTimeout(ctx, d)
			defer cancel()
			return next(ctx, req)
		}
	}
}

// RequireUser rejects calls from anonymous connections.
func RequireUser() Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, req Request) (Response, error) {
			if info, ok := InfoFrom(ctx); !ok || info.UserID == "" {
				return Response{}, centrifuge.ErrorUnauthorized
			}
			return next(ctx, req)
		}
	}
}

// Logging logs one line per call with duration and outcome.
func Logging(l Logger) Middleware {
	return Observe(func(info CallInfo, d time.Duration, err error) {
		attrs := []any{
			"method", info.Method, "request_id", info.RequestID,
			"client_id", info.ClientID, "user_id", info.UserID, "duration", d,
		}
		var re *Error
		var ce *centrifuge.Error
		switch {
		case err == nil:
			l.Info("rpc", attrs...)
		case errors.As(err, &re) || errors.As(err, &ce):
			l.Warn("rpc", append(attrs, "err", err)...) // client-visible failure
		default:
			l.Error("rpc", append(attrs, "err", err)...)
		}
	})
}

// Observe is the hook for metrics/tracing: called after every call.
func Observe(fn func(info CallInfo, d time.Duration, err error)) Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, req Request) (Response, error) {
			start := time.Now()
			res, err := next(ctx, req)
			info, _ := InfoFrom(ctx)
			fn(info, time.Since(start), err)
			return res, err
		}
	}
}
