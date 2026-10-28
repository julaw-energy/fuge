// Package fuge Package asyncapi3 emits AsyncAPI 3.1.0 documents from Go code.
//
// Declare servers, channels, and operations as Go values, reflect payload
// schemas from the structs that cross the wire with [SchemaFromType], and
// marshal the [Document] to JSON or YAML.
//
// Marshalled output is deterministic — maps marshal key-sorted and struct
// field order is fixed — so regenerating an unchanged document is
// byte-identical. That property is what makes a committed document gateable
// in CI; see [Check].
//
// The document model is a curated subset of the specification. Spec objects
// and protocol bindings it does not model yet attach as raw JSON through the
// Bindings fields, so an unmodelled binding never blocks you.
package fuge

import "encoding/json"

// Version is the AsyncAPI specification version this package emits: 3.1.0,
// the only version [NewDocument] emits.
const Version = "3.1.0"

// SpecOperation actions, as defined by the specification: an application either
// sends a message to a channel or receives one from it. Both are expressed
// from the point of view of the application described by the document.
const (
	ActionSend    = "send"
	ActionReceive = "receive"
)

// Document is the root of an AsyncAPI document. Build one with [NewDocument] so the
// specification version is set correctly.
type Document struct {
	AsyncAPI           string                   `json:"asyncapi"`
	ID                 string                   `json:"id,omitempty"`
	Info               Info                     `json:"info"`
	DefaultContentType string                   `json:"defaultContentType,omitempty"`
	Servers            map[string]Server        `json:"servers,omitempty"`
	Channels           map[string]SpecChannel   `json:"channels,omitempty"`
	Operations         map[string]SpecOperation `json:"operations,omitempty"`
	Components         *Components              `json:"components,omitempty"`

	// Extensions holds "x-" values on the document root. See [Extensions].
	Extensions Extensions `json:"-"`
}

// NewDocument returns a Document for AsyncAPI 3.1.0 with its required info fields
// populated.
func NewDocument(title, version string) *Document {
	return &Document{
		AsyncAPI: Version,
		Info:     Info{Title: title, Version: version},
	}
}

// Info carries metadata about the API. Title and Version are required by the
// specification.
type Info struct {
	Title          string        `json:"title"`
	Version        string        `json:"version"`
	Description    string        `json:"description,omitempty"`
	TermsOfService string        `json:"termsOfService,omitempty"`
	Contact        *Contact      `json:"contact,omitempty"`
	License        *License      `json:"license,omitempty"`
	Tags           []Tag         `json:"tags,omitempty"`
	ExternalDocs   *ExternalDocs `json:"externalDocs,omitempty"`

	// Extensions holds "x-" values on this object. See [Extensions].
	Extensions Extensions `json:"-"`
}

// Contact identifies who to reach about the API.
type Contact struct {
	Name  string `json:"name,omitempty"`
	URL   string `json:"url,omitempty"`
	Email string `json:"email,omitempty"`
}

