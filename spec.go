package fuge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"
)

const (
	codeInternal   uint32 = 100 // centrifuge.ErrorInternal
	codeBadRequest uint32 = 107 // centrifuge.ErrorBadRequest
)

func isEmptyStruct(t reflect.Type) bool {
	return t.Kind() == reflect.Struct && t.NumField() == 0
}

// payloadFor reflects t into a payload schema. Empty structs mean "no payload".
func (a *API) payloadFor(t reflect.Type) (json.RawMessage, map[string]json.RawMessage, error) {
	if isEmptyStruct(t) {
		return nil, nil, nil
	}
	return SchemaFromType(reflect.Zero(t).Interface(), a.reflectOpts...)
}

// commit records op in the document and returns the middleware snapshot.
func (a *API) commit(op *Operation, reqT, resT reflect.Type, hasValidator bool) []OpMiddleware {
	a.mu.Lock()
	defer a.mu.Unlock()

	if other, dup := a.methods[op.Method]; dup {
		panic(fmt.Sprintf("fuge: method %q already registered by operation %q", op.Method, other))
	}
	if _, dup := a.doc.Operations[op.OperationID]; dup {
		panic(fmt.Sprintf("fuge: duplicate operation id %q", op.OperationID))
	}

	reqPayload, reqDefs, err := a.payloadFor(reqT)
	if err != nil {
		panic(fmt.Sprintf("fuge: operation %q: reflecting request type %s: %v", op.OperationID, reqT, err))
	}
	resPayload, resDefs, err := a.payloadFor(resT)
	if err != nil {
		panic(fmt.Sprintf("fuge: operation %q: reflecting response type %s: %v", op.OperationID, resT, err))
	}
	for _, defs := range []map[string]json.RawMessage{reqDefs, resDefs} {
		if defs == nil {
			continue
		}
		if err := MergeSchemas(a.schemas, defs); err != nil {
			panic(fmt.Sprintf("fuge: operation %q: schema name collision (two different types share a name): %v", op.OperationID, err))
		}
	}

	security := a.securityRefs(op)
	extensions := extensionsFor(op)
	extensions = withOperationKind(extensions, operationKindRPC)

	// Codes documented on the error reply.
	codes := map[uint32]struct{}{codeInternal: {}}
	if reqPayload != nil {
		codes[codeBadRequest] = struct{}{}
	}
	if hasValidator {
		codes[CodeValidation] = struct{}{}
	}
	for _, c := range op.Errors {
		codes[c] = struct{}{}
	}

	id := op.OperationID
	chKey := "rpc." + op.Method
	chRef := "#/channels/" + chKey
	msgReq, msgRes, msgErr := id+".request", id+".reply", id+".error"

	tags := make([]Tag, 0, len(op.Tags))
	for _, t := range op.Tags {
		tags = append(tags, Tag{Name: t})
	}

	a.doc.Components.Messages[msgReq] = Message{
		Name: msgReq, Title: msgReq, Summary: "Request payload", Payload: reqPayload,
	}
	a.doc.Components.Messages[msgRes] = Message{
		Name: msgRes, Title: msgRes, Summary: "Successful reply payload", Payload: resPayload,
	}
	a.doc.Components.Messages[msgErr] = Message{
		Name: msgErr, Title: msgErr, Summary: "Error reply", Payload: errorPayload(codes),
	}

	a.doc.Channels[chKey] = SpecChannel{
		Address:     op.Method,
		Title:       op.Method,
		Summary:     op.Summary,
		Description: op.Description,
		Tags:        tags,
		Messages: map[string]Message{
			"request": {Ref: "#/components/messages/" + msgReq},
			"reply":   {Ref: "#/components/messages/" + msgRes},
			"error":   {Ref: "#/components/messages/" + msgErr},
		},
	}

	a.doc.Operations[id] = SpecOperation{
		Action:      ActionReceive, // from the server's point of view
		Channel:     Ref(chRef),
		Title:       id,
		Summary:     op.Summary,
		Description: op.Description,
		Tags:        tags,
		Messages:    []RefObj{Ref(chRef + "/messages/request")},
		Reply: &OperationReply{
			Channel: Ref(chRef),
			Messages: []RefObj{
				Ref(chRef + "/messages/reply"),
				Ref(chRef + "/messages/error"),
			},
		},
		Security:   security,
		Extensions: extensions,
	}
	a.methods[op.Method] = id

	return append([]OpMiddleware(nil), a.middlewares...)
}

