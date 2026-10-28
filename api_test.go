package fuge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/centrifugal/centrifuge"
)

type CreateCountryRequest struct {
	Name string `json:"name" required:"true" doc:"Country name"`
	ISO  string `json:"iso"  required:"true"`
}

func (r CreateCountryRequest) Validate() error {
	if len(r.ISO) != 2 {
		return errors.New("iso must be 2 letters")
	}
	return nil
}

type Country struct {
	ID   int    `json:"id"   required:"true"`
	Name string `json:"name" required:"true"`
}

func createCountry(_ context.Context, r *CreateCountryRequest) (*Country, error) {
	return &Country{ID: 1, Name: r.Name}, nil
}

func newAPI(t *testing.T) *API {
	t.Helper()
	cfg := DefaultConfig("Test RPC", "1.0.0")
	cfg.SecuritySchemes["bearer"] = SecurityScheme{Type: "http", Scheme: "bearer", BearerFormat: "JWT"}
	return New(cfg)
}

func mustPanic(t *testing.T, contains string, f func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic containing %q", contains)
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("expected a string panic, got %T: %v", r, r)
		}
		if contains != "" && !strings.Contains(msg, contains) {
			t.Fatalf("panic %q does not contain %q", msg, contains)
		}
	}()
	f()
}

func TestHandlerBehaviour(t *testing.T) {
	api := newAPI(t)
	Register(api, Operation{Method: "country.create"}, createCountry)

	out, err := api.Call(context.Background(), "country.create", []byte(`{"name":"Uganda","iso":"UG"}`))
	if err != nil || string(out) != `{"id":1,"name":"Uganda"}` {
		t.Fatalf("got %s, %v", out, err)
	}

	_, err = api.Call(context.Background(), "country.create", []byte(`{"name":"X","iso":"U"}`))
	var ce *centrifuge.Error
	if !errors.As(err, &ce) || ce.Code != CodeValidation || ce.Message != "iso must be 2 letters" {
		t.Fatalf("want validation error, got %v", err)
	}

	_, err = api.Call(context.Background(), "country.create", []byte(`{nope`))
	if !errors.Is(err, centrifuge.ErrorBadRequest) {
		t.Fatalf("want bad request, got %v", err)
	}
}

func TestNilResponseAndEmptyRequest(t *testing.T) {
	api := newAPI(t)
	Register(api, Operation{Method: "health"},
		func(context.Context, *struct{}) (*struct{}, error) { return nil, nil })
	out, err := api.Call(context.Background(), "health", nil)
	if err != nil || out != nil {
		t.Fatalf("got %v, %v", out, err)
	}
}

func TestMiddlewareSeesOperation(t *testing.T) {
	api := newAPI(t)
	var trace []string
	api.UseMiddleware(
		func(op *Operation, next OpHandler) OpHandler {
			return func(ctx context.Context, d []byte) ([]byte, error) {
				trace = append(trace, "a:"+op.OperationID)
				return next(ctx, d)
			}
		},
		func(op *Operation, next OpHandler) OpHandler {
			return func(ctx context.Context, d []byte) ([]byte, error) {
				got, ok := OperationFrom(ctx)
				if !ok || got.Method != op.Method || len(op.Security) != 1 {
					t.Errorf("operation not available in middleware")
				}
				trace = append(trace, "b")
				return next(ctx, d)
			}
		},
	)
	Register(api, Operation{
		OperationID: "create-country", Method: "country.create",
		Security: []map[string][]string{{"bearer": {}}},
	}, createCountry)

	if _, err := api.Call(context.Background(), "country.create", []byte(`{"name":"U","iso":"UG"}`)); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(trace, ","); got != "a:create-country,b" {
		t.Fatalf("order: %s", got)
	}
	mustPanic(t, "before registering", func() { api.UseMiddleware() })
}

// TestGroupsPrefixDocsAndWire checks the same Group-nested registration from
// three angles at once: it is reachable on the real router under the fully
// prefixed wire name, its Timeout reaches the router as a per-route option,
// and it is documented — with inherited tags — under that same name.
func TestGroupsPrefixDocsAndWire(t *testing.T) {
	api := newAPI(t)
	billing := api.Group("billing", "Billing")
	invoices := billing.Group("invoice", "Invoices")
	Register(invoices, Operation{Method: "create", Tags: []string{"Write"}, Timeout: time.Second}, createCountry)

	out, err := api.Call(context.Background(), "billing.invoice.create", []byte(`{"name":"U","iso":"UG"}`))
	if err != nil || string(out) != `{"id":1,"name":"U"}` {
		t.Fatalf("not reachable under its full name: %s, %v", out, err)
	}

	op, ok := api.Document().Operations["billing.invoice.create"]
	if !ok {
		t.Fatalf("document does not have the prefixed operation: %v", api.Document().Operations)
	}
	var tagNames []string
	for _, tag := range op.Tags {
		tagNames = append(tagNames, tag.Name)
	}
	if strings.Join(tagNames, ",") != "Billing,Invoices,Write" {
		t.Fatalf("tags: %v", tagNames)
	}
	if _, ok := api.Document().Channels["rpc.billing.invoice.create"]; !ok {
		t.Fatal("document does not show the prefixed method")
	}
}

