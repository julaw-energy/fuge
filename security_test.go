package fuge

import (
	"encoding/json"
	"testing"
)

func TestSecuritySchemesAndOperationSecurity(t *testing.T) {
	doc := NewDocument("Secured API", "1.0.0")
	doc.Channels = map[string]SpecChannel{
		"ping": {Address: "ping"},
	}
	doc.Operations = map[string]SpecOperation{
		"ping": {
			Action:   ActionReceive,
			Channel:  Ref("#/channels/ping"),
			Security: []RefObj{Ref("#/components/securitySchemes/bearer")},
		},
	}
	doc.Components = &Components{
		SecuritySchemes: map[string]SecurityScheme{
			"bearer": {Type: SecurityTypeHTTP, Scheme: "bearer", BearerFormat: "JWT"},
			"oauth": {
				Type: SecurityTypeOAuth2,
				Flows: &OAuthFlows{
					ClientCredentials: &OAuthFlow{
						TokenURL:        "https://auth.example.com/token",
						AvailableScopes: map[string]string{"read": "Read access"},
					},
				},
			},
		},
	}

	got, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatal(err)
	}

	var ops map[string]map[string]json.RawMessage
	if err := json.Unmarshal(m["operations"], &ops); err != nil {
		t.Fatal(err)
	}
	if string(ops["ping"]["security"]) != `[{"$ref":"#/components/securitySchemes/bearer"}]` {
		t.Errorf("operation security: %s", ops["ping"]["security"])
	}

	var comps struct {
		SecuritySchemes map[string]json.RawMessage `json:"securitySchemes"`
	}
	if err := json.Unmarshal(m["components"], &comps); err != nil {
		t.Fatal(err)
	}
	if string(comps.SecuritySchemes["bearer"]) != `{"type":"http","scheme":"bearer","bearerFormat":"JWT"}` {
		t.Errorf("bearer scheme: %s", comps.SecuritySchemes["bearer"])
	}
}

func TestExtensionsRoundTripAndOrdering(t *testing.T) {
	op := SpecOperation{
		Action:  ActionReceive,
		Channel: Ref("#/channels/x"),
		Extensions: Extensions{
			"x-permissions": []string{"location.create"},
			"x-internal":    true,
		},
	}

	got, err := json.Marshal(op)
	if err != nil {
		t.Fatal(err)
	}
	// Declared fields keep their struct order; extensions are appended,
	// sorted by key, after them — never interleaved.
	want := `{"action":"receive","channel":{"$ref":"#/channels/x"},"x-internal":true,"x-permissions":["location.create"]}`
	if string(got) != want {
		t.Fatalf("got:  %s\nwant: %s", got, want)
	}

	var back SpecOperation
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatal(err)
	}
	if back.Action != op.Action || back.Channel != op.Channel {
		t.Fatalf("base fields lost: %+v", back)
	}
	if len(back.Extensions) != 2 || back.Extensions["x-internal"] != true {
		t.Fatalf("extensions lost: %+v", back.Extensions)
	}
	if names, ok := back.Extensions["x-permissions"].([]any); !ok || len(names) != 1 || names[0] != "location.create" {
		t.Fatalf("x-permissions: %#v", back.Extensions["x-permissions"])
	}
}

func TestExtensionsNoOpWhenEmpty(t *testing.T) {
	// A type with the Extensions field added must marshal byte-identically
	// to before that field existed, whenever no extension is set — this is
	// what keeps existing committed documents (see Check) stable.
	withField, err := json.Marshal(Server{Host: "h", Protocol: "wss"})
	if err != nil {
		t.Fatal(err)
	}
	if string(withField) != `{"host":"h","protocol":"wss"}` {
		t.Fatalf("got %s", withField)
	}
}

func TestInvalidExtensionKeyErrors(t *testing.T) {
	_, err := json.Marshal(Server{
		Host: "h", Protocol: "wss",
		Extensions: Extensions{"not-x-prefixed": 1},
	})
	if err == nil {
		t.Fatal("expected an error for a non-\"x-\" extension key")
	}
}

