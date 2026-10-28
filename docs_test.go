package fuge

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func docsServer(t *testing.T, prefix string, opts ...DocsOptions) *httptest.Server {
	t.Helper()
	api := New(DefaultConfig("Test API", "1.2.3"))
	mux := http.NewServeMux()
	mux.Handle(strings.TrimSuffix(prefix, "/")+"/", api.DocsHandler(prefix, opts...))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := res.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return res, b.String()
}

func TestDocsHandlerServesUIAndSpec(t *testing.T) {
	srv := docsServer(t, "/asyncdocs", DocsOptions{WebSocketURL: `wss://x/"ws`})

	res, body := get(t, srv.URL+"/asyncdocs/")
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("index: %d %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if !strings.Contains(body, "<title>Test API Reference</title>") {
		t.Errorf("default title missing: %s", body)
	}
	if strings.Contains(body, "{{") {
		t.Errorf("unreplaced placeholder: %s", body)
	}
	if !strings.Contains(body, `content="wss://x/&#34;ws"`) {
		t.Errorf("ws url not escaped: %s", body)
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("csp = %q", csp)
	}

	for path, ct := range map[string]string{
		"/asyncdocs/app.js":        "javascript",
		"/asyncdocs/app.css":       "css",
		"/asyncdocs/asyncapi.json": "json",
		"/asyncdocs/asyncapi.yaml": "yaml",
	} {
		res, body := get(t, srv.URL+path)
		if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Type"), ct) || body == "" {
			t.Errorf("%s: %d %q", path, res.StatusCode, res.Header.Get("Content-Type"))
		}
	}
}

func TestDocsHandlerRedirectsAndRejects(t *testing.T) {
	srv := docsServer(t, "/asyncdocs")

	// Called directly (as under gorilla/mux, which routes the bare prefix
	// here), the handler itself redirects so relative asset URLs resolve.
	rec := httptest.NewRecorder()
	New(DefaultConfig("T", "1")).DocsHandler("/asyncdocs/").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/asyncdocs", nil))
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/asyncdocs/" {
		t.Errorf("redirect: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if res, _ := get(t, srv.URL+"/asyncdocs/missing"); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown path: %d", res.StatusCode)
	}
	if res, _ := get(t, srv.URL+"/asyncdocs/CLAUDE.md"); res.StatusCode != http.StatusNotFound {
		t.Errorf("non-whitelisted embedded file must 404: %d", res.StatusCode)
	}
	res, err := http.Post(srv.URL+"/asyncdocs/", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", res.StatusCode)
	}
}

func TestDocsHandlerSpecURL(t *testing.T) {
	srv := docsServer(t, "/d", DocsOptions{SpecURL: "https://specs.example.com/v1/asyncapi.json?x=1"})
	res, body := get(t, srv.URL+"/d/")
	if !strings.Contains(body, `name="fuge-spec-json" content="https://specs.example.com/v1/asyncapi.json?x=1"`) ||
		!strings.Contains(body, `name="fuge-spec-yaml" content="https://specs.example.com/v1/asyncapi.yaml?x=1"`) {
		t.Errorf("spec metas wrong: %s", body)
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "connect-src 'self' ws: wss: https://specs.example.com;") {
		t.Errorf("csp must allow spec origin: %q", csp)
	}

	// A same-origin path adds nothing to the CSP and no derivable yaml hides the link.
	srv = docsServer(t, "/d", DocsOptions{SpecURL: "/spec"})
	res, body = get(t, srv.URL+"/d/")
	if !strings.Contains(body, `name="fuge-spec-yaml" content=""`) {
		t.Errorf("yaml link should be empty: %s", body)
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "connect-src 'self' ws: wss:;") {
		t.Errorf("csp = %q", csp)
	}
}

func TestDocsHandlerRejectsBadSpecURL(t *testing.T) {
	for _, bad := range []string{"javascript:alert(1)", "ftp://x/y.json", "https:///nohost.json"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("SpecURL %q should panic", bad)
				}
			}()
			New(DefaultConfig("T", "1")).DocsHandler("/d", DocsOptions{SpecURL: bad})
		}()
	}
}

