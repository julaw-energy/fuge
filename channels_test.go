package fuge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/centrifugal/centrifuge"
)

func TestAddressPatternMatchAndRender(t *testing.T) {
	p, err := compilePattern("chat:{roomId}")
	if err != nil {
		t.Fatal(err)
	}

	params, ok := p.match("chat:room-42")
	if !ok || params["roomId"] != "room-42" {
		t.Fatalf("match: %v %v", params, ok)
	}
	if _, ok := p.match("chat:room-42:extra"); ok {
		t.Fatal("should not match trailing segments")
	}
	if _, ok := p.match("other:room-42"); ok {
		t.Fatal("should not match a different literal prefix")
	}
	// A value containing ':' must not be swallowed into one param.
	if _, ok := p.match("chat:a:b"); ok {
		t.Fatal("should not match when the parameter segment itself contains ':'")
	}

	addr, err := p.render(Params{"roomId": "room-42"})
	if err != nil || addr != "chat:room-42" {
		t.Fatalf("render: %s, %v", addr, err)
	}
	if _, err := p.render(Params{}); err == nil {
		t.Fatal("expected an error for a missing parameter")
	}
	if _, err := p.render(Params{"roomId": "a:b"}); err == nil {
		t.Fatal("expected an error for a parameter value containing ':'")
	}
}

func TestAddressPatternMultipleParamsAndLiteralOnly(t *testing.T) {
	p, err := compilePattern("org:{orgId}:room:{roomId}")
	if err != nil {
		t.Fatal(err)
	}
	params, ok := p.match("org:acme:room:42")
	if !ok || params["orgId"] != "acme" || params["roomId"] != "42" {
		t.Fatalf("got %v %v", params, ok)
	}

	lit, err := compilePattern("global-notifications")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lit.match("global-notifications"); !ok {
		t.Fatal("literal-only pattern should match its exact address")
	}
	if _, ok := lit.match("global-notifications-x"); ok {
		t.Fatal("literal-only pattern should not match a longer string")
	}
}

type Notification struct {
	Text string `json:"text" required:"true"`
}

