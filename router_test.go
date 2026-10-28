package fuge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/centrifugal/centrifuge"
)

// addHandler is a raw Handler standing in for what, before the router and
// the documenting API layer merged, a typed handler adapted by Typed() would
// have produced. Decode/validate/encode now happens one layer up, in
// Register (see register.go) — already exercised end-to-end by
// TestHandlerBehaviour — so these tests stick to what Router itself is
// responsible for: dispatch, groups, and middleware ordering.
func addHandler(_ context.Context, req Request) (Response, error) {
	var in struct{ A, B int }
	if err := json.Unmarshal(req.Data, &in); err != nil {
		return Response{}, fmt.Errorf("decode: %w", err)
	}
	out, err := json.Marshal(struct{ Sum int }{in.A + in.B})
	if err != nil {
		return Response{}, err
	}
	return Response{Data: out}, nil
}

func TestGroupsAndMiddlewareOrder(t *testing.T) {
	var trace []string
	mk := func(name string) Middleware {
		return func(next Handler) Handler {
			return func(ctx context.Context, req Request) (Response, error) {
				trace = append(trace, name)
				return next(ctx, req)
			}
		}
	}
	r := NewRouter()
	billing := r.RouteGroup("billing")
	billing.Handle("add", addHandler, WithMiddleware(mk("route")))
	// Registered AFTER the route: must still apply (order-independent).
	billing.Use(mk("group"))
	r.Use(mk("global"))

	out, err := r.Call(context.Background(), "billing.add", []byte(`{"A":1,"B":2}`))
	if err != nil || string(out) != `{"Sum":3}` {
		t.Fatalf("got %s, %v", out, err)
	}
	if got := strings.Join(trace, ","); got != "global,group,route" {
		t.Fatalf("order: %s", got)
	}
}

func TestPanicIsContained(t *testing.T) {
	r := NewRouter()
	r.Handle("boom", func(context.Context, Request) (Response, error) { panic("x") })
	if _, err := r.Call(context.Background(), "boom", nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestDuplicateAndFrozen(t *testing.T) {
	r := NewRouter()
	r.Handle("a", addHandler)
	mustPanic(t, "", func() { r.Handle("a", addHandler) })
	mustPanic(t, "", func() { r.Handle("bad name", nil) })
	_, _ = r.Call(context.Background(), "a", nil)
	mustPanic(t, "", func() { r.Handle("late", addHandler) })
}

func TestTimeoutMapping(t *testing.T) {
	r := NewRouter(WithDefaultTimeout(20 * time.Millisecond))
	r.Handle("slow", func(ctx context.Context, _ Request) (Response, error) {
		<-ctx.Done()
		return Response{}, ctx.Err()
	})
	_, err := r.Call(context.Background(), "slow", nil)
	wire := r.mapError(CallInfo{}, err).(*centrifuge.Error)
	if wire.Code != CodeTimeout {
		t.Fatalf("got %v", wire)
	}
}

// fakeConn stands in for *centrifuge.Client, which cannot be constructed
// without a running node.
type fakeConn struct{}

func (fakeConn) ID() string               { return "c1" }
func (fakeConn) UserID() string           { return "u1" }
func (fakeConn) Context() context.Context { return context.Background() }

func TestServeAndInflightLimit(t *testing.T) {
	release := make(chan struct{})
	r := NewRouter(WithMaxInflight(1))
	r.Handle("hold", func(ctx context.Context, _ Request) (Response, error) {
		info, _ := InfoFrom(ctx)
		if info.UserID != "u1" || info.ClientID != "c1" || info.RequestID == "" {
			return Response{}, errors.New("missing info")
		}
		<-release
		return Response{Data: []byte(`1`)}, nil
	}, WithMiddleware(RequireUser()))

	c := fakeConn{}

	done := make(chan error, 1)
	r.serve(c, centrifuge.RPCEvent{Method: "hold"}, func(_ centrifuge.RPCReply, err error) { done <- err })

	// Second call while the first still holds the only slot.
	lim := make(chan error, 1)
	r.serve(c, centrifuge.RPCEvent{Method: "hold"}, func(_ centrifuge.RPCReply, err error) { lim <- err })
	if err := <-lim; err != centrifuge.ErrorLimitExceeded {
		t.Fatalf("want limit exceeded, got %v", err)
	}

	notFound := make(chan error, 1)
	r.serve(c, centrifuge.RPCEvent{Method: "nope"}, func(_ centrifuge.RPCReply, err error) { notFound <- err })
	if err := <-notFound; err != centrifuge.ErrorMethodNotFound {
		t.Fatalf("want not found, got %v", err)
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
