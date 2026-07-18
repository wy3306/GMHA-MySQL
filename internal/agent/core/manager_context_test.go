package core

import (
	"context"
	"testing"
)

func TestManagerHTTPAddrContext(t *testing.T) {
	ctx := WithManagerHTTPAddr(context.Background(), " http://192.168.31.59:8080/ ")
	if got, want := ManagerHTTPAddrFromContext(ctx), "http://192.168.31.59:8080"; got != want {
		t.Fatalf("ManagerHTTPAddrFromContext() = %q, want %q", got, want)
	}
}