func TestDocumentUnmarshalRoundTrip(t *testing.T) {
	src := `{
		"asyncapi": "3.1.0",
		"info": {"title": "T", "version": "1.0.0"},
		"x-team": "platform",
		"components": {
			"securitySchemes": {"bearer": {"type": "http", "scheme": "bearer"}},
			"x-note": "internal"
		}
	}`

	var doc Document
	if err := json.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Extensions["x-team"] != "platform" {
		t.Fatalf("document extension lost: %+v", doc.Extensions)
	}
	if doc.Components.Extensions["x-note"] != "internal" {
		t.Fatalf("components extension lost: %+v", doc.Components.Extensions)
	}
	if doc.Components.SecuritySchemes["bearer"].Scheme != "bearer" {
		t.Fatalf("security scheme lost: %+v", doc.Components.SecuritySchemes)
	}

	// And it survives a marshal/unmarshal/marshal cycle unchanged.
	out1, err := json.Marshal(&doc)
	if err != nil {
		t.Fatal(err)
	}
	var doc2 Document
	if err := json.Unmarshal(out1, &doc2); err != nil {
		t.Fatal(err)
	}
	out2, err := json.Marshal(&doc2)
	if err != nil {
		t.Fatal(err)
	}
	if string(out1) != string(out2) {
		t.Fatalf("not stable across a round trip:\n%s\n%s", out1, out2)
	}
}

func TestNewRejectsUnsupportedSecuritySchemes(t *testing.T) {
	bad := map[string]SecurityScheme{
		"x509":        {Type: "X509"},
		"basic":       {Type: SecurityTypeHTTP, Scheme: "basic"},
		"header-key":  {Type: SecurityTypeHTTPAPIKey, Name: "k", In: "header"},
		"nameless":    {Type: SecurityTypeHTTPAPIKey, In: "query"},
		"no-flows":    {Type: SecurityTypeOAuth2},
		"no-oidc-url": {Type: SecurityTypeOpenIDConnect},
		"bad-pkce":    {Type: SecurityTypeHTTP, Scheme: "bearer", Extensions: Extensions{ExtUsePKCE: "md5"}},
		"bad-cred":    {Type: SecurityTypeHTTP, Scheme: "bearer", Extensions: Extensions{ExtCredentialsLocation: "cookie"}},
	}
	for name, s := range bad {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: expected panic", name)
				}
			}()
			cfg := DefaultConfig("T", "1")
			cfg.SecuritySchemes[name] = s
			New(cfg)
		}()
	}

	cfg := DefaultConfig("T", "1")
	cfg.SecuritySchemes["bearer"] = SecurityScheme{Type: SecurityTypeHTTP, Scheme: "Bearer"}
	cfg.SecuritySchemes["q"] = SecurityScheme{Type: SecurityTypeHTTPAPIKey, Name: "key", In: "query"}
	cfg.SecuritySchemes["c"] = SecurityScheme{Type: SecurityTypeHTTPAPIKey, Name: "sid", In: "cookie"}
	cfg.SecuritySchemes["oidc"] = SecurityScheme{Type: SecurityTypeOpenIDConnect, OpenIDConnectURL: "https://idp/.well-known/openid-configuration"}
	New(cfg) // must not panic
}

func TestOAuthExtensions(t *testing.T) {
	if len(OAuthExtensions(OAuthOptions{})) != 0 {
		t.Error("zero options must yield no extensions")
	}
	cfg := DefaultConfig("T", "1")
	cfg.SecuritySchemes["oauth"] = SecurityScheme{
		Type: SecurityTypeOAuth2,
		Flows: &OAuthFlows{AuthorizationCode: &OAuthFlow{
			AuthorizationURL: "https://idp.example.com/auth", TokenURL: "https://idp.example.com/token",
			AvailableScopes: map[string]string{"openid": "OIDC"},
		}},
		Extensions: OAuthExtensions(OAuthOptions{
			ClientID: "web", UsePKCE: "S256", DefaultScopes: []string{"openid"},
			AuthorizeParams: map[string]string{"audience": "api"},
		}),
	}
	body, err := New(cfg).JSON()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			SecuritySchemes map[string]map[string]any `json:"securitySchemes"`
		} `json:"components"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	s := doc.Components.SecuritySchemes["oauth"]
	if s[ExtClientID] != "web" || s[ExtUsePKCE] != "S256" || s["x-fuge-default-scopes"].([]any)[0] != "openid" ||
		s[ExtAuthorizeParams].(map[string]any)["audience"] != "api" {
		t.Errorf("extensions not emitted: %v", s)
	}
}
