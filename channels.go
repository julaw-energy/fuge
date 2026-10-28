package fuge

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"sync"

	"github.com/centrifugal/centrifuge"
)

// Params holds the values extracted from a channel address's parameters —
// e.g. {"roomId": "room-42"} for a channel registered with pattern
// "chat:{roomId}" when a client subscribes to "chat:room-42".
type Params map[string]string

// operationKind is the value fuge writes to x-fuge-operation-kind on every
// operation it generates, so a document consumer (a human, a doc renderer, a
// future client generator) can tell an RPC method apart from a broadcast or
// a client-initiated publish without guessing from channel naming.
type operationKind string

const (
	operationKindRPC       operationKind = "rpc"
	operationKindBroadcast operationKind = "broadcast"
	operationKindPublish   operationKind = "publish"

	// extensionOperationKind is the extension key itself. "fuge" namespaces
	// it so it can never collide with some other tool's same-named idea of
	// "operation kind" — the same reasoning "x-" extensions always want.
	extensionOperationKind = "x-fuge-operation-kind"
)

func withOperationKind(ext Extensions, kind operationKind) Extensions {
	out := Extensions{}
	for k, v := range ext {
		out[k] = v
	}
	out[extensionOperationKind] = string(kind)
	return out
}

// Channel describes one addressable, real Centrifuge channel — as opposed to
// the synthetic "rpc.<method>" channels Register creates for RPC methods.
// Pattern may contain {name} placeholders (e.g. "chat:{roomId}"), matching
// AsyncAPI's own channel-parameter convention; a placeholder matches one or
// more characters that are not ':', matching Centrifuge's usual convention
// of ':' as a namespace separator in channel names.
type Channel struct {
	// ID names this channel in the document (as Operation.OperationID does
	// for RPC) and is used to derive its message and operation names.
	ID string
	// Pattern is the channel address, with optional {name} placeholders.
	Pattern     string
	Summary     string
	Description string
	Tags        []string
	// Security gates who may subscribe at the scheme level — see
	// ChannelHandle.OnSubscribe for authorization beyond that (e.g. "does
	// this specific room belong to this specific caller").
	Security   []map[string][]string
	Extensions map[string]any
	// PublishExtensions are the "x-" extensions on the operation that
	// documents client publishing (see AllowPublish), e.g. the permissions a
	// publisher needs. Channel gates see them as ChannelInfo.PublishExtensions.
	PublishExtensions map[string]any
}

// ChannelAction is what a client is attempting on a channel.
type ChannelAction string

const (
	ChannelSubscribe ChannelAction = "subscribe"
	ChannelPublish   ChannelAction = "publish"
)

// ChannelInfo describes a registered channel to a [ChannelGate]. It is shared
// and must not be modified.
type ChannelInfo struct {
	ID       string
	Pattern  string
	Security []map[string][]string
	// Extensions are Channel.Extensions: they document subscribing, and are
	// what a gate reads to authorize ChannelSubscribe.
	Extensions map[string]any
	// PublishExtensions are Channel.PublishExtensions, for ChannelPublish.
	PublishExtensions map[string]any
}

// ChannelGate authorizes a client's action on a channel before it happens, the
// channel counterpart of [OpMiddleware]: it sees the channel's extensions, so
// a requirement documented there (say "x-permissions") can be enforced in one
// place instead of in every channel's OnSubscribe. ctx is the connection's
// context. Returning an error rejects the action; a *fuge.Error or
// *centrifuge.Error reaches the client as is (e.g. centrifuge.ErrorPermissionDenied).
// Gates run in registration order, before the channel's own OnSubscribe.
type ChannelGate func(ctx context.Context, ch *ChannelInfo, action ChannelAction, params Params, info SubscribeInfo) error

// SubscribeInfo is what a channel's OnSubscribe handler receives about the
// client attempting to subscribe.
type SubscribeInfo struct {
	UserID   string
	ClientID string
	Channel  string // the literal address requested, e.g. "chat:room-42"
}

// addressPattern compiles a Channel.Pattern into something that can match a
// real address and extract its parameters, and render the reverse.
type addressPattern struct {
	source     string
	re         *regexp.Regexp
	paramNames []string
}

