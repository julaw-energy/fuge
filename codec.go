package fuge

import "encoding/json"

// Codec converts between wire bytes and Go values. Swap it to use msgpack,
// protobuf, etc. — pass it via Config.Codec.
type Codec interface {
	Unmarshal(data []byte, v any) error
	Marshal(v any) ([]byte, error)
}

type jsonCodec struct{}

func (jsonCodec) Unmarshal(d []byte, v any) error { return json.Unmarshal(d, v) }
func (jsonCodec) Marshal(v any) ([]byte, error)   { return json.Marshal(v) }

// JSON is the default codec. If you swap the codec, also set
// Config.DefaultContentType so the document stays truthful.
var JSON Codec = jsonCodec{}

// Validator is implemented by request types that can validate themselves.
// A returned error becomes a client-visible validation error (CodeValidation).
type Validator interface{ Validate() error }