func TestRegistrationPanics(t *testing.T) {
	api := newAPI(t)
	Register(api, Operation{OperationID: "a", Method: "m.one"}, createCountry)
	mustPanic(t, "already registered", func() {
		Register(api, Operation{OperationID: "b", Method: "m.one"}, createCountry)
	})
	mustPanic(t, "duplicate operation id", func() {
		Register(api, Operation{OperationID: "a", Method: "m.two"}, createCountry)
	})
	mustPanic(t, "unknown security scheme", func() {
		Register(api, Operation{Method: "m.three", Security: []map[string][]string{{"nope": {}}}}, createCountry)
	})
	mustPanic(t, "AND", func() {
		Register(api, Operation{Method: "m.four", Security: []map[string][]string{{"bearer": {}, "x": {}}}}, createCountry)
	})
	mustPanic(t, "must start with", func() {
		Register(api, Operation{Method: "m.five", Extensions: map[string]any{"permissions": 1}}, createCountry)
	})
	mustPanic(t, "invalid method", func() {
		Register(api, Operation{Method: "bad name"}, createCountry)
	})
}

// compact removes indentation so assertions do not depend on formatting.
func compact(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestGeneratedDocument(t *testing.T) {
	api := newAPI(t)
	Register(api, Operation{
		OperationID: "create-country",
		Method:      "country.create",
		Summary:     "Create a new country",
		Tags:        []string{"Country"},
		Security:    []map[string][]string{{"bearer": {}}},
		Errors:      []uint32{103},
		Extensions:  map[string]any{"x-permissions": []string{"location.create"}},
	}, createCountry)
	Register(api, Operation{OperationID: "health-check", Method: "health"},
		func(context.Context, *struct{}) (*struct{}, error) { return nil, nil })

	out, err := api.JSON() // also validates against the AsyncAPI meta-schema
	if err != nil {
		t.Fatal(err)
	}
	again, _ := api.JSON()
	if string(out) != string(again) {
		t.Fatal("output is not deterministic")
	}

	var doc struct {
		Operations map[string]map[string]json.RawMessage `json:"operations"`
		Channels   map[string]struct {
			Address  string                     `json:"address"`
			Messages map[string]json.RawMessage `json:"messages"`
		} `json:"channels"`
		Components struct {
			Schemas         map[string]json.RawMessage `json:"schemas"`
			Messages        map[string]json.RawMessage `json:"messages"`
			SecuritySchemes map[string]map[string]any  `json:"securitySchemes"`
		} `json:"components"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}

	op := doc.Operations["create-country"]
	for _, k := range []string{"action", "channel", "messages", "reply", "security", "x-permissions"} {
		if _, ok := op[k]; !ok {
			t.Errorf("operation is missing %q: %s", k, out)
		}
	}
	if got := compact(t, op["security"]); got != `[{"$ref":"#/components/securitySchemes/bearer"}]` {
		t.Errorf("security: %s", got)
	}
	if doc.Channels["rpc.country.create"].Address != "country.create" {
		t.Errorf("channel address: %+v", doc.Channels)
	}
	if doc.Components.SecuritySchemes["bearer"]["scheme"] != "bearer" {
		t.Errorf("security schemes: %v", doc.Components.SecuritySchemes)
	}
	for _, name := range []string{"CreateCountryRequest", "Country"} {
		if _, ok := doc.Components.Schemas[name]; !ok {
			t.Errorf("schema %s missing: %v", name, doc.Components.Schemas)
		}
	}

	// Error codes: internal(100), bad request(107), validation(422) auto-added + declared 103.
	if got := compact(t, doc.Components.Messages["create-country.error"]); !strings.Contains(got, `"enum":[100,103,107,422]`) {
		t.Errorf("error codes: %s", got)
	}
	// The empty-payload operation has only the internal error code and no payloads.
	if got := compact(t, doc.Components.Messages["health-check.error"]); !strings.Contains(got, `"enum":[100]`) {
		t.Errorf("health error codes: %s", got)
	}
	var reqMsg map[string]json.RawMessage
	if err := json.Unmarshal(doc.Components.Messages["health-check.request"], &reqMsg); err != nil {
		t.Fatal(err)
	}
	if _, has := reqMsg["payload"]; has {
		t.Errorf("empty request should have no payload key: %v", reqMsg)
	}
	if got := compact(t, doc.Components.Messages["create-country.request"]); !strings.Contains(got, `"payload":{"$ref":"#/components/schemas/CreateCountryRequest"}`) {
		t.Errorf("request payload should reference its schema: %s", got)
	}
}