// License names the license the API is published under.
type License struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// Tag groups channels, operations, or messages under a common label.
type Tag struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`

	// Extensions holds "x-" values on this object. See [Extensions].
	Extensions Extensions `json:"-"`
}

// ExternalDocs points at documentation held outside the document.
type ExternalDocs struct {
	Description string `json:"description,omitempty"`
	URL         string `json:"url"`
}

// Server describes where the API is reachable. Host and Protocol are required
// by the specification; Bindings carries protocol-specific configuration as
// raw JSON.
type Server struct {
	Host        string          `json:"host"`
	Protocol    string          `json:"protocol"`
	Pathname    string          `json:"pathname,omitempty"`
	Description string          `json:"description,omitempty"`
	Title       string          `json:"title,omitempty"`
	Summary     string          `json:"summary,omitempty"`
	Tags        []Tag           `json:"tags,omitempty"`
	Bindings    json.RawMessage `json:"bindings,omitempty"`
	Security    []RefObj        `json:"security,omitempty"`

	// Extensions holds "x-" values on this object. See [Extensions].
	Extensions Extensions `json:"-"`
}

// SpecChannel is an addressable path messages flow through. An Address may embed
// parameters in braces — "/ports/{unlocode}" — each of which must have a
// matching entry in Parameters.
type SpecChannel struct {
	Address     string               `json:"address,omitempty"`
	Title       string               `json:"title,omitempty"`
	Summary     string               `json:"summary,omitempty"`
	Description string               `json:"description,omitempty"`
	Messages    map[string]Message   `json:"messages,omitempty"`
	Parameters  map[string]Parameter `json:"parameters,omitempty"`
	Servers     []RefObj             `json:"servers,omitempty"`
	Tags        []Tag                `json:"tags,omitempty"`
	Bindings    json.RawMessage      `json:"bindings,omitempty"`

	// Extensions holds "x-" values on this object. See [Extensions].
	Extensions Extensions `json:"-"`
}

// Parameter describes one substitution in a channel address.
type Parameter struct {
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Default     string   `json:"default,omitempty"`
	Examples    []string `json:"examples,omitempty"`
	Location    string   `json:"location,omitempty"`

	// Extensions holds "x-" values on this object. See [Extensions].
	Extensions Extensions `json:"-"`
}

// SpecOperation is an action the application performs on a channel: [ActionSend]
// or [ActionReceive]. Set Reply to describe a request/reply exchange.
type SpecOperation struct {
	Action      string          `json:"action,omitempty"`
	Channel     RefObj          `json:"channel,omitzero"`
	Title       string          `json:"title,omitempty"`
	Summary     string          `json:"summary,omitempty"`
	Description string          `json:"description,omitempty"`
	Messages    []RefObj        `json:"messages,omitempty"`
	Reply       *OperationReply `json:"reply,omitempty"`
	// Security lists acceptable security schemes as references into
	// Components.SecuritySchemes, e.g. Ref("#/components/securitySchemes/bearer").
	// Any one entry satisfies the operation (a logical OR); see [SecurityScheme].
	Security []RefObj        `json:"security,omitempty"`
	Tags     []Tag           `json:"tags,omitempty"`
	Bindings json.RawMessage `json:"bindings,omitempty"`

	// Extensions holds "x-" values on this object. See [Extensions].
	Extensions Extensions `json:"-"`
}

// OperationReply describes the response half of a request/reply exchange, and
// the channel it arrives on — which may be the request channel itself.
type OperationReply struct {
	Channel  RefObj   `json:"channel,omitzero"`
	Messages []RefObj `json:"messages,omitempty"`

	// Extensions holds "x-" values on this object. See [Extensions].
	Extensions Extensions `json:"-"`
}

// Message describes one kind of message on a channel. Set Ref alone to
// reference a message defined under [Components]; otherwise Payload carries
// the schema, typically produced by [SchemaFromType].
type Message struct {
	Ref         string          `json:"$ref,omitempty"`
	Name        string          `json:"name,omitempty"`
	Title       string          `json:"title,omitempty"`
	Summary     string          `json:"summary,omitempty"`
	Description string          `json:"description,omitempty"`
	ContentType string          `json:"contentType,omitempty"`
	Headers     json.RawMessage `json:"headers,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	Examples    json.RawMessage `json:"examples,omitempty"`
	Tags        []Tag           `json:"tags,omitempty"`
	Bindings    json.RawMessage `json:"bindings,omitempty"`

	// Extensions holds "x-" values on this object. See [Extensions].
	Extensions Extensions `json:"-"`
}

// Components holds reusable definitions referenced from elsewhere in the
// document. Schemas is where [SchemaFromType] definitions belong; entries are
// referenced as "#/components/schemas/Name".
type Components struct {
	Schemas         map[string]json.RawMessage `json:"schemas,omitempty"`
	Messages        map[string]Message         `json:"messages,omitempty"`
	Parameters      map[string]Parameter       `json:"parameters,omitempty"`
	Servers         map[string]Server          `json:"servers,omitempty"`
	Channels        map[string]SpecChannel     `json:"channels,omitempty"`
	SecuritySchemes map[string]SecurityScheme  `json:"securitySchemes,omitempty"`

	// Extensions holds "x-" values on this object. See [Extensions].
	Extensions Extensions `json:"-"`
}

// RefObj is a JSON Reference to another part of the document. Build one with
// [Ref].
type RefObj struct {
	Ref string `json:"$ref,omitempty"`
}

// Ref returns a reference to target, e.g. "#/channels/portTotals".
func Ref(target string) RefObj {
	return RefObj{Ref: target}
}