func TestRegisterChannelDocumentShape(t *testing.T) {
	api := newAPI(t)
	ch := RegisterChannel[Notification](api, Channel{
		ID: "user-notifications", Pattern: "user:{userId}:notifications",
		Summary:  "Per-user notification stream",
		Tags:     []string{"Notifications"},
		Security: []map[string][]string{{"bearer": {}}},
	})

	spec, ok := api.Document().Channels["user-notifications"]
	if !ok {
		t.Fatal("channel missing from document")
	}
	if spec.Address != "user:{userId}:notifications" {
		t.Fatalf("address: %s", spec.Address)
	}
	if _, ok := spec.Parameters["userId"]; !ok {
		t.Fatalf("parameters missing userId: %v", spec.Parameters)
	}

	op, ok := api.Document().Operations["user-notifications-broadcast"]
	if !ok {
		t.Fatal("broadcast operation missing")
	}
	if op.Action != ActionSend {
		t.Fatalf("action: %s", op.Action)
	}
	if op.Extensions[extensionOperationKind] != string(operationKindBroadcast) {
		t.Fatalf("kind: %v", op.Extensions[extensionOperationKind])
	}
	if len(op.Security) != 1 {
		t.Fatalf("security: %v", op.Security)
	}

	out, err := api.JSON() // validates against the real meta-schema
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Operations map[string]map[string]json.RawMessage `json:"operations"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if compact(t, doc.Operations["user-notifications-broadcast"]["x-fuge-operation-kind"]) != `"broadcast"` {
		t.Errorf("kind not in rendered JSON: %s", out)
	}

	// Publish renders the address and hands off to a real Node — Node.Publish
	// itself is centrifuge's own well-established behavior, so this only
	// needs to confirm fuge's part (rendering + encoding) doesn't error;
	// what actually reaches subscribers is covered by
	// TestAddressPatternMatchAndRender and TestPublishDispatchEndToEnd.
	node, err := centrifuge.New(centrifuge.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := node.Run(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = node.Shutdown(context.Background()) }()

	if err := ch.Publish(node, Params{"userId": "u1"}, Notification{Text: "hi"}); err != nil {
		t.Fatal(err)
	}

	// Rendering with a missing parameter must fail before ever touching the
	// node — passing nil here confirms that: a nil node would panic if
	// Publish tried to use it.
	if err := ch.Publish(nil, Params{}, Notification{Text: "x"}); err == nil {
		t.Fatal("expected an error for a missing parameter")
	}
}

type ChatPublishInput struct {
	Text string `json:"text" required:"true"`
}
type ChatMessage struct {
	RoomID string `json:"roomId" required:"true"`
	Author string `json:"author" required:"true"`
	Text   string `json:"text" required:"true"`
}

func TestAllowPublishDocumentShapeAndDualTypes(t *testing.T) {
	api := newAPI(t)
	chat := RegisterChannel[ChatMessage](api, Channel{ID: "chat-room", Pattern: "chat:{roomId}"})
	AllowPublish(chat, func(ctx context.Context, params Params, in *ChatPublishInput) (*ChatMessage, error) {
		return &ChatMessage{RoomID: params["roomId"], Author: "someone", Text: in.Text}, nil
	})

	op, ok := api.Document().Operations["chat-room-publish"]
	if !ok {
		t.Fatal("publish operation missing")
	}
	if op.Action != ActionReceive {
		t.Fatalf("action: %s", op.Action)
	}
	if op.Extensions[extensionOperationKind] != string(operationKindPublish) {
		t.Fatalf("kind: %v", op.Extensions[extensionOperationKind])
	}

	spec := api.Document().Channels["chat-room"]
	if _, ok := spec.Messages["broadcast"]; !ok {
		t.Fatal("broadcast message missing after AllowPublish")
	}
	if _, ok := spec.Messages["publish"]; !ok {
		t.Fatal("publish message missing")
	}

	for _, name := range []string{"ChatPublishInput", "ChatMessage"} {
		if _, ok := api.Document().Components.Schemas[name]; !ok {
			t.Errorf("schema %s missing: %v", name, api.Document().Components.Schemas)
		}
	}

	if _, err := api.JSON(); err != nil { // validates the mixed rpc+channel document
		t.Fatal(err)
	}

	mustPanic(t, "already set", func() {
		AllowPublish(chat, func(context.Context, Params, *ChatPublishInput) (*ChatMessage, error) { return nil, nil })
	})
}

func TestAllowPublishRejectsBadPayload(t *testing.T) {
	api := newAPI(t)
	chat := RegisterChannel[ChatMessage](api, Channel{ID: "chat-room2", Pattern: "chat2:{roomId}"})
	AllowPublish(chat, func(ctx context.Context, params Params, in *ChatPublishInput) (*ChatMessage, error) {
		if in.Text == "" {
			return nil, errors.New("empty text")
		}
		return &ChatMessage{RoomID: params["roomId"], Author: "x", Text: in.Text}, nil
	})

	out, err := chat.dispatchPublish(context.Background(), Params{"roomId": "1"}, []byte(`{nope`))
	if out != nil || !errors.Is(err, centrifuge.ErrorBadRequest) {
		t.Fatalf("want bad request, got %v %v", out, err)
	}
}

// --- end-to-end: the same dispatch logic API.Attach wires up ---
//
// A real *centrifuge.Client only exists once a real connection (a real
// centrifuge.Transport) has been established — the library gives no public
// way to construct one standalone for testing. So rather than depend on a
// hand-rolled transport, these tests drive the exact same dispatch path
// api.go's Attach uses — findRegisteredChannel does what its OnSubscribe/
// OnPublish closures do: find the channel whose pattern matches the
// address, or report none matched — and then call the same
// registeredChannel methods Attach calls on whichever one it finds.

func findRegisteredChannel(api *API, address string) (registeredChannel, Params, bool) {
	for _, ch := range api.subChannels {
		if params, ok := ch.match(address); ok {
			return ch, params, true
		}
	}
	return nil, nil, false
}

func TestSubscribeDispatchEndToEnd(t *testing.T) {
	api := newAPI(t)
	notif := RegisterChannel[Notification](api, Channel{ID: "notif-e2e", Pattern: "notif2:{userId}"})
	notif.OnSubscribe(func(ctx context.Context, params Params, info SubscribeInfo) error {
		if params["userId"] != info.UserID {
			return centrifuge.ErrorPermissionDenied
		}
		return nil
	})

	authorize := func(address string, userID string) error {
		ch, params, ok := findRegisteredChannel(api, address)
		if !ok {
			return centrifuge.ErrorUnknownChannel
		}
		info := SubscribeInfo{UserID: userID, ClientID: "c1", Channel: address}
		return ch.authorizeSubscribe(context.Background(), params, info)
	}

	if err := authorize("notif2:u1", "u1"); err != nil {
		t.Fatalf("own channel should be allowed: %v", err)
	}
	if err := authorize("notif2:someoneelse", "u1"); !errors.Is(err, centrifuge.ErrorPermissionDenied) {
		t.Fatalf("want permission denied, got %v", err)
	}
	if err := authorize("totally:unregistered", "u1"); !errors.Is(err, centrifuge.ErrorUnknownChannel) {
		t.Fatalf("want unknown channel, got %v", err)
	}
}

func TestPublishDispatchEndToEnd(t *testing.T) {
	api := newAPI(t)
	chat := RegisterChannel[ChatMessage](api, Channel{ID: "chat-e2e", Pattern: "chate2e:{roomId}"})
	AllowPublish(chat, func(ctx context.Context, params Params, in *ChatPublishInput) (*ChatMessage, error) {
		return &ChatMessage{RoomID: params["roomId"], Author: "svc", Text: in.Text}, nil
	})
	// A second channel with no AllowPublish at all.
	RegisterChannel[Notification](api, Channel{ID: "readonly-e2e", Pattern: "readonly2e:{x}"})

	// publish mirrors exactly what api.go's OnPublish closure does: find the
	// channel, reject if it never called AllowPublish, otherwise dispatch —
	// returning the bytes that would be handed to node.Publish, or an error.
	publish := func(address string, data []byte) ([]byte, error) {
		ch, params, ok := findRegisteredChannel(api, address)
		if !ok {
			return nil, centrifuge.ErrorUnknownChannel
		}
		if !ch.canPublish() {
			return nil, centrifuge.ErrorPermissionDenied
		}
		return ch.dispatchPublish(context.Background(), params, data)
	}

	// A publish into the AllowPublish channel is transformed — what would be
	// broadcast reflects fn's output, not the raw client bytes.
	out, err := publish("chate2e:room1", []byte(`{"text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got ChatMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.Author != "svc" || got.RoomID != "room1" || got.Text != "hi" {
		t.Fatalf("broadcast does not reflect the transform: %+v", got)
	}

	// A channel that never called AllowPublish rejects publish outright.
	if _, err := publish("readonly2e:x", []byte(`{"text":"nope"}`)); !errors.Is(err, centrifuge.ErrorPermissionDenied) {
		t.Fatalf("want permission denied, got %v", err)
	}

	// An address matching no registered channel at all.
	if _, err := publish("nowhere:x", []byte(`{}`)); !errors.Is(err, centrifuge.ErrorUnknownChannel) {
		t.Fatalf("want unknown channel, got %v", err)
	}
}

// --- every $ref in a generated document must resolve to something real ---
//
// The AsyncAPI meta-schema only checks that a "$ref" is a string, never that
// it points anywhere — so a dangling or self-referential $ref sails through
// Validate() and only breaks whatever later tries to follow it (a doc
// renderer, a client generator). These tests follow every one.

// resolveRef follows a "#/..." JSON pointer through the rendered document,
// hopping through any further $ref objects it lands on, and fails the test on
// anything dangling, circular, or not an object.
func resolveRef(t *testing.T, doc map[string]any, ref string) map[string]any {
	t.Helper()
	seen := map[string]bool{}
	for {
		if seen[ref] {
			t.Fatalf("circular $ref: %s", ref)
		}
		seen[ref] = true
		if !strings.HasPrefix(ref, "#/") {
			t.Fatalf("unsupported $ref %q", ref)
		}
		var cur any = doc
		for _, seg := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			seg = strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")
			m, ok := cur.(map[string]any)
			if !ok {
				t.Fatalf("dangling $ref %s: cannot descend into %q", ref, seg)
			}
			if cur, ok = m[seg]; !ok {
				t.Fatalf("dangling $ref %s: no %q", ref, seg)
			}
		}
		node, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("$ref %s does not point at an object", ref)
		}
		next, isRef := node["$ref"].(string)
		if !isRef {
			return node
		}
		ref = next
	}
}

