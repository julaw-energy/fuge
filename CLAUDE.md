# pkg/fuge

Typed RPC layer over centrifuge that generates an AsyncAPI 3.1 doc (huma-style, but for WebSockets).
Own Go module (`github.com/julaw-energy/fuge`, `go.mod` here). Self-contained: must not import anything from the host app. Run `go test ./...` from this directory. README.md has the full user-facing design.

## Map
- `api.go` (`New`, `API`), `register.go` (`Register`), `operation.go` — documented RPC layer.
- `channels.go` (`RegisterChannel`) — documented pub/sub layer; `ChannelGate` (`API.UseChannelGate`) authorizes client subscribe/publish from channel extensions.
- `router.go`, `middleware.go`, `callinfo.go` — raw routing (`Handle`, `Use`, `Attach`, `Call`).
- `asyncapi3.go`, `spec.go`, `reflect.go`, `merge.go` — AsyncAPI document build + Go type → JSON schema.
- `validate/` (separate package, opt-in via `Config.Validator`) — validation against the official AsyncAPI meta-schema; fuge itself must not import it or `jsonschema/v6`.
- `errors.go` (`*fuge.Error`, `WithErrorMapper`), `security.go` (only bearer/httpApiKey query+cookie/oauth2/OIDC allowed; `x-fuge-*` OAuth extensions), `extensions.go`, `codec.go`.
- `docs.go` + `docsui/` — embedded interactive AsyncAPI reference with "Try it" client (`API.ServeDocs`/`Routes`/`DocsHandler`; paths set via `Config.DocsPath/JSONPath/YAMLPath`); `docsui/CLAUDE.md` has its map.
- `example/main.go` — runnable reference usage.

## Rules
- Any change to public behavior needs a test in the matching `*_test.go`; run `go test ./...` in `pkg/fuge`.
- Generated documents must still pass meta-schema validation (`validate/validate_test.go`).
- Keep README.md in sync when the public API changes.
