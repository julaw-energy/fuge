package fuge

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	"github.com/swaggest/jsonschema-go"
)

type reflectConfig struct {
	requireAll bool
}

// ReflectOption configures [SchemaFromType].
type ReflectOption func(*reflectConfig)

// RequireAll marks every property of every reflected definition as required.
// Correct only for payloads whose Go structs never use omitempty — Go then
// marshals every field on every message.
func RequireAll() ReflectOption {
	return func(cfg *reflectConfig) {
		cfg.requireAll = true
	}
}

// SchemaFromType reflects v into a JSON Schema, returning the payload schema
// for the value itself — a "$ref" into "#/components/schemas/" — and the
// definitions it references, ready to assign to [Components].Schemas.
//
// Property names come from json struct tags. Schema keywords are read from
// the tags jsonschema-go understands, including required:"true", so the wire
// contract is declared on the struct rather than restated here.
func SchemaFromType(v any, opts ...ReflectOption) (json.RawMessage, map[string]json.RawMessage, error) {
	cfg := reflectConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}

	defs := map[string]json.RawMessage{}

	var collectErr error

	r := jsonschema.Reflector{}

	schema, err := r.Reflect(v,
		jsonschema.RootRef,
		jsonschema.DefinitionsPrefix("#/components/schemas/"),
		envelopNullability,
		jsonschema.InterceptNullability(envelopRefNullability),
		jsonschema.InterceptDefName(func(t reflect.Type, defaultDefName string) string {
			if t.Name() != "" {
				return t.Name()
			}

			return defaultDefName
		}),

		jsonschema.InterceptProp(
			func(params jsonschema.InterceptPropParams) error {
				if !params.Processed {
					return nil
				}

				if description := params.Field.Tag.Get("doc"); description != "" {
					params.PropertySchema.WithDescription(description)
				}

				return nil
			},
		),

		jsonschema.CollectDefinitions(func(name string, s jsonschema.Schema) {
			b, err := s.JSONSchemaBytes()
			if err != nil {
				if collectErr == nil {
					collectErr = err
				}

				return
			}

			defs[name] = b
		}),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("reflecting %T: %w", v, err)
	}

	if collectErr != nil {
		return nil, nil, collectErr
	}

	payload, err := schema.JSONSchemaBytes()
	if err != nil {
		return nil, nil, fmt.Errorf("marshaling root schema for %T: %w", v, err)
	}

	if cfg.requireAll {
		for name, def := range defs {
			required, err := requireAllProperties(def)
			if err != nil {
				return nil, nil, fmt.Errorf("requiring properties of %s: %w", name, err)
			}

			defs[name] = required
		}
	}

	return payload, defs, nil
}

func requireAllProperties(def json.RawMessage) (json.RawMessage, error) {
	var schema map[string]json.RawMessage

	err := json.Unmarshal(def, &schema)
	if err != nil {
		return nil, fmt.Errorf("unmarshaling definition: %w", err)
	}

	rawProps, ok := schema["properties"]
	if !ok {
		return def, nil
	}

	var props map[string]json.RawMessage

	err = json.Unmarshal(rawProps, &props)
	if err != nil {
		return nil, fmt.Errorf("unmarshaling properties: %w", err)
	}

	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}

	sort.Strings(names)

	required, err := json.Marshal(names)
	if err != nil {
		return nil, fmt.Errorf("marshaling required list: %w", err)
	}

	schema["required"] = required

	out, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshaling definition: %w", err)
	}

	return out, nil
}

// Nullability belongs to the field, not the type: Go structs are shared across
// fields that differ in nullability, so injecting null into the referenced
// definition would state it for every use. Both hooks keep null at the use
// site — envelopNullability for inferred cases, envelopRefNullability for an
// explicit nullable tag, whose branch in the reflector returns before the
// enveloping logic runs.
func envelopNullability(rc *jsonschema.ReflectContext) {
	rc.EnvelopNullability = true
}

func envelopRefNullability(params jsonschema.InterceptNullabilityParams) {
	if !params.NullAdded || params.Schema == nil || params.Schema.Ref == nil {
		return
	}

	ref := jsonschema.Schema{Ref: params.Schema.Ref}

	params.Schema.Ref = nil
	params.Schema.RemoveType(jsonschema.Null)
	params.Schema.Type = nil
	params.Schema.AnyOf = []jsonschema.SchemaOrBool{
		ref.ToSchemaOrBool(),
		jsonschema.Null.ToSchemaOrBool(),
	}
}
