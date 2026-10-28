package fuge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/centrifugal/centrifuge"
)

// CallInfo is attached to the context of every call.
type CallInfo struct {
	Method    string
	RequestID string
	ClientID  string
	UserID    string
	Received  time.Time
	// Client is nil for in-process calls made via Router.Call.
	Client *centrifuge.Client
}

type infoKey struct{}

func withInfo(ctx context.Context, info CallInfo) context.Context {
	return context.WithValue(ctx, infoKey{}, info)
}

// InfoFrom returns the call metadata for the current request.
func InfoFrom(ctx context.Context) (CallInfo, bool) {
	info, ok := ctx.Value(infoKey{}).(CallInfo)
	return info, ok
}

func newRequestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
