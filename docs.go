package fuge

import (
	"embed"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

//go:embed docsui
var docsUIFS embed.FS

// DocsOptions customizes [API.DocsHandler]. The zero value is valid.
type DocsOptions struct {
	// Title is the browser tab title. Defaults to "<info.title> Reference".
	Title string
	// WebSocketURL pre-fills the connection bar of the "Try it" client, e.g.
	// "wss://api.example.com/connection/websocket". When empty the UI derives
	// it from the page's own origin and the document's first server pathname.
	// A URL the user typed in earlier (kept in their browser) takes precedence.
	WebSocketURL string
	// SpecURL makes the UI load the AsyncAPI document from this URL instead of
	// the copy this handler serves: a path ("/asyncapi.json") or an absolute
	// http(s) URL, whose origin is then allowed by the page's CSP (the server
	// must permit cross-origin reads, e.g. CORS). The YAML link is derived by
	// swapping a ".json" suffix for ".yaml", and hidden when there is none.
	// Any other scheme panics at construction, like a bad http.ServeMux pattern.
	SpecURL string
	// SpecYAMLURL is the YAML counterpart of SpecURL. When empty it is derived
	// from SpecURL as described there. Ignored without SpecURL.
	SpecYAMLURL string
	// ConnectSrc lists extra origins ("https://idp.example.com") the page may
	// fetch from. Origins of the security schemes' token and discovery URLs
	// and of SpecURL are added automatically; list others here, e.g. the
	// authorization/token endpoints an openIdConnect discovery document
	// points at when they live on a different host.
	ConnectSrc []string
}

// DocsHandler serves an interactive AsyncAPI reference: the rendered
// operations and schemas plus a "Try it" client that connects to the
// websocket, calls RPC methods and subscribes to channels. It is
// self-contained (no CDN, no build step) and also serves the generated
// document at <prefix>/asyncapi.json and <prefix>/asyncapi.yaml.
//
// prefix is the path the handler is mounted at, without a trailing slash
// (e.g. "/asyncdocs"). Mount it for both the prefix and everything below it:
//
//	h := api.DocsHandler("/asyncdocs")
//	mux.Handle("/asyncdocs/", h) // net/http also redirects "/asyncdocs"
//
// or with gorilla/mux: router.Handle("/asyncdocs", h) and
// router.PathPrefix("/asyncdocs/").Handler(h).
//
// Like the document handlers, the document is generated on first request,
// so register all operations before serving.
func (a *API) DocsHandler(prefix string, opts ...DocsOptions) http.Handler {
	var o DocsOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	prefix = "/" + strings.Trim(prefix, "/")
	if prefix == "/" {
		prefix = ""
	}

	assets, err := fs.Sub(docsUIFS, "docsui")
	if err != nil {
		panic(err) // static embedded path; cannot fail at runtime
	}
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		panic(err)
	}
	title := o.Title
	if title == "" {
		title = a.doc.Info.Title + " Reference"
	}
	specJSON, specYAML, specOrigin := "asyncapi.json", "asyncapi.yaml", ""
	if o.SpecURL != "" {
		specJSON, specYAML, specOrigin = customSpec(o.SpecURL, o.SpecYAMLURL)
	}
	page := strings.NewReplacer(
		"{{TITLE}}", html.EscapeString(title),
		"{{SPEC_JSON}}", html.EscapeString(specJSON),
		"{{SPEC_YAML}}", html.EscapeString(specYAML),
		"{{WS_URL}}", html.EscapeString(o.WebSocketURL),
	).Replace(string(index))

	// 'self' covers the UI's own assets and the spec; ws:/wss: lets the
	// "Try it" client reach any websocket endpoint.
	csp := strings.Join([]string{
		"default-src 'none'",
		"script-src 'self'",
		"style-src 'self'",
		"img-src 'self' data:",
		strings.TrimSpace("connect-src 'self' ws: wss: " + strings.Join(connectOrigins(specOrigin, o.ConnectSrc, a.schemes), " ")),
		"base-uri 'none'",
		"form-action 'none'",
		"frame-ancestors 'none'",
	}, "; ")

	files := http.FileServerFS(assets)
	jsonDoc, yamlDoc := a.JsonDocHandler(), a.YamlDocHandler()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("X-Content-Type-Options", "nosniff")

		rest, ok := strings.CutPrefix(r.URL.Path, prefix)
		if !ok {
			http.NotFound(w, r)
			return
		}
		switch rest {
		case "": // "/asyncdocs" → "/asyncdocs/" so the page's relative URLs resolve
			http.Redirect(w, r, prefix+"/", http.StatusMovedPermanently)
		case "/", "/index.html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(page))
		case "/asyncapi.json":
			jsonDoc.ServeHTTP(w, r)
		case "/asyncapi.yaml":
			yamlDoc.ServeHTTP(w, r)
		case "/app.js", "/auth.js", "/app.css":
			w.Header().Set("Cache-Control", "no-cache")
			r2 := r.Clone(r.Context())
			r2.URL.Path = rest
			files.ServeHTTP(w, r2)
		default:
			http.NotFound(w, r)
		}
	})
}