// securityRefs turns op.Security — huma-style: each map is one alternative
// requirement, and a map with more than one entry would be an "AND" of
// schemes — into asyncapi3 references. AsyncAPI's operation security is a
// flat "OR" list of scheme references (see SecurityScheme's doc
// comment), so an "AND" cannot be expressed and panics here, same as before
// this was a native field.
func (a *API) securityRefs(op *Operation) []RefObj {
	return a.securityRefsFor(op.OperationID, op.Security)
}

// securityRefsFor is the shared implementation behind securityRefs, reused
// by channel registration (see channels.go) so both share one validation
// path rather than two copies that could drift.
func (a *API) securityRefsFor(name string, security []map[string][]string) []RefObj {
	var refs []RefObj
	for _, req := range security {
		if len(req) > 1 {
			panic(fmt.Sprintf("fuge: %q: a security requirement combining several schemes (AND) is not expressible in AsyncAPI 3", name))
		}
		for schemeName := range req {
			if _, ok := a.schemes[schemeName]; !ok {
				panic(fmt.Sprintf("fuge: %q references unknown security scheme %q (declare it in Config.SecuritySchemes)", name, schemeName))
			}
			refs = append(refs, Ref("#/components/securitySchemes/"+schemeName))
		}
	}
	return refs
}

// extensionsFor validates op.Extensions' keys up front, so a malformed key
// panics at registration with the operation named, rather than surfacing
// later as a plain marshal error out of API.JSON.
func extensionsFor(op *Operation) Extensions {
	for k := range op.Extensions {
		if !strings.HasPrefix(k, "x-") {
			panic(fmt.Sprintf("fuge: operation %q: extension key %q must start with \"x-\"", op.OperationID, k))
		}
	}
	return op.Extensions
}

// errorPayload is the shape of a Centrifuge error, with the code restricted to
// the ones this operation can actually return.
func errorPayload(codes map[uint32]struct{}) json.RawMessage {
	enum := make([]uint32, 0, len(codes))
	for c := range codes {
		enum = append(enum, c)
	}
	sort.Slice(enum, func(i, j int) bool { return enum[i] < enum[j] })
	return mustJSON(map[string]any{
		"type":     "object",
		"required": []string{"code", "message"},
		"properties": map[string]any{
			"code":      map[string]any{"type": "integer", "enum": enum},
			"message":   map[string]any{"type": "string"},
			"temporary": map[string]any{"type": "boolean"},
		},
	})
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// JSON renders the document and, if Config.Validator is set, runs it over the
// result. Security, extensions and security schemes
// are already part of a.doc by the time this runs — Register sets them
// directly on native fields — so there is no patch step here anymore; this
// is just marshal and, optionally, validate. Field order is whatever
// Document's own MarshalJSON produces (declared fields in their
// struct order, then any "x-" extensions sorted after them), so output is
// still deterministic.
func (a *API) JSON() ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	raw, err := json.Marshal(a.doc)
	if err != nil {
		return nil, err
	}
	if a.validator != nil {
		if err := a.validator(raw); err != nil {
			return nil, fmt.Errorf("generated document is not valid AsyncAPI %s: %w", Version, err)
		}
	}

	return raw, nil
}

func dropFlowStyle(node *yaml.Node) {
	node.Style = 0 // Reset style to default (BlockStyle)
	for _, child := range node.Content {
		dropFlowStyle(child)
	}
}

func jsonToYaml(jsonBytes []byte) []byte {
	var node yaml.Node
	if err := yaml.Unmarshal(jsonBytes, &node); err != nil {
		return []byte("")
	}

	dropFlowStyle(&node)

	yamlBytes, err := yaml.Marshal(&node)
	if err != nil {
		return []byte("")
	}

	return yamlBytes
}

type contentType int

// Declare the enum values
const (
	jsonContent contentType = iota
	yamlContent
)

// DocHandler serves the document (mount it e.g. at /asyncapi.json). The
// document is generated once, on first request, so register all operations
// before serving.
func (a *API) docHandler(cType contentType) http.Handler {
	var (
		once sync.Once
		body []byte
		err  error
	)
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		once.Do(func() { body, err = a.JSON() })
		if err != nil {
			http.Error(w, "asyncapi generation failed", http.StatusInternalServerError)
			return
		}
		if cType == jsonContent {
			w.Header().Set("Content-Type", "application/vnd.aai.asyncapi+json")
			_, _ = w.Write(body)
		} else {
			w.Header().Set("Content-Type", "application/vnd.aai.asyncapi+yaml")
			_, _ = w.Write(jsonToYaml(body))
		}
	})
}

func (a *API) JsonDocHandler() http.Handler {
	return a.docHandler(jsonContent)
}

func (a *API) YamlDocHandler() http.Handler {
	return a.docHandler(yamlContent)
}
