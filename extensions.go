package fuge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Extensions holds "x-" specification extensions: implementation-defined
// values the specification permits on almost every object, alongside that
// object's own fields (e.g. a channel, an operation, a security scheme).
//
// Keys must start with "x-". MarshalJSON reports an error otherwise, since a
// malformed key would silently produce a document that fails meta-schema validation (fuge/validate).
type Extensions map[string]any

// marshalExt marshals base (a plain value with no MarshalJSON method of its
// own — pass a local "type alias T" conversion to avoid recursing back into
// the caller's MarshalJSON) and appends ext's entries, sorted by key, as
// additional top-level properties. Field order coming from base is otherwise
// preserved: when ext is empty, the returned bytes equal json.Marshal(base)
// exactly, so a type with no extensions set marshals exactly as before this
// field existed.
func marshalExt(base any, ext Extensions) ([]byte, error) {
	raw, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}
	if len(ext) == 0 {
		return raw, nil
	}

	keys := make([]string, 0, len(ext))
	for k := range ext {
		if !strings.HasPrefix(k, "x-") {
			return nil, fmt.Errorf("asyncapi3: extension key %q must start with \"x-\"", k)
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	if bytes.Equal(raw, []byte("{}")) {
		buf.WriteByte('{')
	} else {
		buf.Write(raw[:len(raw)-1]) // everything but the closing brace
		buf.WriteByte(',')
	}
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		vb, err := json.Marshal(ext[k])
		if err != nil {
			return nil, fmt.Errorf("asyncapi3: marshaling extension %q: %w", k, err)
		}
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(vb)
	}
	buf.WriteByte('}')

	return buf.Bytes(), nil
}

// unmarshalExt unmarshals data into dst (a pointer to a plain struct with no
// UnmarshalJSON method of its own — pass &a where a is a local "type alias T"
// conversion), then collects any top-level "x-" keys into *ext.
func unmarshalExt(data []byte, dst any, ext *Extensions) error {
	if err := json.Unmarshal(data, dst); err != nil {
		return err
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for k, v := range raw {
		if !strings.HasPrefix(k, "x-") {
			continue
		}
		var val any
		if err := json.Unmarshal(v, &val); err != nil {
			return fmt.Errorf("asyncapi3: unmarshaling extension %q: %w", k, err)
		}
		if *ext == nil {
			*ext = Extensions{}
		}
		(*ext)[k] = val
	}

	return nil
}

func (d Document) MarshalJSON() ([]byte, error) {
	type alias Document

	return marshalExt(alias(d), d.Extensions)
}

func (d *Document) UnmarshalJSON(data []byte) error {
	type alias Document

	var a alias
	if err := unmarshalExt(data, &a, &a.Extensions); err != nil {
		return err
	}

	*d = Document(a)

	return nil
}

func (i Info) MarshalJSON() ([]byte, error) {
	type alias Info

	return marshalExt(alias(i), i.Extensions)
}

func (i *Info) UnmarshalJSON(data []byte) error {
	type alias Info

	var a alias
	if err := unmarshalExt(data, &a, &a.Extensions); err != nil {
		return err
	}

	*i = Info(a)

	return nil
}

func (s Server) MarshalJSON() ([]byte, error) {
	type alias Server

	return marshalExt(alias(s), s.Extensions)
}

func (s *Server) UnmarshalJSON(data []byte) error {
	type alias Server

	var a alias
	if err := unmarshalExt(data, &a, &a.Extensions); err != nil {
		return err
	}

	*s = Server(a)

	return nil
}

func (c SpecChannel) MarshalJSON() ([]byte, error) {
	type alias SpecChannel

	return marshalExt(alias(c), c.Extensions)
}

func (c *SpecChannel) UnmarshalJSON(data []byte) error {
	type alias SpecChannel

	var a alias
	if err := unmarshalExt(data, &a, &a.Extensions); err != nil {
		return err
	}

	*c = SpecChannel(a)

	return nil
}

func (p Parameter) MarshalJSON() ([]byte, error) {
	type alias Parameter

	return marshalExt(alias(p), p.Extensions)
}

func (p *Parameter) UnmarshalJSON(data []byte) error {
	type alias Parameter

	var a alias
	if err := unmarshalExt(data, &a, &a.Extensions); err != nil {
		return err
	}

	*p = Parameter(a)

	return nil
}

func (m Message) MarshalJSON() ([]byte, error) {
	type alias Message

	return marshalExt(alias(m), m.Extensions)
}

func (m *Message) UnmarshalJSON(data []byte) error {
	type alias Message

	var a alias
	if err := unmarshalExt(data, &a, &a.Extensions); err != nil {
		return err
	}

	*m = Message(a)

	return nil
}

func (o SpecOperation) MarshalJSON() ([]byte, error) {
	type alias SpecOperation

	return marshalExt(alias(o), o.Extensions)
}

func (o *SpecOperation) UnmarshalJSON(data []byte) error {
	type alias SpecOperation

	var a alias
	if err := unmarshalExt(data, &a, &a.Extensions); err != nil {
		return err
	}

	*o = SpecOperation(a)

	return nil
}

func (r OperationReply) MarshalJSON() ([]byte, error) {
	type alias OperationReply

	return marshalExt(alias(r), r.Extensions)
}

func (r *OperationReply) UnmarshalJSON(data []byte) error {
	type alias OperationReply

	var a alias
	if err := unmarshalExt(data, &a, &a.Extensions); err != nil {
		return err
	}

	*r = OperationReply(a)

	return nil
}

func (t Tag) MarshalJSON() ([]byte, error) {
	type alias Tag

	return marshalExt(alias(t), t.Extensions)
}

func (t *Tag) UnmarshalJSON(data []byte) error {
	type alias Tag

	var a alias
	if err := unmarshalExt(data, &a, &a.Extensions); err != nil {
		return err
	}

	*t = Tag(a)

	return nil
}

func (c Components) MarshalJSON() ([]byte, error) {
	type alias Components

	return marshalExt(alias(c), c.Extensions)
}

func (c *Components) UnmarshalJSON(data []byte) error {
	type alias Components

	var a alias
	if err := unmarshalExt(data, &a, &a.Extensions); err != nil {
		return err
	}

	*c = Components(a)

	return nil
}

func (s SecurityScheme) MarshalJSON() ([]byte, error) {
	type alias SecurityScheme

	return marshalExt(alias(s), s.Extensions)
}

func (s *SecurityScheme) UnmarshalJSON(data []byte) error {
	type alias SecurityScheme

	var a alias
	if err := unmarshalExt(data, &a, &a.Extensions); err != nil {
		return err
	}

	*s = SecurityScheme(a)

	return nil
}