// customSpec validates DocsOptions.SpecURL/SpecYAMLURL and returns the JSON
// URL, the YAML URL ("" if none) and the origins to add to the CSP (space
// separated; "" for same-origin paths).
func customSpec(rawJSON, rawYAML string) (jsonURL, yamlURL, origins string) {
	seen := map[string]bool{}
	check := func(raw string) {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https") || (u.Scheme != "" && u.Host == "") {
			panic(fmt.Sprintf("fuge: spec URL %q must be a path or an absolute http(s) URL", raw))
		}
		if o := u.Scheme + "://" + u.Host; u.Scheme != "" && !seen[o] {
			seen[o] = true
			origins = strings.TrimSpace(origins + " " + o)
		}
	}
	check(rawJSON)
	yamlURL = rawYAML
	if yamlURL == "" {
		u, _ := url.Parse(rawJSON)
		if base, ok := strings.CutSuffix(u.Path, ".json"); ok {
			y := *u
			y.Path = base + ".yaml"
			yamlURL = y.String()
		}
	} else {
		check(yamlURL)
	}
	return rawJSON, yamlURL, origins
}

// docRoutes holds the resolved mount paths (see Config.DocsPath).
type docRoutes struct{ docs, json, yaml string }

func newDocRoutes(cfg Config) docRoutes {
	clean := func(p, def string) string {
		if p = strings.TrimSpace(p); p == "" {
			p = def
		}
		return "/" + strings.Trim(p, "/")
	}
	r := docRoutes{
		docs: clean(cfg.DocsPath, "/asyncdocs"),
		json: clean(cfg.JSONPath, "/asyncapi.json"),
		yaml: clean(cfg.YAMLPath, "/asyncapi.yaml"),
	}
	if r.json == r.yaml || r.docs == "/" || r.docs == r.json || r.docs == r.yaml {
		panic(fmt.Sprintf("fuge: DocsPath, JSONPath and YAMLPath must be distinct and not \"/\": %+v", r))
	}
	return r
}

// DocsRoute, JsonRoute and YamlRoute return the configured mount paths
// (Config.DocsPath, JSONPath, YAMLPath; defaults "/asyncdocs",
// "/asyncapi.json", "/asyncapi.yaml"). Use them wherever you need the URL, so
// changing the config is the only edit.
func (a *API) DocsRoute() string { return a.routes.docs }
func (a *API) JsonRoute() string { return a.routes.json }
func (a *API) YamlRoute() string { return a.routes.yaml }

// Route is one path to mount. Subtree means "this path and everything below
// it" (a prefix match); otherwise it is an exact match.
type Route struct {
	Path    string
	Subtree bool
	Handler http.Handler
}

// Mux is the part of a router ServeDocs needs. *http.ServeMux and chi's
// routers satisfy it; for routers whose Handle has a different signature
// (gorilla/mux) loop over [API.Routes] instead.
type Mux interface {
	Handle(pattern string, h http.Handler)
}

// SpecRoutes returns the routes serving the document: JsonRoute and YamlRoute.
func (a *API) SpecRoutes() []Route {
	return []Route{
		{Path: a.routes.json, Handler: a.JsonDocHandler()},
		{Path: a.routes.yaml, Handler: a.YamlDocHandler()},
	}
}

// DocsRoutes returns the routes serving the interactive reference: DocsRoute
// exactly and below. It loads the document from JsonRoute/YamlRoute, so mount
// SpecRoutes too (Routes returns both).
func (a *API) DocsRoutes() []Route {
	opts := a.docsOpts
	opts.SpecURL, opts.SpecYAMLURL = a.routes.json, a.routes.yaml
	h := a.DocsHandler(a.routes.docs, opts)
	return []Route{{Path: a.routes.docs, Handler: h}, {Path: a.routes.docs, Subtree: true, Handler: h}}
}

// Routes returns SpecRoutes and DocsRoutes.
func (a *API) Routes() []Route { return append(a.SpecRoutes(), a.DocsRoutes()...) }

// ServeSpec mounts only the document (JsonRoute, YamlRoute) on m.
func (a *API) ServeSpec(m Mux) { mount(m, a.SpecRoutes()) }

// ServeDocs mounts the document and the interactive reference on m at the
// configured paths — the one-liner for the common case:
//
//	api.ServeDocs(http.DefaultServeMux) // /asyncapi.json, /asyncapi.yaml, /asyncdocs/
func (a *API) ServeDocs(m Mux) { mount(m, a.Routes()) }

func mount(m Mux, routes []Route) {
	for _, r := range routes {
		p := r.Path
		if r.Subtree {
			p += "/" // net/http (and chi) subtree pattern
		}
		m.Handle(p, r.Handler)
	}
}

// connectOrigins collects the extra connect-src origins: the custom spec's,
// the caller's, and those of every oauth2/openIdConnect endpoint the browser
// calls itself (token, refresh, discovery). Authorization URLs are navigated
// to in a popup, which connect-src doesn't govern.
func connectOrigins(specOrigins string, extra []string, schemes map[string]SecurityScheme) []string {
	var out []string
	seen := map[string]bool{}
	add := func(raw string) {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return
		}
		if o := u.Scheme + "://" + u.Host; !seen[o] {
			seen[o] = true
			out = append(out, o)
		}
	}
	for _, o := range strings.Fields(specOrigins) {
		add(o)
	}
	for _, o := range extra {
		add(o)
	}
	names := make([]string, 0, len(schemes))
	for n := range schemes {
		names = append(names, n)
	}
	sort.Strings(names) // deterministic CSP
	for _, n := range names {
		s := schemes[n]
		add(s.OpenIDConnectURL)
		if s.Flows == nil {
			continue
		}
		for _, f := range []*OAuthFlow{s.Flows.Implicit, s.Flows.Password, s.Flows.ClientCredentials, s.Flows.AuthorizationCode} {
			if f != nil {
				add(f.TokenURL)
				add(f.RefreshURL)
			}
		}
	}
	return out
}
