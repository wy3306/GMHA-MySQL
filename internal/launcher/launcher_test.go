package launcher

import (
	"net/http/httptest"
	"testing"
)

func TestLauncherURL(t *testing.T) {
	tests := map[string]string{
		":8079":          "http://127.0.0.1:8079",
		"0.0.0.0:8079":   "http://127.0.0.1:8079",
		"127.0.0.1:8079": "http://127.0.0.1:8079",
	}
	for input, want := range tests {
		if got := launcherURL(input); got != want {
			t.Fatalf("launcherURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestBrowserManagerURLUsesRequestHost(t *testing.T) {
	controller, err := NewController(Config{ManagerURL: "auto", ManagerListen: ":8080"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "http://192.0.2.10:8079/api/status", nil)
	if got, want := controller.browserManagerURL(request), "http://192.0.2.10:8080"; got != want {
		t.Fatalf("browserManagerURL() = %q, want %q", got, want)
	}
}
