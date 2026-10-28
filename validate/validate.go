// Package validate checks a generated AsyncAPI 3.1.0 document against the
// official meta-schema. It lives apart from package fuge so that applications
// which don't validate don't pay for the embedded meta-schema (about 400 KB)
// or the JSON Schema compiler. Opt in with fuge.Config.Validator:
//
//	cfg := fuge.DefaultConfig("Platform RPC", "1.0.0")
//	cfg.Validator = validate.Bytes
//
// Validation never reaches the network.
package validate

import (
	"bytes"
	_ "embed"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Version is the AsyncAPI specification version the embedded meta-schema
// describes; it must match fuge.Version.
const Version = "3.1.0"

//go:embed asyncapi-3.1.0.json
var metaSchemaJSON []byte

const metaSchemaURL = "http://asyncapi.com/definitions/3.1.0/asyncapi.json"

var (
	once   sync.Once
	schema *jsonschema.Schema
	err    error
)

func load() (*jsonschema.Schema, error) {
	once.Do(func() {
		doc, e := jsonschema.UnmarshalJSON(bytes.NewReader(metaSchemaJSON))
		if e != nil {
			err = fmt.Errorf("parsing embedded meta-schema: %w", e)
			return
		}
		compiler := jsonschema.NewCompiler()
		if e = compiler.AddResource(metaSchemaURL, doc); e != nil {
			err = fmt.Errorf("registering embedded meta-schema: %w", e)
			return
		}
		if schema, e = compiler.Compile(metaSchemaURL); e != nil {
			err = fmt.Errorf("compiling embedded meta-schema: %w", e)
		}
	})
	return schema, err
}

// Bytes checks a marshalled AsyncAPI document against the embedded
// meta-schema. A document that declares a different "asyncapi" version, or
// omits the field, fails. Its signature matches fuge.Config.Validator.
func Bytes(document []byte) error {
	s, err := load()
	if err != nil {
		return err
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(document))
	if err != nil {
		return fmt.Errorf("parsing document: %w", err)
	}
	if err := s.Validate(value); err != nil {
		return fmt.Errorf("document does not conform to AsyncAPI %s: %w", Version, err)
	}
	return nil
}