var paramRe = regexp.MustCompile(`\{([a-zA-Z_][a-zA-Z0-9_]*)\}`)

func compilePattern(pattern string) (*addressPattern, error) {
	if pattern == "" {
		return nil, fmt.Errorf("empty pattern")
	}

	var reBuf strings.Builder
	var names []string
	last := 0

	reBuf.WriteString("^")
	for _, loc := range paramRe.FindAllStringSubmatchIndex(pattern, -1) {
		reBuf.WriteString(regexp.QuoteMeta(pattern[last:loc[0]]))
		name := pattern[loc[2]:loc[3]]
		names = append(names, name)
		reBuf.WriteString(fmt.Sprintf("(?P<%s>[^:]+)", name))
		last = loc[1]
	}
	reBuf.WriteString(regexp.QuoteMeta(pattern[last:]))
	reBuf.WriteString("$")

	re, err := regexp.Compile(reBuf.String())
	if err != nil {
		return nil, fmt.Errorf("compiling pattern %q: %w", pattern, err)
	}

	return &addressPattern{source: pattern, re: re, paramNames: names}, nil
}

func (p *addressPattern) match(address string) (Params, bool) {
	m := p.re.FindStringSubmatch(address)
	if m == nil {
		return nil, false
	}
	params := Params{}
	for i, name := range p.re.SubexpNames() {
		if i == 0 || name == "" {
			continue
		}
		params[name] = m[i]
	}
	return params, true
}

// render substitutes params into the pattern, producing a real address, e.g.
// "chat:{roomId}" + {"roomId": "room-42"} -> "chat:room-42". It errors if a
// required parameter is missing, or if a value would make the rendered
// address fail to match its own pattern (i.e. contains ':').
func (p *addressPattern) render(params Params) (string, error) {
	result := p.source
	for _, name := range p.paramNames {
		v, ok := params[name]
		if !ok {
			return "", fmt.Errorf("missing parameter %q", name)
		}
		if strings.Contains(v, ":") {
			return "", fmt.Errorf("parameter %q value %q must not contain ':'", name, v)
		}
		result = strings.Replace(result, "{"+name+"}", v, 1)
	}
	return result, nil
}

// registeredChannel is what API dispatches subscribe/publish events against;
// implemented by *ChannelHandle[T] for every T.
type registeredChannel interface {
	match(address string) (Params, bool)
	channelInfo() *ChannelInfo
	authorizeSubscribe(ctx context.Context, params Params, info SubscribeInfo) error
	canPublish() bool
	dispatchPublish(ctx context.Context, params Params, data []byte) ([]byte, error)
}

// ChannelHandle is returned by RegisterChannel: the typed handle used to
// publish to this channel, gate subscriptions, and (via AllowPublish) accept
// client-initiated publishes.
type ChannelHandle[T any] struct {
	api     *API
	key     string // document channel key, e.g. "chat-room"
	pattern *addressPattern
	info    *ChannelInfo

	mu          sync.Mutex
	onSubscribe func(ctx context.Context, params Params, info SubscribeInfo) error
	publish     func(ctx context.Context, params Params, data []byte) ([]byte, error)
}