func collectRefs(v any, out *[]string) {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if s, ok := child.(string); ok && k == "$ref" {
				*out = append(*out, s)
				continue
			}
			collectRefs(child, out)
		}
	case []any:
		for _, child := range x {
			collectRefs(child, out)
		}
	}
}

func TestGeneratedDocumentRefsAllResolve(t *testing.T) {
	api := newAPI(t)
	Register(api, Operation{OperationID: "create-country", Method: "country.create"}, createCountry)
	RegisterChannel[Notification](api, Channel{ID: "refs-notif", Pattern: "refs-notif", Summary: "literal address"})
	chat := RegisterChannel[ChatMessage](api, Channel{ID: "refs-chat", Pattern: "refs-chat:{roomId}"})
	AllowPublish(chat, func(context.Context, Params, *ChatPublishInput) (*ChatMessage, error) { return nil, nil })

	out, err := api.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}

	var refs []string
	collectRefs(doc, &refs)
	if len(refs) == 0 {
		t.Fatal("expected the document to contain $refs")
	}
	for _, ref := range refs {
		resolveRef(t, doc, ref)
	}

	// Beyond merely resolving: every message on a real (non-rpc) channel must
	// end at an actual message carrying a payload, or the type a subscriber
	// should expect is unreachable from the document.
	channels := doc["channels"].(map[string]any)
	for name, raw := range channels {
		if strings.HasPrefix(name, "rpc.") {
			continue
		}
		msgs, _ := raw.(map[string]any)["messages"].(map[string]any)
		if len(msgs) == 0 {
			t.Errorf("channel %q has no messages", name)
		}
		for msgName, m := range msgs {
			ref, ok := m.(map[string]any)["$ref"].(string)
			if !ok {
				continue // an inline message needs no resolving
			}
			if _, has := resolveRef(t, doc, ref)["payload"]; !has {
				t.Errorf("channel %q message %q resolves to a message with no payload", name, msgName)
			}
		}
	}
}

