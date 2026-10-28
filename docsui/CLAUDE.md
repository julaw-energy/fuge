# docsui — interactive AsyncAPI reference (part of the reusable `fuge` package)

Dependency-free SPA (no build step, no CDN) that renders the AsyncAPI doc and has a "Try it" panel
for every operation. Embedded by `../docs.go` (`//go:embed docsui`) and exposed as `api.DocsHandler(prefix, DocsOptions)`;
tests in `../docs_test.go`. Must stay free of app-specific code/URLs (package is meant to be published):
- All URLs are **relative** (`app.js`, `asyncapi.json`) so it works under any mount prefix.
- `index.html` has `{{TITLE}}`/`{{SPEC_JSON}}`/`{{SPEC_YAML}}`/`{{WS_URL}}` placeholders (spec ones overridable via `DocsOptions.SpecURL`), filled by `docs.go` into
  `<meta name="fuge-*">` tags that `app.js` reads via `CFG`. Add new server→UI settings the same way.
- Only `index.html`, `app.js`, `auth.js`, `app.css` are served (explicit whitelist in `docs.go`); a new asset file needs a case there.
Edit these files only; Go needs a rebuild to pick them up. A host app mounts it via `API.ServeDocs`/`Routes`.

## Files
- `index.html` — shell; loads `/asyncdocs/app.css` + `/asyncdocs/app.js`.
- `auth.js` — Authorize panel + OAuth flows (`window.FugeAuth`; `init({spec,h,onChange})`, `summary`, `open`, `prepare(url)`→`{url,token,warning}`).
  Supports only what fuge allows (see `../security.go`): http bearer, httpApiKey query/cookie, oauth2 (code+PKCE, implicit, password,
  client credentials), openIdConnect (discovery). Defaults from the scheme + `x-fuge-*` extensions; user overrides persist
  (`fuge.auth.cfg.<scheme>` localStorage, secrets/tokens `fuge.auth.secret.<scheme>` sessionStorage). The popup callback is this same
  page (`?code&state=fuge.…`), relayed via BroadcastChannel/postMessage — the script returns early, `app.js` skips booting.
  Debug log: every step goes through `log(step, data)` (secrets masked via `scrub`/`scrubUrl`), kept in `localStorage['fuge.debug.log']`
  so the popup's steps survive; shown in Authorize → Debug log and via `fugeDebug()`. New auth code should log its steps the same way.
  `app.js` only shows the Authorize button (`authButton`) and per-operation scheme badges (`securityRow`).
- `app.css` — theme tokens (`:root` + `:root[data-theme="dark"]`), then layout/components. Mobile rules are
  in the `@media (max-width: 760px)` block at the end (sidebar becomes an off-canvas drawer).
- `app.js` — one IIFE, ~665 lines. Sections (grep the `/* ──` banners; use `grep -n "^function \|^class "`):
  helpers (`h()` DOM builder, `store`) → spec model (`deref`, `buildModel`) → example generation (`example`)
  → schema renderer (`renderProps`/`renderProp`) → `Client` (Centrifuge JSON protocol over WebSocket)
  → app `state` → connection bar (`renderConn`) → navigation (`renderNav`, `tagGroups`, `group`, `route`)
  → pages (`overviewPage`, `rpcPage`, `channelPage`, `schemaPage`) → widgets (`rpcTryIt`, `channelTryIt`,
  `snippetsCard`, `logView`, `codeBlock`) → `boot()`.

## Conventions
- The page's CSP is `style-src 'self'`: **never** use `style:`/`style=` attributes (blocked) — add a class to `app.css` (see the utilities at its end).
- Build DOM only with `h(tag, attrs, ...children)`; never `innerHTML` with spec content (only the static mail icon).
- Colors/spacing via CSS variables; any new color needs both light and dark values.
- Hash routing: `#/` overview, `#/op/<operationId>`, `#/schema/<name>`. `route()` re-renders `#main`.
- Spec facts: operations carry `x-fuge-operation-kind` (`rpc`|`broadcast`), `x-permissions`, `tags`.
  RPC method name = `channel.address`; broadcast channel name = `channel.address`. Sidebar groups by first tag,
  untagged fall back to "RPC methods"/"Channels".
- Persistence (keys prefixed `fuge.`): URL + theme + collapsed groups in `localStorage`, tokens/secrets in `sessionStorage` only.
- Live updates to the open channel page go through `refreshLive`; sidebar redraw via `renderNav()`.

## Testing without the real server
Headless Chrome against a static copy (no jsdom available). Scratch recipe:
1. Convert a sample spec to JSON (`/home/tendo/GolandProjects/expr-editor/socket/julaw.yaml` via js-yaml; or serve a real one with a throwaway `main` calling `api.DocsHandler`)
   to `asyncapi.json`; put it and the `docsui/` files (index.html placeholders filled by hand) in a temp dir.
2. `python3 -m http.server <port>` there, then
   `google-chrome --headless=new --no-sandbox --window-size=1500,900 --screenshot=out.png "http://localhost:<port>/index.html#/op/create-country"`
   (use `--window-size=390,800` for mobile) and view the PNG.
3. `node --check app.js` for syntax; `go build ./...` for the embed.
Try-it connect/RPC/subscribe is only verifiable against a running host app (`/connection/websocket`).
Reference clients for the protocol: `/home/tendo/GolandProjects/expr-editor/socket/centclient/bare.js`.

## Working efficiently
- Don't read all of `app.js`; read the one section you're changing (`Read` with offset/limit from the grep above).
- UI-only tweaks are usually `app.css` alone.
