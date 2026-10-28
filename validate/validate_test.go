package validate_test

import (
	"encoding/json"
	"github.com/julaw-energy/fuge"
	"github.com/julaw-energy/fuge/validate"
	"strings"
	"testing"
)

func Test_Validate_Accepts_A_Minimal_Document(t *testing.T) {
	doc := fuge.NewDocument("Test API", "1.0.0")

	if err := validateDoc(doc); err != nil {
		t.Fatalf("minimal document must validate, got %v", err)
	}
}

func Test_Validate_Accepts_A_Document_With_Channels_And_Operations(t *testing.T) {
	payload, schemas, err := fuge.SchemaFromType(samplePayload{})
	if err != nil {
		t.Fatal(err)
	}

	doc := fuge.NewDocument("Widget", "1.0.0")
	doc.DefaultContentType = "application/json"
	doc.Servers = map[string]fuge.Server{
		"production": {Host: "example.com", Protocol: "wss"},
	}
	doc.Channels = map[string]fuge.SpecChannel{
		"sample": {
			Address:  "/samples/{id}",
			Messages: map[string]fuge.Message{"sample": {Ref: "#/components/messages/sample"}},
			Parameters: map[string]fuge.Parameter{
				"id": {Description: "Sample id."},
			},
		},
	}
	doc.Operations = map[string]fuge.SpecOperation{
		"receiveSample": {
			Action:   fuge.ActionReceive,
			Channel:  fuge.Ref("#/channels/sample"),
			Messages: []fuge.RefObj{fuge.Ref("#/channels/sample/messages/sample")},
		},
	}
	doc.Components = &fuge.Components{
		Schemas:  schemas,
		Messages: map[string]fuge.Message{"sample": {Name: "Sample", Payload: payload}},
	}

	if err := validateDoc(doc); err != nil {
		t.Fatalf("document must validate, got %v", err)
	}
}

func Test_ValidateBytes_Rejects_A_Wrong_Spec_Version(t *testing.T) {
	err := validate.Bytes([]byte(`{"asyncapi":"2.6.0","info":{"title":"x","version":"1"}}`))
	if err == nil {
		t.Fatal("want error for an unsupported spec version, got nil")
	}
}

func Test_ValidateBytes_Rejects_A_Missing_Info_Title(t *testing.T) {
	err := validate.Bytes([]byte(`{"asyncapi":"3.1.0","info":{"version":"1"}}`))
	if err == nil {
		t.Fatal("want error for a document without info.title, got nil")
	}

	if !strings.Contains(err.Error(), "title") {
		t.Fatalf("error must name the offending property, got %v", err)
	}
}

func Test_ValidateBytes_Rejects_An_Invalid_Operation_Action(t *testing.T) {
	doc := map[string]any{
		"asyncapi": "3.1.0",
		"info":     map[string]any{"title": "x", "version": "1"},
		"channels": map[string]any{"c": map[string]any{"address": "/c"}},
		"operations": map[string]any{
			"o": map[string]any{"action": "broadcast", "channel": map[string]any{"$ref": "#/channels/c"}},
		},
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	if err := validate.Bytes(raw); err == nil {
		t.Fatal("want error for an operation action outside send/receive, got nil")
	}
}

func Test_ValidateBytes_Rejects_Malformed_JSON(t *testing.T) {
	if err := validate.Bytes([]byte(`{"asyncapi":`)); err == nil {
		t.Fatal("want error for malformed JSON, got nil")
	}
}

func Test_ValidateBytes_Rejects_A_Missing_Version_Field(t *testing.T) {
	err := validate.Bytes([]byte(`{"info":{"title":"x","version":"1"}}`))
	if err == nil {
		t.Fatal("want error for a document with no \"asyncapi\" field, got nil")
	}
}

type samplePayload struct {
	Name  string `json:"Name"`
	Total int    `json:"Total"`
}

func validateDoc(doc *fuge.Document) error {
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return validate.Bytes(raw)
}
