// Command example is a minimal, runnable demonstration of fuge: it builds an
// API, registers one documented RPC method, wires it into both Centrifuge
// and HTTP, and prints the generated AsyncAPI document.
//
// Run it with:
//
//	go run ./example
package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/centrifugal/centrifuge"

	"github.com/julaw-energy/fuge"
)

type CreateCountryRequest struct {
	Name string `json:"name" required:"true" doc:"Country name"`
	ISO  string `json:"iso"  required:"true" doc:"ISO 3166-1 alpha-2 code"`
}

func (r CreateCountryRequest) Validate() error {
	if len(r.ISO) != 2 {
		return fmt.Errorf("iso must be exactly 2 letters, got %q", r.ISO)
	}
	return nil
}

type Country struct {
	ID   int    `json:"id"   required:"true"`
	Name string `json:"name" required:"true"`
}

func createCountry(_ context.Context, req *CreateCountryRequest) (*Country, error) {
	return &Country{ID: 1, Name: req.Name}, nil
}

// loggingMiddleware is an OpMiddleware: it runs around every Register'd
// operation and can read the Operation being called.
func loggingMiddleware(op *fuge.Operation, next fuge.OpHandler) fuge.OpHandler {
	return func(ctx context.Context, data []byte) ([]byte, error) {
		fmt.Printf("-> %s (%s)\n", op.Method, op.OperationID)
		return next(ctx, data)
	}
}

// newAPI builds and returns the whole API: this is the part a real
// application would call once, at startup.
func newAPI() *fuge.API {
	cfg := fuge.DefaultConfig("Platform RPC", "1.0.0")
	cfg.Info.Description = "RPC API for Platform"
	cfg.Servers = map[string]fuge.Server{
		"production": {Host: "rpc.example.com", Protocol: "wss", Pathname: "/connection/websocket"},
	}
	cfg.SecuritySchemes["bearer"] = fuge.SecurityScheme{Type: "http", Scheme: "bearer", BearerFormat: "JWT"}

	api := fuge.New(cfg)
	api.UseMiddleware(loggingMiddleware)

	countries := api.Group("country", "Country")
	fuge.Register(countries, fuge.Operation{
		OperationID: "create-country",
		Method:      "create",
		Summary:     "Create a new country",
		Security:    []map[string][]string{{"bearer": {}}},
		Extensions:  map[string]any{"x-permissions": []string{"location.create"}},
	}, createCountry)

	return api
}

// serveDoc mounts the generated AsyncAPI document at /asyncapi.json. In a
// real server this *http.ServeMux is what you pass to http.ListenAndServe.
func serveDoc(api *fuge.API) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/asyncapi.json", api.JsonDocHandler())
	return mux
}

// attachToCentrifuge is called from node.OnConnect in a real server:
//
//	node.OnConnect(func(c *centrifuge.Client) { attachToCentrifuge(node, api, c) })
//
// It is defined here, unused by main, only because this example has no
// running Centrifuge node to connect it to.
func attachToCentrifuge(node *centrifuge.Node, api *fuge.API, c *centrifuge.Client) {
	api.Attach(node, c)
}

var _ = attachToCentrifuge // referenced so the pattern above type-checks

type Notification struct {
	Text string `json:"text" required:"true"`
}

// registerNotifications shows the channel side: a real Centrifuge channel,
// parameterized per user, that only that user may subscribe to.
func registerNotifications(api *fuge.API) *fuge.ChannelHandle[Notification] {
	ch := fuge.RegisterChannel[Notification](api, fuge.Channel{
		ID:      "user-notifications",
		Pattern: "user:{userId}:notifications",
		Summary: "Per-user notification stream",
	})
	ch.OnSubscribe(func(ctx context.Context, params fuge.Params, info fuge.SubscribeInfo) error {
		if params["userId"] != info.UserID {
			return fmt.Errorf("not your notification stream")
		}
		return nil
	})
	return ch
}

func main() {
	api := newAPI()
	_ = registerNotifications(api) // documents the channel; Publish(node, ...) is called from
	// application code wherever a notification is triggered — this example
	// has no live Node to demonstrate that part against.
	_ = serveDoc(api) // mux ready to pass to http.ListenAndServe in a real server
	fmt.Println("AsyncAPI document ready to serve at /asyncapi.json")
	fmt.Println()

	// Calling a Register'd operation in-process (what a test, or a health
	// check, would do — a real client calls it over the WebSocket instead).
	out, err := api.Call(context.Background(), "country.create", []byte(`{"name":"Uganda","iso":"UG"}`))
	if err != nil {
		panic(err)
	}
	fmt.Printf("country.create -> %s\n\n", out)

	doc, err := api.JSON()
	if err != nil {
		panic(err)
	}
	fmt.Println(string(doc))
}