// RegisterChannel documents ch as a real (non-RPC) channel with a "send"
// operation — the server publishing to it — and returns a handle for
// publishing typed messages of type T. Combine with AllowPublish if clients
// (including other services acting as ordinary clients) should be able to
// publish into it directly.
func RegisterChannel[T any](api *API, ch Channel) *ChannelHandle[T] {
	if err := validateName(ch.ID); err != nil {
		panic(fmt.Sprintf("fuge: invalid channel id %q: %v", ch.ID, err))
	}
	pattern, err := compilePattern(ch.Pattern)
	if err != nil {
		panic(fmt.Sprintf("fuge: channel %q: %v", ch.ID, err))
	}

	handle := &ChannelHandle[T]{api: api, key: ch.ID, pattern: pattern, info: &ChannelInfo{
		ID: ch.ID, Pattern: ch.Pattern, Security: ch.Security,
		Extensions: ch.Extensions, PublishExtensions: ch.PublishExtensions,
	}}

	api.mu.Lock()
	defer api.mu.Unlock()

	if _, dup := api.doc.Channels[ch.ID]; dup {
		panic(fmt.Sprintf("fuge: channel %q already registered", ch.ID))
	}
	opID := ch.ID + "-broadcast"
	if _, dup := api.doc.Operations[opID]; dup {
		panic(fmt.Sprintf("fuge: duplicate operation id %q", opID))
	}

	payload, defs, err := api.payloadFor(reflect.TypeFor[T]())
	if err != nil {
		panic(fmt.Sprintf("fuge: channel %q: reflecting message type %s: %v", ch.ID, reflect.TypeFor[T](), err))
	}
	if defs != nil {
		if err := MergeSchemas(api.schemas, defs); err != nil {
			panic(fmt.Sprintf("fuge: channel %q: schema name collision: %v", ch.ID, err))
		}
	}

	msgName := ch.ID + ".broadcast"
	api.doc.Components.Messages[msgName] = Message{
		Name: msgName, Title: ch.Summary, Summary: "Broadcast payload", Payload: payload,
	}

	tags := make([]Tag, 0, len(ch.Tags))
	for _, t := range ch.Tags {
		tags = append(tags, Tag{Name: t})
	}
	params := make(map[string]Parameter, len(pattern.paramNames))
	for _, name := range pattern.paramNames {
		params[name] = Parameter{}
	}

	chRef := "#/channels/" + ch.ID
	api.doc.Channels[ch.ID] = SpecChannel{
		Address:     ch.Pattern,
		Title:       ch.ID,
		Summary:     ch.Summary,
		Description: ch.Description,
		Tags:        tags,
		Parameters:  params,
		// The channel's message entry points at the real message in
		// components (where the payload lives) — not at itself. Operations,
		// below, then point at this entry via the channel.
		Messages: map[string]Message{
			"broadcast": {Ref: "#/components/messages/" + msgName},
		},
	}

	api.doc.Operations[opID] = SpecOperation{
		Action:      ActionSend,
		Channel:     Ref(chRef),
		Title:       ch.Summary,
		Summary:     ch.Summary,
		Description: ch.Description,
		Tags:        tags,
		Messages:    []RefObj{Ref(chRef + "/messages/broadcast")},
		Security:    api.securityRefsFor(ch.ID, ch.Security),
		Extensions:  withOperationKind(Extensions(ch.Extensions), operationKindBroadcast),
	}

	api.subChannels = append(api.subChannels, handle)
	return handle
}

