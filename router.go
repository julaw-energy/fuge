package fuge

import (
	"context"
	"fmt"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/centrifugal/centrifuge"
)

type Request struct {
	Method string
	Data   []byte
}

type Response struct {
	Data []byte
}

type Handler func(ctx context.Context, req Request) (Response, error)

// Middleware wraps a Handler. Middleware attached first runs first (outermost).
type Middleware func(Handler) Handler

// ---- options ---------------------------------------------------------------

type Option func(*config)

type config struct {
	logger         Logger
	defaultTimeout time.Duration
	maxInflight    int
	mapper         ErrorMapper
}

// WithDefaultTimeout applies a deadline to every route (override per route
// with WithTimeout). Zero means no deadline.
func WithDefaultTimeout(d time.Duration) Option { return func(c *config) { c.defaultTimeout = d } }

// WithMaxInflight bounds concurrently running handlers across the router.
// Excess calls are rejected immediately with ErrorLimitExceeded.
func WithMaxInflight(n int) Option { return func(c *config) { c.maxInflight = n } }

func WithErrorMapper(m ErrorMapper) Option { return func(c *config) { c.mapper = m } }

func WithLogger(l Logger) Option { return func(c *config) { c.logger = l } }

type RouteOption func(*routeConfig)

type routeConfig struct {
	mw         []Middleware
	timeout    time.Duration
	timeoutSet bool
}

// WithTimeout overrides the default timeout for one route (0 disables it).
func WithTimeout(d time.Duration) RouteOption {
	return func(c *routeConfig) { c.timeout, c.timeoutSet = d, true }
}

// WithMiddleware adds middleware that applies to this route only, innermost.
func WithMiddleware(mw ...Middleware) RouteOption {
	return func(c *routeConfig) { c.mw = append(c.mw, mw...) }
}

// ---- router ----------------------------------------------------------------

type route struct {
	method  string
	handler Handler
	group   *RouteGroup
	cfg     routeConfig
}

type Router struct {
	cfg  config
	root *RouteGroup
	sem  chan struct{}

	mu     sync.Mutex
	frozen bool
	routes []*route
	seen   map[string]struct{}

	once  sync.Once
	table map[string]Handler // immutable after compile
}

func NewRouter(opts ...Option) *Router {
	r := &Router{seen: make(map[string]struct{})}
	r.cfg.logger = defaultLogger()
	for _, o := range opts {
		o(&r.cfg)
	}
	if r.cfg.maxInflight > 0 {
		r.sem = make(chan struct{}, r.cfg.maxInflight)
	}
	r.root = &RouteGroup{router: r}
	return r
}

// Use adds router-wide middleware. Order of Use vs Handle calls does not
// matter: the final chain is built once, when the router is first used.
func (r *Router) Use(mw ...Middleware) { r.root.Use(mw...) }
func (r *Router) RouteGroup(prefix string, mw ...Middleware) *RouteGroup {
	return r.root.RouteGroup(prefix, mw...)
}
func (r *Router) Handle(method string, h Handler, opts ...RouteOption) {
	r.root.Handle(method, h, opts...)
}
func (r *Router) register(m string, h Handler, o []RouteOption) { r.root.register(m, h, o) }

// Routes lists registered method names (handy for startup logs / docs).
func (r *Router) Routes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.routes))
	for _, rt := range r.routes {
		out = append(out, rt.method)
	}
	sort.Strings(out)
	return out
}

func (r *Router) mustNotBeFrozen() {
	if r.frozen {
		panic("fuge: routes and middleware must be registered before the router serves its first call")
	}
}

func (r *Router) compile() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frozen = true

	table := make(map[string]Handler, len(r.routes))
	for _, rt := range r.routes {
		// Collect middleware root -> leaf group, then route-level.
		var groups []*RouteGroup
		for g := rt.group; g != nil; g = g.parent {
			groups = append(groups, g)
		}
		var stack []Middleware
		for i := len(groups) - 1; i >= 0; i-- {
			stack = append(stack, groups[i].mw...)
		}
		stack = append(stack, rt.cfg.mw...)

		timeout := r.cfg.defaultTimeout
		if rt.cfg.timeoutSet {
			timeout = rt.cfg.timeout
		}
		if timeout > 0 {
			// Outside user middleware so they observe the deadline too.
			stack = append([]Middleware{withTimeout(timeout)}, stack...)
		}
		table[rt.method] = chain(rt.handler, stack...)
	}
	r.table = table
}

func (r *Router) lookup(method string) (Handler, bool) {
	r.once.Do(r.compile)
	h, ok := r.table[method]
	return h, ok
}

// ---- groups ----------------------------------------------------------------

type RouteGroup struct {
	router *Router
	parent *RouteGroup
	prefix string
	mw     []Middleware
}

func (g *RouteGroup) Use(mw ...Middleware) {
	g.router.mu.Lock()
	defer g.router.mu.Unlock()
	g.router.mustNotBeFrozen()
	g.mw = append(g.mw, mw...)
}

