package core

import (
	"context"
	"strings"
)

type managerHTTPAddrContextKey struct{}

// WithManagerHTTPAddr records the Manager endpoint used by the active task
// connection so handlers can download Manager-hosted resources from the same
// reachable endpoint.
func WithManagerHTTPAddr(ctx context.Context, addr string) context.Context {
	return context.WithValue(ctx, managerHTTPAddrContextKey{}, strings.TrimRight(strings.TrimSpace(addr), "/"))
}

// ManagerHTTPAddrFromContext returns the endpoint used by the active task
// connection. It is intentionally scoped to one dispatch and never persisted.
func ManagerHTTPAddrFromContext(ctx context.Context) string {
	addr, _ := ctx.Value(managerHTTPAddrContextKey{}).(string)
	return strings.TrimRight(strings.TrimSpace(addr), "/")
}
