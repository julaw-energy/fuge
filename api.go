package fuge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/centrifugal/centrifuge"
)

// Config mirrors huma.Config: it embeds the document so cfg.Info, cfg.Servers,
// cfg.Components... are edited directly.
type Config struct {
	*Document

	// SecuritySchemes populate components.securitySchemes.
	SecuritySchemes map[string]SecurityScheme
	// Codec defaults to JSON.
	Codec Codec
	// ReflectOptions are passed to SchemaFromType, e.g. RequireAll().
	ReflectOptions []ReflectOption

	// DocsPath, JSONPath and YAMLPath are where ServeDocs/Routes mount the
	// interactive reference and the generated document. Empty values default
	// to "/asyncdocs", "/asyncapi.json" and "/asyncapi.yaml"; set one and
	// everything derived from it (the routes, the UI's spec links) follows.
	DocsPath, JSONPath, YAMLPath string
	// Validator, when set, is run on the generated JSON document before it is
	// served; its error fails API.JSON. Nil (the default) skips validation.
	// validate.Bytes (package fuge/validate) checks against the official
	// AsyncAPI meta-schema.
	Validator func(document []byte) error
	// Docs customizes the interactive reference (title, default websocket URL).
	// Its SpecURL/SpecYAMLURL are managed by ServeDocs and ignored there.
	Docs DocsOptions
}

// DefaultConfig returns a Config with JSON as the content type and codec.
func DefaultConfig(title, version string) Config {
	doc := NewDocument(title, version)
	doc.DefaultContentType = "application/json"
	return Config{
		Document:        doc,
		SecuritySchemes: map[string]SecurityScheme{},
		Codec:           JSON,
	}
}

// API is the single object an application holds: it dispatches RPC calls
// (embedding a *Router) and, in the same step, documents every call
// Register binds as AsyncAPI. There is no separate router type to wire up —
// New builds both together.
type API struct {
	mu          sync.Mutex
	doc         *Document
	router      *Router
	codec       Codec
	reflectOpts []ReflectOption
	schemes     map[string]SecurityScheme // == doc.Components.SecuritySchemes
	schemas     map[string]json.RawMessage
	middlewares []OpMiddleware
	methods     map[string]string // method -> operation id
	subChannels []registeredChannel
	chanGates   []ChannelGate
	routes      docRoutes
	docsOpts    DocsOptions
	validator   func([]byte) error
}

// New builds the API: a Router (configured by opts, see WithDefaultTimeout,
// WithMaxInflight, WithErrorMapper, WithLogger) plus the AsyncAPI document
// described by cfg. Use DefaultConfig to build cfg.
func New(cfg Config, opts ...Option) *API {
	doc := cfg.Document
	if doc == nil {
		doc = NewDocument("API", "0.0.0")
	}
	if doc.Components == nil {
		doc.Components = &Components{}
	}
	if doc.Components.Schemas == nil {
		doc.Components.Schemas = map[string]json.RawMessage{}
	}
	if doc.Components.Messages == nil {
		doc.Components.Messages = map[string]Message{}
	}
	if doc.Channels == nil {
		doc.Channels = map[string]SpecChannel{}
	}
	if doc.Operations == nil {
		doc.Operations = map[string]SpecOperation{}
	}
	codec := cfg.Codec
	if codec == nil {
		codec = JSON
	}
	schemes := make(map[string]SecurityScheme, len(cfg.SecuritySchemes))
	for k, v := range cfg.SecuritySchemes {
		if why := checkSecurityScheme(v); why != "" {
			panic(fmt.Sprintf("fuge: security scheme %q: %s", k, why))
		}
		schemes[k] = v
	}
	// The same map is what Components.SecuritySchemes marshals: no separate
	// patch step at JSON() time.
	doc.Components.SecuritySchemes = schemes
	return &API{
		doc:         doc,
		router:      NewRouter(opts...),
		codec:       codec,
		reflectOpts: cfg.ReflectOptions,
		schemes:     schemes,
		schemas:     doc.Components.Schemas,
		methods:     map[string]string{},
		routes:      newDocRoutes(cfg),
		docsOpts:    cfg.Docs,
		validator:   cfg.Validator,
	}
}