// AllowPublish lets clients (including other services acting as ordinary
// Centrifuge clients) publish directly into ch, documenting a second,
// "receive" operation on the same channel. Req is what a publisher must
// send — decoded and Validate()'d exactly like an RPC request — and is
// deliberately allowed to differ from T, what subscribers receive: fn is the
// seam between them. Returning (nil, nil) accepts the publish without
// broadcasting anything.
func AllowPublish[T, Req any](ch *ChannelHandle[T], fn func(ctx context.Context, params Params, req *Req) (*T, error)) {
	api := ch.api
	api.mu.Lock()
	defer api.mu.Unlock()

	ch.mu.Lock()
	alreadySet := ch.publish != nil
	ch.mu.Unlock()
	if alreadySet {
		panic(fmt.Sprintf("fuge: channel %q: AllowPublish already set", ch.key))
	}

	opID := ch.key + "-publish"
	if _, dup := api.doc.Operations[opID]; dup {
		panic(fmt.Sprintf("fuge: duplicate operation id %q", opID))
	}

	_, hasValidator := any(new(Req)).(Validator)
	reqPayload, reqDefs, err := api.payloadFor(reflect.TypeFor[Req]())
	if err != nil {
		panic(fmt.Sprintf("fuge: channel %q: reflecting publish type %s: %v", ch.key, reflect.TypeFor[Req](), err))
	}
	if reqDefs != nil {
		if err := MergeSchemas(api.schemas, reqDefs); err != nil {
			panic(fmt.Sprintf("fuge: channel %q: schema name collision: %v", ch.key, err))
		}
	}
	_ = hasValidator // publish acks carry no payload (see AllowPublish's doc comment), so there is nowhere to attach a validation-error code today

	msgName := ch.key + ".publish"
	api.doc.Components.Messages[msgName] = Message{
		Name: msgName, Summary: "Publish payload (sent by the publisher)", Payload: reqPayload,
	}

	chRef := "#/channels/" + ch.key
	spec := api.doc.Channels[ch.key]
	if spec.Messages == nil {
		spec.Messages = map[string]Message{}
	}
	spec.Messages["publish"] = Message{Ref: "#/components/messages/" + msgName}
	api.doc.Channels[ch.key] = spec

	api.doc.Operations[opID] = SpecOperation{
		Action:     ActionReceive,
		Channel:    Ref(chRef),
		Summary:    "Publish into this channel",
		Messages:   []RefObj{Ref(chRef + "/messages/publish")},
		Extensions: withOperationKind(Extensions(ch.info.PublishExtensions), operationKindPublish),
	}

	codec := api.codec
	dispatch := func(ctx context.Context, params Params, data []byte) ([]byte, error) {
		in := new(Req)
		if len(data) > 0 {
			if err := codec.Unmarshal(data, in); err != nil {
				return nil, fmt.Errorf("decode publish to %s: %v: %w", ch.key, err, centrifuge.ErrorBadRequest)
			}
		}
		if v, ok := any(in).(Validator); ok {
			if err := v.Validate(); err != nil {
				return nil, &centrifuge.Error{Code: CodeValidation, Message: err.Error()}
			}
		}
		out, err := fn(ctx, params, in)
		if err != nil {
			return nil, err
		}
		if out == nil {
			return nil, nil
		}
		return codec.Marshal(out)
	}

	ch.mu.Lock()
	ch.publish = dispatch
	ch.mu.Unlock()
}

// OnSubscribe gates who may subscribe to this channel, beyond the coarse
// Security requirement — e.g. checking that params["userId"] matches the
// authenticated caller. Returning an error rejects the subscription. Returns
// ch so it chains at the call site: RegisterChannel(...).OnSubscribe(...).
func (ch *ChannelHandle[T]) OnSubscribe(fn func(ctx context.Context, params Params, info SubscribeInfo) error) *ChannelHandle[T] {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.onSubscribe = fn
	return ch
}

// Publish sends msg to the channel identified by params (e.g.
// {"roomId": "room-42"} for a channel registered as "chat:{roomId}"), via
// node.Publish. The type used here is exactly the type RegisterChannel
// documented for this channel — there is no other way to publish to it.
func (ch *ChannelHandle[T]) Publish(node *centrifuge.Node, params Params, msg T) error {
	address, err := ch.pattern.render(params)
	if err != nil {
		return fmt.Errorf("fuge: channel %q: %w", ch.key, err)
	}
	data, err := ch.api.codec.Marshal(msg)
	if err != nil {
		return fmt.Errorf("fuge: channel %q: encoding message: %w", ch.key, err)
	}
	_, err = node.Publish(address, data)
	return err
}

func (ch *ChannelHandle[T]) match(address string) (Params, bool) { return ch.pattern.match(address) }

func (ch *ChannelHandle[T]) channelInfo() *ChannelInfo { return ch.info }

func (ch *ChannelHandle[T]) canPublish() bool {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	return ch.publish != nil
}

func (ch *ChannelHandle[T]) authorizeSubscribe(ctx context.Context, params Params, info SubscribeInfo) error {
	ch.mu.Lock()
	fn := ch.onSubscribe
	ch.mu.Unlock()
	if fn == nil {
		return nil
	}
	return fn(ctx, params, info)
}

func (ch *ChannelHandle[T]) dispatchPublish(ctx context.Context, params Params, data []byte) ([]byte, error) {
	ch.mu.Lock()
	fn := ch.publish
	ch.mu.Unlock()
	if fn == nil {
		return nil, centrifuge.ErrorPermissionDenied
	}
	return fn(ctx, params, data)
}
