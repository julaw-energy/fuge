package fuge

import (
	"fmt"
	"strings"
)

// SecurityScheme defines an authentication or authorization mechanism that
// operations can require. Define schemes under [Components].SecuritySchemes
// and reference them from SpecOperation.Security with
// [Ref]("#/components/securitySchemes/<name>") — AsyncAPI's operation
// security list is a flat "OR" of scheme references (any one satisfies the
// operation), unlike OpenAPI's map-of-scheme-to-scopes; there is no way to
// require two schemes at once (an "AND"), and this package does not model an
// inline (non-referenced) SecurityScheme literal in that list.
//
// fuge supports only the token-based schemes a browser WebSocket can carry,
// and [New] panics on any other Type:
//
//	http          — Scheme must be "bearer"; BearerFormat optional ("JWT")
//	httpApiKey    — Name, In: "query" or "cookie" (not "header": browsers cannot
//	                set headers on a WebSocket)
//	oauth2        — Flows (any of the four), and optionally Scopes
//	openIdConnect — OpenIDConnectURL (the .well-known/openid-configuration URL)
//
// For oauth2 and openIdConnect, the interactive docs UI can run the flows and
// pre-fills them from the scheme; tune that with [OAuthExtensions] ("x-fuge-*"
// keys on Extensions). the meta-schema (see fuge/validate) catches combinations the
// specification itself doesn't allow.
type SecurityScheme struct {
	Type             string      `json:"type"`
	Description      string      `json:"description,omitempty"`
	Name             string      `json:"name,omitempty"`
	In               string      `json:"in,omitempty"`
	Scheme           string      `json:"scheme,omitempty"`
	BearerFormat     string      `json:"bearerFormat,omitempty"`
	Flows            *OAuthFlows `json:"flows,omitempty"`
	OpenIDConnectURL string      `json:"openIdConnectUrl,omitempty"`
	Scopes           []string    `json:"scopes,omitempty"`

	// Extensions holds "x-" values on this object. See [Extensions].
	Extensions Extensions `json:"-"`
}

// Security scheme types fuge supports.
const (
	SecurityTypeHTTP          = "http"
	SecurityTypeHTTPAPIKey    = "httpApiKey"
	SecurityTypeOAuth2        = "oauth2"
	SecurityTypeOpenIDConnect = "openIdConnect"
)

// checkSecurityScheme reports why s is not usable with fuge ("" if fine).
func checkSecurityScheme(s SecurityScheme) string {
	switch s.Type {
	case SecurityTypeHTTP:
		if !strings.EqualFold(s.Scheme, "bearer") {
			return fmt.Sprintf("type http supports only scheme \"bearer\", got %q", s.Scheme)
		}
	case SecurityTypeHTTPAPIKey:
		if s.Name == "" {
			return "type httpApiKey requires Name"
		}
		if s.In != "query" && s.In != "cookie" {
			return fmt.Sprintf("type httpApiKey supports In \"query\" or \"cookie\" (browsers cannot set websocket headers), got %q", s.In)
		}
	case SecurityTypeOAuth2:
		f := s.Flows
		if f == nil || (f.Implicit == nil && f.Password == nil && f.ClientCredentials == nil && f.AuthorizationCode == nil) {
			return "type oauth2 requires at least one flow"
		}
	case SecurityTypeOpenIDConnect:
		if s.OpenIDConnectURL == "" {
			return "type openIdConnect requires OpenIDConnectURL"
		}
	default:
		return fmt.Sprintf("unsupported type %q (fuge supports http bearer, httpApiKey, oauth2 and openIdConnect)", s.Type)
	}
	if err := checkFugeExtensions(s.Extensions); err != nil {
		return err.Error()
	}
	return ""
}

// OAuthFlows configures the OAuth2 flows an "oauth2" [SecurityScheme]
// supports. At least one flow must be set.
type OAuthFlows struct {
	Implicit          *OAuthFlow `json:"implicit,omitempty"`
	Password          *OAuthFlow `json:"password,omitempty"`
	ClientCredentials *OAuthFlow `json:"clientCredentials,omitempty"`
	AuthorizationCode *OAuthFlow `json:"authorizationCode,omitempty"`
}

