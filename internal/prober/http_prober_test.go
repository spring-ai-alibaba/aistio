package prober

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/agentscope/info" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"name": "chatbot",
			"runtime": "agentscope-java",
			"version": "1.0.0",
			"sdkVersion": "2.1.0",
			"contractLevel": 2,
			"capabilities": ["session-reporting","hot-reload"],
			"port": 8080,
			"agentConfig": {"model": "qwen-max", "modelProvider": "DashScope", "tools": ["search"]}
		}`))
	}))
	defer srv.Close()

	p := NewHTTPProber()
	info, err := p.ProbeInfo(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("ProbeInfo: %v", err)
	}
	if info.ContractLevel != 2 {
		t.Errorf("contractLevel = %d, want 2", info.ContractLevel)
	}
	if info.Runtime != "agentscope-java" {
		t.Errorf("runtime = %q", info.Runtime)
	}
	if len(info.Capabilities) != 2 {
		t.Errorf("capabilities = %v", info.Capabilities)
	}
	if info.AgentConfig == nil || info.AgentConfig.Model != "qwen-max" {
		t.Errorf("agentConfig = %+v", info.AgentConfig)
	}
}

func TestProbeInfo_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	p := NewHTTPProber()
	if _, err := p.ProbeInfo(context.Background(), srv.URL); err == nil {
		t.Error("expected error on non-200 response")
	}
}

func TestProbeHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := NewHTTPProber()
	ok, err := p.ProbeHealth(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("ProbeHealth: %v", err)
	}
	if !ok {
		t.Error("expected healthy")
	}
}
