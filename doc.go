// Package fuge is a typed RPC layer for Centrifuge WebSocket connections,
// documented as it's built. Register a typed Go function and it becomes both
// a live RPC method and an entry in the generated AsyncAPI 3.1 document —
// the way huma binds an HTTP handler and documents it as OpenAPI in one call.
//
// The package works at three layers, and most applications only ever touch
// the top one:
//
//   - Register / Operation / Group: bind a typed handler and describe it
//     (summary, tags, security, error codes, "x-" extensions). This is what
//     builds the AsyncAPI document, and it's the primary, recommended way to
//     add a method.
//   - Handle / RouteGroup (on API or Router): register a raw, undocumented
//     method directly — an escape hatch for internal methods that shouldn't
//     appear in the public document.
//   - Router: the underlying dispatcher (middleware, timeouts, panic
//     recovery, bounded concurrency, Centrifuge wiring). API embeds one; most
//     applications never construct a Router directly.
//
// # Getting started
//
//	api := fuge.New(fuge.DefaultConfig("Platform RPC", "1.0.0"))
//
//	fuge.Register(api, fuge.Operation{
//	    OperationID: "create-country",
//	    Method:      "country.create",
//	    Summary:     "Create a new country",
//	}, func(ctx context.Context, req *CreateCountryRequest) (*Country, error) {
//	    return &Country{ID: 1, Name: req.Name}, nil
//	})
//
//	node.OnConnect(func(c *centrifuge.Client) { api.Attach(node, c) })
//	api.ServeDocs(mux) // /asyncapi.json, /asyncapi.yaml, interactive /asyncdocs/ (paths: Config.DocsPath etc.)
//
// # Errors
//
// A handler can return a plain error (mapped to an internal error and
// logged, never leaking details to the client), a *fuge.Error built with
// NewError or one of ErrNotFound / ErrConflict / ErrValidation / ErrTimeout
// (mapped to its Code/Message/Temporary), or a *centrifuge.Error directly.
// WithErrorMapper translates domain errors (e.g. a database's "not found")
// without every handler needing to know about them.
//
// # Documenting a method
//
// Operation mirrors huma.Operation. Security is a list of alternative
// requirements — each map is one set of schemes that together satisfy the
// operation (an "AND"); the list itself is the "OR". AsyncAPI 3 cannot
// express an "AND" inside one requirement (unlike OpenAPI), so a map with
// more than one entry panics at registration. Extensions are "x-" keys
// merged onto the operation. Errors are extra Centrifuge error codes the
// operation may return, beyond the internal/bad-request/validation codes
// added automatically.
//
// # Channels: real Centrifuge pub/sub, not RPC
//
// Register documents a synthetic "rpc.<method>" channel purely so every RPC
// method has somewhere to hang its schemas in the document; it has nothing
// to do with Centrifuge's actual channel/subscribe mechanism. For a real
// channel — one clients subscribe to, that the server (or another client,
// or another service acting as one) publishes into — use RegisterChannel:
//
//	notif := fuge.RegisterChannel[Notification](api, fuge.Channel{
//	    ID: "user-notifications", Pattern: "user:{userId}:notifications",
//	})
//	notif.OnSubscribe(func(ctx context.Context, params fuge.Params, info fuge.SubscribeInfo) error {
//	    if params["userId"] != info.UserID {
//	        return centrifuge.ErrorPermissionDenied
//	    }
//	    return nil
//	})
//	notif.Publish(node, fuge.Params{"userId": user.ID}, Notification{Text: "..."})
//
// Centrifuge itself has no notion of "declared" channels — by default any
// client can subscribe to any string. API.Attach closes that gap: a
// subscribe to an address matching no RegisterChannel'd pattern is rejected
// with centrifuge.ErrorUnknownChannel before OnSubscribe ever runs.
//
// Publish, similarly, is refused by Centrifuge unless explicitly allowed.
// AllowPublish opts a channel into it, and lets the publish shape differ
// from the broadcast shape — what a publisher sends isn't always what
// subscribers should receive:
//
//	fuge.AllowPublish(chatRoom, func(ctx context.Context, params fuge.Params, in *ChatInput) (*ChatMessage, error) {
//	    return &ChatMessage{RoomID: params["roomId"], Author: fuge.InfoFrom(ctx).UserID, Text: in.Text}, nil
//	})
//
// in is decoded and Validate()'d like an RPC request. Returning (nil, nil)
// accepts the publish without broadcasting anything.
//
// Every operation fuge generates — RPC, broadcast, or publish — carries
// "x-fuge-operation-kind" ("rpc" / "broadcast" / "publish"), so a document
// consumer (a renderer, a future client generator) never has to guess intent
// from channel naming.
package fuge