// OAuthFlow configures one OAuth2 flow. AuthorizationURL is required for
// Implicit and AuthorizationCode; TokenURL is required for Password,
// ClientCredentials and AuthorizationCode. AvailableScopes is always
// required — pass an empty, non-nil map if the flow grants no scopes, since a
// nil map marshals as JSON null and fails validation against the
// specification's meta-schema.
type OAuthFlow struct {
	AuthorizationURL string            `json:"authorizationUrl,omitempty"`
	TokenURL         string            `json:"tokenUrl,omitempty"`
	RefreshURL       string            `json:"refreshUrl,omitempty"`
	AvailableScopes  map[string]string `json:"availableScopes"`
}

// Keys of the "x-fuge-*" extensions the docs UI reads from an oauth2 or
// openIdConnect [SecurityScheme] to pre-fill its authorization panel. Build
// them with [OAuthExtensions].
const (
	ExtClientID            = "x-fuge-client-id"
	ExtRedirectURI         = "x-fuge-redirect-uri"
	ExtUsePKCE             = "x-fuge-use-pkce"
	ExtDefaultScopes       = "x-fuge-default-scopes"
	ExtTokenName           = "x-fuge-token-name"
	ExtCredentialsLocation = "x-fuge-credentials-location"
	ExtAuthorizeParams     = "x-fuge-authorize-params"
	ExtTokenParams         = "x-fuge-token-params"
)

// OAuthOptions are the defaults the docs UI pre-fills for an oauth2 or
// openIdConnect scheme. Every field is optional, and the person using the UI
// can change any of them before authorizing.
type OAuthOptions struct {
	// ClientID is the public client identifier.
	ClientID string
	// RedirectURI the identity provider sends the user back to. Defaults to
	// the docs page itself, which relays the result to the opener.
	RedirectURI string
	// UsePKCE for the authorization-code flow: "S256" (default), "plain" or "none".
	UsePKCE string
	// DefaultScopes are pre-selected.
	DefaultScopes []string
	// TokenName is the field holding the token in the token response
	// (default "access_token").
	TokenName string
	// CredentialsLocation says where client credentials go on token requests:
	// "body" (default) or "header" (HTTP Basic).
	CredentialsLocation string
	// AuthorizeParams are extra query parameters on the authorization request
	// (e.g. {"audience": "api", "prompt": "login"}).
	AuthorizeParams map[string]string
	// TokenParams are extra form fields on the token request.
	TokenParams map[string]string
}

// OAuthExtensions returns the "x-fuge-*" extensions for o, ready to assign to
// SecurityScheme.Extensions. Zero fields are omitted.
func OAuthExtensions(o OAuthOptions) Extensions {
	ext := Extensions{}
	set := func(k string, v string) {
		if v != "" {
			ext[k] = v
		}
	}
	set(ExtClientID, o.ClientID)
	set(ExtRedirectURI, o.RedirectURI)
	set(ExtUsePKCE, o.UsePKCE)
	set(ExtTokenName, o.TokenName)
	set(ExtCredentialsLocation, o.CredentialsLocation)
	if len(o.DefaultScopes) > 0 {
		ext[ExtDefaultScopes] = o.DefaultScopes
	}
	if len(o.AuthorizeParams) > 0 {
		ext[ExtAuthorizeParams] = o.AuthorizeParams
	}
	if len(o.TokenParams) > 0 {
		ext[ExtTokenParams] = o.TokenParams
	}
	return ext
}

// checkFugeExtensions rejects values the docs UI would not understand.
func checkFugeExtensions(ext Extensions) error {
	if v, ok := ext[ExtUsePKCE].(string); ok && v != "S256" && v != "plain" && v != "none" {
		return fmt.Errorf("%s must be S256, plain or none, got %q", ExtUsePKCE, v)
	}
	if v, ok := ext[ExtCredentialsLocation].(string); ok && v != "body" && v != "header" {
		return fmt.Errorf("%s must be body or header, got %q", ExtCredentialsLocation, v)
	}
	return nil
}