// Document exposes the underlying model (like huma's api.OpenAPI()).
// Security and "x-" extensions are already applied by the time Register
// returns — Document reflects the fully-formed operation immediately.
func (a *API) Document() *Document { return a.doc }

// Router exposes the underlying dispatcher, for raw/undocumented
// registration (see Handle and RouteGroup) or anything else Router offers
// that API does not forward directly.
func (a *API) Router() *Router { return a.router }

// Attach wires the API into a Centrifuge connection: Register'd RPC methods
// (via Router.Attach), plus subscribe/publish dispatch for every channel
// registered with RegisterChannel. node is required to actually publish a
// transformed message when a channel has AllowPublish set (see channels.go);
// call this from node.OnConnect, where both are already in scope together:
//
//	node.OnConnect(func(c *centrifuge.Client) { api.Attach(node, c) })
//
// A subscribe to an address matching no registered channel is rejected with
// centrifuge.ErrorUnknownChannel — Centrifuge itself has no such restriction
// by default (see RegisterChannel's doc comment), so this is what makes
// "channels must be declared on the backend" actually true.
func (a *API) Attach(node *centrifuge.Node, c *centrifuge.Client) {
	a.router.Attach(c)

	c.OnSubscribe(func(e centrifuge.SubscribeEvent, cb centrifuge.SubscribeCallback) {
		info := SubscribeInfo{UserID: c.UserID(), ClientID: c.ID(), Channel: e.Channel}
		for _, ch := range a.subChannels {
			params, ok := ch.match(e.Channel)
			if !ok {
				continue
			}
			if err := a.runChannelGates(c.Context(), ch, ChannelSubscribe, params, info); err != nil {
				cb(centrifuge.SubscribeReply{}, a.mapChannelError(err))
				return
			}
			if err := ch.authorizeSubscribe(c.Context(), params, info); err != nil {
				cb(centrifuge.SubscribeReply{}, a.mapChannelError(err))
				return
			}
			cb(centrifuge.SubscribeReply{}, nil)
			return
		}
		cb(centrifuge.SubscribeReply{}, centrifuge.ErrorUnknownChannel)
	})

	c.OnPublish(func(e centrifuge.PublishEvent, cb centrifuge.PublishCallback) {
		for _, ch := range a.subChannels {
			params, ok := ch.match(e.Channel)
			if !ok {
				continue
			}
			info := SubscribeInfo{UserID: c.UserID(), ClientID: c.ID(), Channel: e.Channel}
			if err := a.runChannelGates(c.Context(), ch, ChannelPublish, params, info); err != nil {
				cb(centrifuge.PublishReply{}, a.mapChannelError(err))
				return
			}
			if !ch.canPublish() {
				cb(centrifuge.PublishReply{}, centrifuge.ErrorPermissionDenied)
				return
			}
			out, err := ch.dispatchPublish(c.Context(), params, e.Data)
			if err != nil {
				cb(centrifuge.PublishReply{}, a.mapChannelError(err))
				return
			}
			if out == nil {
				// Accepted, but nothing to broadcast — still need a non-nil
				// Result so centrifuge doesn't fall back to auto-publishing
				// the client's raw (unvalidated, untransformed) bytes.
				cb(centrifuge.PublishReply{Result: &centrifuge.PublishResult{}}, nil)
				return
			}
			if _, err := node.Publish(e.Channel, out); err != nil {
				cb(centrifuge.PublishReply{}, a.mapChannelError(err))
				return
			}
			cb(centrifuge.PublishReply{Result: &centrifuge.PublishResult{}}, nil)
			return
		}
		cb(centrifuge.PublishReply{}, centrifuge.ErrorUnknownChannel)
	})
}