// RouteGroup creates a sub-group; names become "<parent>.<prefix>.<method>".
func (g *RouteGroup) RouteGroup(prefix string, mw ...Middleware) *RouteGroup {
	if err := validateName(prefix); err != nil {
		panic(fmt.Sprintf("fuge: invalid group prefix %q: %v", prefix, err))
	}
	return &RouteGroup{router: g.router, parent: g, prefix: prefix, mw: mw}
}

func (g *RouteGroup) Handle(method string, h Handler, opts ...RouteOption) {
	g.register(method, h, opts)
}

func (g *RouteGroup) register(method string, h Handler, opts []RouteOption) {
	if h == nil {
		panic("fuge: nil handler for " + method)
	}
	if err := validateName(method); err != nil {
		panic(fmt.Sprintf("fuge: invalid method %q: %v", method, err))
	}
	full := g.fullName(method)

	var cfg routeConfig
	for _, o := range opts {
		o(&cfg)
	}

	r := g.router
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mustNotBeFrozen()
	if _, dup := r.seen[full]; dup {
		panic("fuge: duplicate method: " + full)
	}
	r.seen[full] = struct{}{}
	r.routes = append(r.routes, &route{method: full, handler: h, group: g, cfg: cfg})
}

func (g *RouteGroup) fullName(method string) string {
	var parts []string
	for x := g; x != nil; x = x.parent {
		if x.prefix != "" {
			parts = append([]string{x.prefix}, parts...)
		}
	}
	return strings.Join(append(parts, method), ".")
}

// ---- dispatch --------------------------------------------------------------

// Attach wires the router into a client. Call it from node.OnConnect:
//
//	node.OnConnect(func(c *centrifuge.Client) { router.Attach(c) })
func (r *Router) Attach(c *centrifuge.Client) {
	r.once.Do(r.compile) // fail fast on misconfiguration, not on the first call
	c.OnRPC(func(e centrifuge.RPCEvent, cb centrifuge.RPCCallback) {
		r.serve(c, e, cb)
	})
}

// conn is the slice of *centrifuge.Client that serve needs. Keeping it as an
// interface lets tests drive serve with a fake instead of a real connection.
type conn interface {
	ID() string
	UserID() string
	Context() context.Context
}

func (r *Router) serve(c conn, e centrifuge.RPCEvent, cb centrifuge.RPCCallback) {
	h, ok := r.lookup(e.Method)
	if !ok {
		cb(centrifuge.RPCReply{}, centrifuge.ErrorMethodNotFound)
		return
	}
	if r.sem != nil {
		select {
		case r.sem <- struct{}{}:
		default:
			cb(centrifuge.RPCReply{}, centrifuge.ErrorLimitExceeded)
			return
		}
	}

	info := CallInfo{
		Method:    e.Method,
		RequestID: newRequestID(),
		ClientID:  c.ID(),
		UserID:    c.UserID(),
		Received:  time.Now(),
	}
	if cl, ok := c.(*centrifuge.Client); ok {
		info.Client = cl
	}
	// Derived from the client's context: cancelled when the connection closes.
	ctx := withInfo(c.Context(), info)

	// Centrifuge runs OnRPC on the connection's read loop, so slow handlers
	// would stall the client's other messages. Hand off to a goroutine.
	go func() {
		if r.sem != nil {
			defer func() { <-r.sem }()
		}
		res, err := r.invoke(ctx, h, Request{Method: e.Method, Data: e.Data})
		if err != nil {
			cb(centrifuge.RPCReply{}, r.mapError(info, err))
			return
		}
		cb(centrifuge.RPCReply{Data: res.Data}, nil)
	}()
}

// Call runs a route in-process without Centrifuge: ideal for unit tests.
// Returned errors are the raw handler errors (not mapped to wire errors).
func (r *Router) Call(ctx context.Context, method string, data []byte) ([]byte, error) {
	h, ok := r.lookup(method)
	if !ok {
		return nil, centrifuge.ErrorMethodNotFound
	}
	if _, has := InfoFrom(ctx); !has {
		ctx = withInfo(ctx, CallInfo{Method: method, RequestID: newRequestID(), Received: time.Now()})
	}
	res, err := r.invoke(ctx, h, Request{Method: method, Data: data})
	if err != nil {
		return nil, err
	}
	return res.Data, nil
}

// invoke is the panic safety net: a panicking handler must never take down the process.
func (r *Router) invoke(ctx context.Context, h Handler, req Request) (res Response, err error) {
	defer func() {
		if p := recover(); p != nil {
			r.cfg.logger.Error("fuge: panic", "method", req.Method, "panic", p, "stack", string(debug.Stack()))
			res, err = Response{}, fmt.Errorf("fuge: panic in %s: %v", req.Method, p)
		}
	}()
	return h(ctx, req)
}