func TestServeDocsDefaults(t *testing.T) {
	api := New(DefaultConfig("Test API", "1"))
	if api.DocsRoute() != "/asyncdocs" || api.JsonRoute() != "/asyncapi.json" || api.YamlRoute() != "/asyncapi.yaml" {
		t.Fatalf("defaults: %s %s %s", api.DocsRoute(), api.JsonRoute(), api.YamlRoute())
	}
	mux := http.NewServeMux()
	api.ServeDocs(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	for _, p := range []string{"/asyncapi.json", "/asyncapi.yaml", "/asyncdocs/", "/asyncdocs/app.js"} {
		if res, _ := get(t, srv.URL+p); res.StatusCode != 200 {
			t.Errorf("%s: %d", p, res.StatusCode)
		}
	}
	// The UI must load the document from the configured route.
	_, body := get(t, srv.URL+"/asyncdocs/")
	if !strings.Contains(body, `name="fuge-spec-json" content="/asyncapi.json"`) || !strings.Contains(body, `name="fuge-spec-yaml" content="/asyncapi.yaml"`) {
		t.Errorf("ui spec urls: %s", body)
	}
}

func TestServeDocsConfigCascades(t *testing.T) {
	cfg := DefaultConfig("Test API", "1")
	cfg.DocsPath, cfg.JSONPath, cfg.YAMLPath = "ws-docs/", "/spec/ws.json", "spec/ws.yml"
	cfg.Docs = DocsOptions{Title: "Mine"}
	api := New(cfg)
	if api.DocsRoute() != "/ws-docs" || api.JsonRoute() != "/spec/ws.json" || api.YamlRoute() != "/spec/ws.yml" {
		t.Fatalf("normalized: %s %s %s", api.DocsRoute(), api.JsonRoute(), api.YamlRoute())
	}
	mux := http.NewServeMux()
	api.ServeDocs(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	for _, p := range []string{"/spec/ws.json", "/spec/ws.yml", "/ws-docs/"} {
		if res, _ := get(t, srv.URL+p); res.StatusCode != 200 {
			t.Errorf("%s: %d", p, res.StatusCode)
		}
	}
	if res, _ := get(t, srv.URL+"/asyncapi.json"); res.StatusCode != 404 {
		t.Errorf("default path must not be mounted: %d", res.StatusCode)
	}
	_, body := get(t, srv.URL+"/ws-docs/")
	for _, want := range []string{"<title>Mine</title>", `content="/spec/ws.json"`, `content="/spec/ws.yml"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %s", want, body)
		}
	}
}

func TestRoutesShape(t *testing.T) {
	api := New(DefaultConfig("T", "1"))
	var got []string
	for _, r := range api.Routes() {
		p := r.Path
		if r.Subtree {
			p += "/*"
		}
		got = append(got, p)
	}
	want := "/asyncapi.json /asyncapi.yaml /asyncdocs /asyncdocs/*"
	if strings.Join(got, " ") != want {
		t.Errorf("routes = %v, want %s", got, want)
	}
	if n := len(api.SpecRoutes()); n != 2 {
		t.Errorf("SpecRoutes = %d", n)
	}
}

func TestRouteConfigCollisionsPanic(t *testing.T) {
	for _, mut := range []func(*Config){
		func(c *Config) { c.JSONPath, c.YAMLPath = "/x", "/x" },
		func(c *Config) { c.DocsPath = "/asyncapi.json" },
		func(c *Config) { c.DocsPath = "/" },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("expected panic")
				}
			}()
			cfg := DefaultConfig("T", "1")
			mut(&cfg)
			New(cfg)
		}()
	}
}

func TestDocsCSPAllowsIdentityProviderOrigins(t *testing.T) {
	cfg := DefaultConfig("T", "1")
	cfg.SecuritySchemes["oauth"] = SecurityScheme{Type: SecurityTypeOAuth2, Flows: &OAuthFlows{AuthorizationCode: &OAuthFlow{
		AuthorizationURL: "https://login.example.com/auth", TokenURL: "https://token.example.com/t", AvailableScopes: map[string]string{},
	}}}
	cfg.SecuritySchemes["oidc"] = SecurityScheme{Type: SecurityTypeOpenIDConnect, OpenIDConnectURL: "https://oidc.example.com/.well-known/openid-configuration"}
	cfg.Docs = DocsOptions{ConnectSrc: []string{"https://extra.example.com", "javascript:x"}}
	api := New(cfg)

	rec := httptest.NewRecorder()
	api.DocsRoutes()[0].Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/asyncdocs/", nil))
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"https://token.example.com", "https://oidc.example.com", "https://extra.example.com"} {
		if !strings.Contains(csp, want) {
			t.Errorf("csp missing %s: %s", want, csp)
		}
	}
	for _, not := range []string{"login.example.com", "javascript"} {
		if strings.Contains(csp, not) {
			t.Errorf("csp must not contain %s: %s", not, csp)
		}
	}
}
