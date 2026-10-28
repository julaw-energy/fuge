package fuge

import (
	"encoding/json"
	"reflect"
	"testing"
)

func Test_New_Document_Marshals_Minimal(t *testing.T) {
	doc := NewDocument("Test API", "1.0.0")

	got, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	want := `{"asyncapi":"3.1.0","info":{"title":"Test API","version":"1.0.0"}}`
	if string(got) != want {
		t.Fatalf("got:  %s\nwant: %s", got, want)
	}
}

func Test_Document_Marshals_Channel_Operation_Components(t *testing.T) {
	doc := NewDocument("Widget", "1.0.0")
	doc.Info.Description = "Public widget feed"
	doc.DefaultContentType = "application/json"
	doc.Servers = map[string]Server{
		"production": {Host: "co2.example.com", Protocol: "wss"},
	}
	doc.Channels = map[string]SpecChannel{
		"portTotals": {
			Address:     "/unlocodes/{unlocode}",
			Description: "Broadcasts totals every 5 seconds",
			Parameters: map[string]Parameter{
				"unlocode": {Description: "Port UNLOCODE or the literal \"all\""},
			},
			Messages: map[string]Message{
				"portTotals": {Ref: "#/components/messages/portTotals"},
			},
		},
	}
	doc.Operations = map[string]SpecOperation{
		"receivePortTotals": {
			Action:   ActionReceive,
			Channel:  Ref("#/channels/portTotals"),
			Messages: []RefObj{Ref("#/channels/portTotals/messages/portTotals")},
		},
	}
	doc.Components = &Components{
		Messages: map[string]Message{
			"portTotals": {
				Name:    "PortTotals",
				Payload: json.RawMessage(`{"$ref":"#/components/schemas/PortTotals"}`),
			},
		},
		Schemas: map[string]json.RawMessage{
			"PortTotals": json.RawMessage(`{"type":"object"}`),
		},
	}

	got, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	want := `{
		"asyncapi": "3.1.0",
		"info": {"title": "Widget", "version": "1.0.0", "description": "Public widget feed"},
		"defaultContentType": "application/json",
		"servers": {"production": {"host": "co2.example.com", "protocol": "wss"}},
		"channels": {
			"portTotals": {
				"address": "/unlocodes/{unlocode}",
				"description": "Broadcasts totals every 5 seconds",
				"parameters": {"unlocode": {"description": "Port UNLOCODE or the literal \"all\""}},
				"messages": {"portTotals": {"$ref": "#/components/messages/portTotals"}}
			}
		},
		"operations": {
			"receivePortTotals": {
				"action": "receive",
				"channel": {"$ref": "#/channels/portTotals"},
				"messages": [{"$ref": "#/channels/portTotals/messages/portTotals"}]
			}
		},
		"components": {
			"schemas": {"PortTotals": {"type": "object"}},
			"messages": {"portTotals": {"name": "PortTotals", "payload": {"$ref": "#/components/schemas/PortTotals"}}}
		}
	}`
	assertJSONEqual(t, got, []byte(want))
}

func assertJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()

	var gotAny, wantAny any
	if err := json.Unmarshal(got, &gotAny); err != nil {
		t.Fatalf("got is not valid JSON: %v\n%s", err, got)
	}

	if err := json.Unmarshal(want, &wantAny); err != nil {
		t.Fatalf("want is not valid JSON: %v\n%s", err, want)
	}

	if !reflect.DeepEqual(gotAny, wantAny) {
		gotN, err := json.MarshalIndent(gotAny, "", "  ")
		if err != nil {
			t.Fatal(err)
		}

		wantN, err := json.MarshalIndent(wantAny, "", "  ")
		if err != nil {
			t.Fatal(err)
		}

		t.Fatalf("documents differ\ngot:\n%s\nwant:\n%s", gotN, wantN)
	}
}
