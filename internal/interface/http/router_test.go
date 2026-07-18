package http

import "testing"

func TestIsHAClusterActionPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/api/v1/clusters/demo/bootstrap", true},
		{"/api/v1/clusters/demo/bootstrap/", true},
		{"/api/v1/clusters/demo/vip/config", true},
		{"/api/v1/clusters/demo/failover/plan", true},
		{"/api/v1/clusters/demo/architecture/start", true},
		{"/api/v1/clusters/demo", false},
		{"/api/v1/clusters/demo/machines", false},
		{"/api/v1/clusters/bootstrap-demo", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := isHAClusterActionPath(tt.path); got != tt.want {
				t.Fatalf("isHAClusterActionPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