// mapChannelError adapts a channel handler's error the same way Router maps
// an RPC handler's error — a *fuge.Error or *centrifuge.Error passes through
// with its real code; anything else becomes an opaque internal error, logged
// server-side but never leaked to the client.
func (a *API) mapChannelError(err error) error {
	return a.router.mapError(CallInfo{}, err)
}

// Call runs a registered method in-process, documented or not — see Router.Call.
func (a *API) Call(ctx context.Context, method string, data []byte) ([]byte, error) {
	return a.router.Call(ctx, method, data)
}

// Use adds router-level middleware (see Router.Use), applied to every call —
// documented Register operations included, since they dispatch through the
// same Router underneath. For middleware that should see the Operation being
// called (e.g. to read op.Security), use UseMiddleware instead.
func (a *API) Use(mw ...Middleware) { a.router.Use(mw...) }

// Handle registers a raw, undocumented method directly on the Router — for
// anything that should not appear in the AsyncAPI document. Prefer Register
// for anything client-facing.
func (a *API) Handle(method string, h Handler, opts ...RouteOption) {
	a.router.Handle(method, h, opts...)
}

// RouteGroup groups raw, undocumented methods under a prefix — see
// Router.RouteGroup. For documented operations, use Group instead.
func (a *API) RouteGroup(prefix string, mw ...Middleware) *RouteGroup {
	return a.router.RouteGroup(prefix, mw...)
}

// UseMiddleware adds middleware applied to every Register'd operation. It
// must be called before the first Register, otherwise earlier operations
// would silently miss it.
func (a *API) UseMiddleware(mw ...OpMiddleware) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.methods) > 0 {
		panic("fuge: UseMiddleware must be called before registering operations")
	}
	a.middlewares = append(a.middlewares, mw...)
}

// UseChannelGate adds gates that authorize every client subscribe and publish
// before the channel's own handlers run; see [ChannelGate]. Unlike
// UseMiddleware it may be called at any time.
func (a *API) UseChannelGate(g ...ChannelGate) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.chanGates = append(a.chanGates, g...)
}

// runChannelGates returns the first gate error for the action, or nil.
func (a *API) runChannelGates(ctx context.Context, ch registeredChannel, action ChannelAction, params Params, info SubscribeInfo) error {
	a.mu.Lock()
	gates := a.chanGates
	a.mu.Unlock()
	for _, g := range gates {
		if err := g(ctx, ch.channelInfo(), action, params, info); err != nil {
			return err
		}
	}
	return nil
}

// Registrar is implemented by *API and *Group so Register works on both.
type Registrar interface {
	api() *API
	prepare(op Operation) Operation
}

func (a *API) api() *API                      { return a }
func (a *API) prepare(op Operation) Operation { return op }

// Group prefixes method names and tags for the operations registered on it.
// Unlike RouteGroup, the prefix is visible to the documentation.
type Group struct {
	parent Registrar
	prefix string
	tags   []string
}

func (a *API) Group(prefix string, tags ...string) *Group {
	return newGroup(a, prefix, tags)
}

func (g *Group) Group(prefix string, tags ...string) *Group {
	return newGroup(g, prefix, tags)
}

func newGroup(parent Registrar, prefix string, tags []string) *Group {
	if err := validateName(prefix); err != nil {
		panic(fmt.Sprintf("fuge: invalid group prefix %q: %v", prefix, err))
	}
	return &Group{parent: parent, prefix: prefix, tags: tags}
}

func (g *Group) api() *API { return g.parent.api() }

func (g *Group) prepare(op Operation) Operation {
	op.Method = g.prefix + "." + op.Method
	op.Tags = append(append([]string(nil), g.tags...), op.Tags...)
	return g.parent.prepare(op)
}

// validateName keeps names safe as JSON-pointer segments and component keys.
// Used for both documented (Group/Register) and raw (RouteGroup/Handle) names.
func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("empty")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_', r == ':':
		default:
			return fmt.Errorf("character %q not allowed (use letters, digits and . - _ :)", r)
		}
	}
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") || strings.Contains(name, "..") {
		return fmt.Errorf("empty segment")
	}
	return nil
}
