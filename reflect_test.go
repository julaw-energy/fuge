package fuge

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

type SamplePayload struct {
	Name      string      `json:"Name"`
	Total     SampleTotal `json:"Total"`
	Timestamp time.Time   `json:"Timestamp"`
}

type SampleTotal struct {
	YTD          int     `json:"YTD"`
	PreviousYear float64 `json:"PreviousYear"`
}

func Test_SchemaFromType_Returns_RootRef_And_Definitions(t *testing.T) {
	payload, defs, err := SchemaFromType(SamplePayload{})
	if err != nil {
		t.Fatal(err)
	}

	assertJSONEqual(t, payload, []byte(`{"$ref":"#/components/schemas/SamplePayload"}`))

	root, ok := defs["SamplePayload"]
	if !ok {
		t.Fatalf("missing SamplePayload definition, got keys: %v", keys(defs))
	}

	assertJSONEqual(t, root, []byte(`{
		"type": "object",
		"properties": {
			"Name": {"type": "string"},
			"Total": {"$ref": "#/components/schemas/SampleTotal"},
			"Timestamp": {"type": "string", "format": "date-time"}
		}
	}`))

	nested, ok := defs["SampleTotal"]
	if !ok {
		t.Fatalf("missing SampleTotal definition, got keys: %v", keys(defs))
	}

	assertJSONEqual(t, nested, []byte(`{
		"type": "object",
		"properties": {
			"YTD": {"type": "integer"},
			"PreviousYear": {"type": "number"}
		}
	}`))
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}

func Test_SchemaFromType_RequireAll_Marks_Every_Property_Required(t *testing.T) {
	_, defs, err := SchemaFromType(SamplePayload{}, RequireAll())
	if err != nil {
		t.Fatal(err)
	}

	for defName, want := range map[string][]string{
		"SamplePayload": {"Name", "Timestamp", "Total"},
		"SampleTotal":   {"PreviousYear", "YTD"},
	} {
		var schema struct {
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(defs[defName], &schema); err != nil {
			t.Fatal(err)
		}

		sort.Strings(schema.Required)

		if !reflect.DeepEqual(schema.Required, want) {
			t.Fatalf("%s: want required %v, got %v", defName, want, schema.Required)
		}
	}
}

type SharedRef struct {
	Label string `json:"label"`
}

type NullabilityHost struct {
	Required SharedRef  `json:"required"`
	Optional *SharedRef `json:"optional" nullable:"true"`
}

func Test_SchemaFromType_Keeps_Nullability_At_The_Use_Site(t *testing.T) {
	_, defs, err := SchemaFromType(NullabilityHost{})
	if err != nil {
		t.Fatal(err)
	}

	var shared struct {
		Type json.RawMessage `json:"type"`
	}
	if err := json.Unmarshal(defs["SharedRef"], &shared); err != nil {
		t.Fatal(err)
	}

	if string(shared.Type) != `"object"` {
		t.Fatalf("the shared definition must not carry nullability, got type %s", shared.Type)
	}

	var host struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(defs["NullabilityHost"], &host); err != nil {
		t.Fatal(err)
	}

	if got := string(host.Properties["required"]); got != `{"$ref":"#/components/schemas/SharedRef"}` {
		t.Fatalf("non-nullable field must be a bare $ref, got %s", got)
	}

	optional := string(host.Properties["optional"])
	if !strings.Contains(optional, `"anyOf"`) || !strings.Contains(optional, `"null"`) {
		t.Fatalf("nullable field must express null at the use site, got %s", optional)
	}
}

type NullabilityHostReversed struct {
	Optional *SharedRef `json:"optional" nullable:"true"`
	Required SharedRef  `json:"required"`
}

func Test_SchemaFromType_Nullability_Is_Order_Independent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sample any
	}{
		{"non-nullable field first", NullabilityHost{}},
		{"nullable field first", NullabilityHostReversed{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, defs, err := SchemaFromType(tc.sample)
			if err != nil {
				t.Fatal(err)
			}

			if got := string(defs["SharedRef"]); strings.Contains(got, `"null"`) {
				t.Fatalf("the shared definition must never carry null, got %s", got)
			}

			var host struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}

			for _, raw := range defs {
				var candidate struct {
					Properties map[string]json.RawMessage `json:"properties"`
				}

				if err := json.Unmarshal(raw, &candidate); err == nil {
					if _, ok := candidate.Properties["optional"]; ok {
						host = candidate
					}
				}
			}

			if got := string(host.Properties["required"]); strings.Contains(got, `"null"`) {
				t.Fatalf("non-nullable field must not accept null, got %s", got)
			}

			if got := string(host.Properties["optional"]); !strings.Contains(got, `"null"`) {
				t.Fatalf("nullable field must accept null, got %s", got)
			}
		})
	}
}
