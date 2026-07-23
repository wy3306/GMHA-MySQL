package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAICapabilitiesCanBeDiscoveredWithoutWorkbenchState(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/ai/capabilities", nil)
	response := httptest.NewRecorder()
	NewAIHandler(nil).Handle(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		APIVersion string `json:"api_version"`
		Actions    []struct {
			ID         string `json:"id"`
			HTTPMethod string `json:"http_method"`
			APIPath    string `json:"api_path"`
		} `json:"actions"`
		ClusterEndpoints []struct {
			ID                  string   `json:"id"`
			InvocationMode      string   `json:"invocation_mode"`
			AIActionID          string   `json:"ai_action_id"`
			SensitiveParameters []string `json:"sensitive_parameters"`
		} `json:"cluster_endpoints"`
		SecurityBoundary string `json:"security_boundary"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.APIVersion != "v1" {
		t.Fatalf("unexpected API version %q", payload.APIVersion)
	}
	found := false
	for _, action := range payload.Actions {
		if action.ID == "configure_cluster_vip" {
			found = action.HTTPMethod == http.MethodPost && action.APIPath == "/api/v1/clusters/{cluster_name}/vip/config"
		}
	}
	if !found {
		t.Fatalf("configure_cluster_vip is not discoverable: %#v", payload.Actions)
	}
	endpoints := make(map[string]struct {
		mode      string
		action    string
		sensitive []string
	})
	for _, endpoint := range payload.ClusterEndpoints {
		endpoints[endpoint.ID] = struct {
			mode      string
			action    string
			sensitive []string
		}{endpoint.InvocationMode, endpoint.AIActionID, endpoint.SensitiveParameters}
	}
	if endpoint := endpoints["clusters.vip.config.apply"]; endpoint.mode != "ai_action" || endpoint.action != "configure_cluster_vip" {
		t.Fatalf("VIP apply endpoint is not linked to its AI action: %#v", endpoint)
	}
	if endpoint := endpoints["clusters.mysql.install"]; endpoint.mode != "secure_input_api" || len(endpoint.sensitive) == 0 {
		t.Fatalf("secret-bearing MySQL install was not marked for secure input: %#v", endpoint)
	}
	if payload.SecurityBoundary == "" {
		t.Fatal("capability response omitted the secure-input boundary")
	}
}

func TestAICapabilitiesRejectsMutationMethods(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/ai/capabilities", nil)
	response := httptest.NewRecorder()
	NewAIHandler(nil).Handle(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unexpected status %d", response.Code)
	}
}
