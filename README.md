# fuge

A typed RPC layer for [Centrifuge](https://github.com/centrifugal/centrifuge)
WebSocket connections, documented as it's built. Register a typed Go
function and it becomes both a live RPC method and an entry in a generated
[AsyncAPI 3.1](https://www.asyncapi.com/) document — the way
[huma](https://github.com/danielgtaylor/huma) binds an HTTP handler and
documents it as OpenAPI in one call.

```go
api := fuge.New(fuge.DefaultConfig("Platform RPC", "1.0.0"))

fuge.Register(api, fuge.Operation{
    OperationID: "create-country",
    Method:      "country.create",
    Summary:     "Create a new country",
}, func(ctx context.Context, req *CreateCountryRequest) (*Country, error) {
    return &Country{ID: 1, Name: req.Name}, nil
})

node.OnConnect(func(c *centrifuge.Client) { api.Attach(node, c) })
api.ServeDocs(mux) // /asyncapi.json, /asyncapi.yaml and the interactive /asyncdocs/ UI
```

### Authorizing calls and channel access

RPC methods are gated with an `OpMiddleware` (`api.UseMiddleware`), which sees
the operation and its extensions. Channels have the equivalent: a
`ChannelGate` (`api.UseChannelGate`) that runs before every client subscribe
and publish, and sees the channel's extensions:

```go
api.UseChannelGate(func(ctx context.Context, ch *fuge.ChannelInfo, action fuge.ChannelAction,
    params fuge.Params, info fuge.SubscribeInfo) error {
    ext := ch.Extensions // documents subscribing
    if action == fuge.ChannelPublish {
        ext = ch.PublishExtensions // documents client publishing (AllowPublish)
    }
    return checkPermissions(ctx, ext) // e.g. read ext["x-permissions"]
})
```

Declaring a requirement as an extension on the operation or channel means the
same value is shown in the AsyncAPI document and enforced by the gate. Return
`centrifuge.ErrorPermissionDenied` to refuse; per-channel rules that depend on
the parameters (does this room belong to this user?) still go in
`ChannelHandle.OnSubscribe`, which runs after the gates.

### Security

fuge supports the token-based schemes a browser WebSocket can carry; `New`
panics on anything else:

| Scheme | Delivered as |
|---|---|
| `http` + `bearer` | Centrifuge `connect` token |
| `oauth2` (all four flows) | access token → connect token |
| `openIdConnect` | access token → connect token |
| `httpApiKey`, `in: query` / `cookie` | URL query parameter / cookie |

(Header-based keys, HTTP basic and the other AsyncAPI types can't be sent by a
browser WebSocket, so they aren't offered.) The docs UI has an **Authorize**
panel for these: it runs authorization code + PKCE, implicit, password and
client-credentials flows and OpenID Connect discovery, pre-filled from the
document, with every field editable. Tune the defaults with `x-fuge-*`
extensions:

```go
cfg.SecuritySchemes["oauth"] = fuge.SecurityScheme{
    Type:  fuge.SecurityTypeOAuth2,
    Flows: &fuge.OAuthFlows{AuthorizationCode: &fuge.OAuthFlow{
        AuthorizationURL: "https://idp.example.com/auth",
        TokenURL:         "https://idp.example.com/token",
        AvailableScopes:  map[string]string{"openid": "OIDC"},
    }},
    Extensions: fuge.OAuthExtensions(fuge.OAuthOptions{
        ClientID:        "web",
        DefaultScopes:   []string{"openid"},
        UsePKCE:         "S256", // or "plain", "none"
        AuthorizeParams: map[string]string{"audience": "api"},
    }),
}
```

`OAuthOptions` also covers `RedirectURI` (default: the docs page), `TokenName`,
`CredentialsLocation` (`"body"`/`"header"`) and `TokenParams`. The docs page's
CSP automatically allows the token/discovery endpoints' origins; add others
(e.g. endpoints an OIDC discovery document points at) with
`DocsOptions.ConnectSrc`. The identity provider must allow CORS from the docs
origin for the token endpoint, and the redirect URI must be registered with it.

### Serving the docs

`api.ServeDocs(m)` mounts the document and the interactive reference (rendered
operations plus a "Try it" client) on anything with `Handle(pattern, http.Handler)`
— `*http.ServeMux`, chi. The paths come from `Config` and default to:

| Config field | Default |
|---|---|
| `DocsPath` | `/asyncdocs` |
| `JSONPath` | `/asyncapi.json` |
| `YAMLPath` | `/asyncapi.yaml` |

Set one and everything follows (routes, the UI's spec links). Use
`api.DocsRoute()`, `api.JsonRoute()` and `api.YamlRoute()` wherever you need the
URL. `cfg.Docs` (`DocsOptions`) sets the page title and default websocket URL.

For routers with a different `Handle` signature (gorilla/mux), loop over the routes:

```go
for _, r := range api.Routes() { // or api.SpecRoutes() / api.DocsRoutes()
    if r.Subtree {
        router.PathPrefix(r.Path + "/").Handler(r.Handler)
    } else {
        router.Handle(r.Path, r.Handler)
    }
}
```

`api.DocsHandler(prefix, DocsOptions{...})` is the lower-level handler if you
want to place the UI yourself (`SpecURL` points it at a document hosted elsewhere).

See `example/main.go` for a complete, runnable version of the above.

## Why

Centrifuge gives you a WebSocket connection and an RPC event; it doesn't
give you routing, typed request/response handling, or any way to describe
your RPC surface to the outside world. fuge adds all three:

- **Typed handlers.** Write `func(ctx, *Req) (*Res, error)`; fuge decodes,
  validates (if `Req` implements `Validate() error`), calls your function,
  and encodes the result.
- **Real errors.** A returned error is mapped to an internal error and
  logged without leaking details, unless it's a `*fuge.Error` (see
  `ErrNotFound`, `ErrConflict`, `ErrValidation`, `ErrTimeout`, or build your
  own with `NewError`) or a `*centrifuge.Error`, in which case its code,
  message and `Temporary` flag go straight to the client. `WithErrorMapper`
  translates domain errors — a database's "not found", say — without every
  handler needing to know about the wire format.
- **A generated AsyncAPI document.** Every `Register`'d operation appears in
  `api.JSON()` / `api.DocHandler()`: its request and response schemas
  (reflected from your Go types), its possible error codes, its security
  requirements, and any `x-` extensions you attach. Validation against the official
  AsyncAPI 3.1 meta-schema is opt-in (see [Dependencies](#dependencies)).

## The three layers

Most applications only ever touch the first one.

| Layer                   | Use for                                      | Key names                                                         |
|-------------------------|----------------------------------------------|-------------------------------------------------------------------|
| **Documented RPC**      | Anything client-facing, request/reply        | `Register`, `Operation`, `Group`, `UseMiddleware`, `OpMiddleware` |
| **Documented channels** | Real pub/sub — broadcast, subscribe, publish | `RegisterChannel`, `Channel`, `AllowPublish`, `OnSubscribe`       |
| **Raw**                 | Internal/undocumented methods                | `Handle`, `RouteGroup`, `Use`, `Middleware`                       |
| **Router**              | Rarely touched directly                      | `Router`, `NewRouter`, `Attach`, `Call`                           |

`API` (built by `New`) embeds a `Router` directly — there's no separate
router to construct or wire up. `Register` and `Handle` both end up
dispatching through the same `Router`, so router-level `Use` middleware
(timeouts, logging, auth) applies to everything either way; `UseMiddleware`
is the documented-only layer that can additionally read the `Operation`
being called (`OperationFrom(ctx)`), e.g. to check `op.Security`.

## Security and extensions

```go
cfg := fuge.DefaultConfig("Platform RPC", "1.0.0")
cfg.SecuritySchemes["bearer"] = fuge.SecurityScheme{
    Type: "http", Scheme: "bearer", BearerFormat: "JWT",
}

fuge.Register(api, fuge.Operation{
    Method:     "country.create",
    Security:   []map[string][]string{{"bearer": {}}},
    Errors:     []uint32{403},
    Extensions: map[string]any{"x-permissions": []string{"location.create"}},
}, createCountry)
```

`Security` is a list of alternative requirements — each map is one set of
schemes that together satisfy the operation (an "AND"); the list itself is
the "OR". AsyncAPI 3 can't express an "AND" *inside* one requirement (unlike
OpenAPI's security model), so a map with more than one entry panics at
registration rather than silently producing an invalid document.

## Groups

```go
billing := api.Group("billing", "Billing")           // documented, prefixes method + tags
invoices := billing.Group("invoice", "Invoices")
fuge.Register(invoices, fuge.Operation{Method: "create"}, createInvoice)
// wire method: "billing.invoice.create", tags: ["Billing", "Invoices"]
```

`RouteGroup` (on `Router` or `API.RouteGroup`) does the same for raw,
undocumented methods — it prefixes the wire name but never touches the
document.

## Channels: real pub/sub, not RPC

`Register`'d methods live on synthetic `rpc.<method>` channels — a
documentation device, unrelated to Centrifuge's actual channel/subscribe
mechanism. For a real channel — one clients subscribe to, that the server
(or another client, or another service acting as one) publishes into — use
`RegisterChannel`:

```go
notif := fuge.RegisterChannel[Notification](api, fuge.Channel{
    ID: "user-notifications", Pattern: "user:{userId}:notifications",
})
notif.OnSubscribe(func(ctx context.Context, params fuge.Params, info fuge.SubscribeInfo) error {
    if params["userId"] != info.UserID {
        return centrifuge.ErrorPermissionDenied
    }
    return nil
})
notif.Publish(node, fuge.Params{"userId": user.ID}, Notification{Text: "..."})
```

`Pattern` may contain `{name}` placeholders — `Params` is what you get back
in `OnSubscribe`/`AllowPublish` (extracted from the real address a client
used) and what you supply to `Publish` (rendered back into a real address).
A placeholder matches one or more characters that aren't `:`, matching
Centrifuge's usual convention of `:` as a namespace separator.

**Centrifuge itself has no notion of "declared" channels** — by default any
client can subscribe to any string. `API.Attach` closes that gap: a
subscribe to an address matching no `RegisterChannel`'d pattern is rejected
with `centrifuge.ErrorUnknownChannel` before `OnSubscribe` ever runs.

Publish, by contrast, is refused by Centrifuge *unless* explicitly allowed.
`AllowPublish` opts a channel into it, and lets the publish shape differ from
the broadcast shape — what a publisher sends isn't always what subscribers
should receive:

```go
fuge.AllowPublish(chatRoom, func(ctx context.Context, params fuge.Params, in *ChatInput) (*ChatMessage, error) {
    return &ChatMessage{RoomID: params["roomId"], Author: fuge.InfoFrom(ctx).UserID, Text: in.Text}, nil
})
```

`in` is decoded and `Validate()`'d exactly like an RPC request. Returning
`(nil, nil)` accepts the publish without broadcasting anything. Every
operation fuge generates — RPC, broadcast, or publish — carries
`x-fuge-operation-kind` (`"rpc"` / `"broadcast"` / `"publish"`), so a
document consumer never has to guess intent from channel naming.

## Testing

```
go test -race ./...
```

The suite covers routing (groups, middleware ordering, timeouts, panic
recovery, bounded concurrency), the AsyncAPI document model (security
schemes, `x-` extensions, schema reflection, meta-schema validation (`validate` package) for both
success and failure cases), channels (address-pattern matching and
rendering, `RegisterChannel`/`AllowPublish` document shape, and end-to-end
subscribe/publish dispatch — including that a rejected or transformed
publish never lets a client's raw bytes leak to subscribers), and the
RPC layers together (a `Register`'d, `Group`-nested operation reachable
under its full prefixed name, documented under that same name, with its
`Timeout` actually reaching the router).

## Install

```
go get github.com/julaw-energy/fuge
```

Requires Go 1.26+.

## Dependencies

Direct, for package `fuge`:

| Module | Why |
|---|---|
| `github.com/centrifugal/centrifuge` | The transport fuge wraps (`Node`, `Client`, RPC and pub/sub events). Required. |
| `github.com/swaggest/jsonschema-go` | Go type → JSON Schema for request/response payloads. |
| `go.yaml.in/yaml/v3` | Block-style YAML rendering of the document. |

Logging uses the standard library's `log/slog` behind the small `Logger` interface (`WithLogger`).
Centrifuge brings its own transitive dependencies (protobuf, redis client, prometheus, ...); fuge adds none of them itself.

### Optional: meta-schema validation

Package `github.com/julaw-energy/fuge/validate` checks the generated document against the official AsyncAPI 3.1
meta-schema. It embeds the schema (about 400 KB) and depends on `santhosh-tekuri/jsonschema/v6`, so it is a separate
package: programs that don't import it don't link either. Opt in per API:

```go
cfg := fuge.DefaultConfig("Platform RPC", "1.0.0")
cfg.Validator = validate.Bytes // api.JSON() now fails on a non-conforming document
```

`Config.Validator` is any `func([]byte) error`, so you can plug in your own check.

## Status

Pre-1.0: the API may still change. It is a merge of three previously separate pieces (an RPC router, a huma-style
registration layer, and a fork of [`mitresthen/asyncapi3`](https://github.com/mitresthen/asyncapi3) extended with security
schemes, `x-` extensions, and AsyncAPI 3.1 support) into one package.

## License

Apache License, Version 2.0 — see `LICENSE`. Some files are derived from
[`mitresthen/asyncapi3`](https://github.com/mitresthen/asyncapi3) (also
Apache-2.0); see `NOTICE` for which ones and what changed.