func TestChannelGatesSeeExtensionsAndStopAtFirstError(t *testing.T) {
	api := New(DefaultConfig("T", "1"))
	type Msg struct {
		Text string `json:"text"`
	}
	RegisterChannel[Msg](api, Channel{
		ID: "room", Pattern: "room:{id}",
		Extensions:        map[string]any{"x-need": "sub"},
		PublishExtensions: map[string]any{"x-need": "pub"},
	})

	var calls []string
	api.UseChannelGate(
		func(_ context.Context, ch *ChannelInfo, action ChannelAction, params Params, info SubscribeInfo) error {
			need := ch.Extensions["x-need"]
			if action == ChannelPublish {
				need = ch.PublishExtensions["x-need"]
			}
			calls = append(calls, string(action)+":"+need.(string)+":"+params["id"]+":"+info.UserID)
			if action == ChannelPublish {
				return centrifuge.ErrorPermissionDenied
			}
			return nil
		},
		func(context.Context, *ChannelInfo, ChannelAction, Params, SubscribeInfo) error {
			calls = append(calls, "second")
			return nil
		},
	)

	ch, params, ok := findRegisteredChannel(api, "room:7")
	if !ok {
		t.Fatal("channel not found")
	}
	info := SubscribeInfo{UserID: "u1", Channel: "room:7"}

	if err := api.runChannelGates(context.Background(), ch, ChannelSubscribe, params, info); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := api.runChannelGates(context.Background(), ch, ChannelPublish, params, info); !errors.Is(err, centrifuge.ErrorPermissionDenied) {
		t.Fatalf("publish should be denied by the first gate, got %v", err)
	}
	want := "subscribe:sub:7:u1,second,publish:pub:7:u1" // the second gate must not run after a denial
	if got := strings.Join(calls, ","); got != want {
		t.Errorf("calls = %s, want %s", got, want)
	}
}

func TestPublishExtensionsAreDocumented(t *testing.T) {
	api := New(DefaultConfig("T", "1"))
	type Msg struct {
		Text string `json:"text"`
	}
	h := RegisterChannel[Msg](api, Channel{
		ID: "room", Pattern: "room",
		Extensions:        map[string]any{"x-perm": "view"},
		PublishExtensions: map[string]any{"x-perm": "post"},
	})
	AllowPublish(h, func(context.Context, Params, *Msg) (*Msg, error) { return nil, nil })

	body, err := api.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Operations map[string]map[string]any `json:"operations"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if got := doc.Operations["room-broadcast"]["x-perm"]; got != "view" {
		t.Errorf("subscribe operation x-perm = %v", got)
	}
	if got := doc.Operations["room-publish"]["x-perm"]; got != "post" {
		t.Errorf("publish operation x-perm = %v", got)
	}
}
