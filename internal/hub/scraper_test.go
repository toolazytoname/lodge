package hub

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/toolazytoname/lodge/internal/shared"
)

type scriptedAgentTransport struct {
	responses []*http.Response
	calls     int
	closes    int
	requests  []*http.Request
}

func (transport *scriptedAgentTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.requests = append(transport.requests, request)
	index := transport.calls
	transport.calls++
	if index >= len(transport.responses) {
		index = len(transport.responses) - 1
	}
	response := transport.responses[index]
	response.Request = request
	return response, nil
}

func (transport *scriptedAgentTransport) CloseIdleConnections() {
	transport.closes++
}

func agentResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestScraperReconnectsOnceAfterStaleRouteNotFound(t *testing.T) {
	transport := &scriptedAgentTransport{responses: []*http.Response{
		agentResponse(http.StatusNotFound, "404 page not found"),
		agentResponse(http.StatusOK, `{"ok":true,"hostname":"agent-a","agentVersion":"0.2.0","apiVersion":"v1"}`),
	}}
	scraper := NewScraper(NewMemStore(), 0)
	scraper.client = &http.Client{Transport: transport}

	var ping shared.Ping
	err := scraper.getJSON(context.Background(), AgentConfig{URL: "http://100.64.0.1:8443", Token: "secret"}, "/v1/ping", &ping)
	if err != nil {
		t.Fatalf("stale route should recover on a fresh connection: %v", err)
	}
	if transport.calls != 2 || transport.closes != 1 {
		t.Fatalf("expected one reconnect retry, calls=%d closes=%d", transport.calls, transport.closes)
	}
	if ping.AgentVer != "0.2.0" || ping.APIVersion != shared.APIVersion {
		t.Fatalf("retry response was not decoded: %+v", ping)
	}
	for _, request := range transport.requests {
		if request.Method != http.MethodGet || request.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("retry changed the authenticated GET contract: %+v", request)
		}
	}
}

func TestScraperDoesNotRetryAuthenticationFailure(t *testing.T) {
	transport := &scriptedAgentTransport{responses: []*http.Response{
		agentResponse(http.StatusUnauthorized, "unauthorized"),
	}}
	scraper := NewScraper(NewMemStore(), 0)
	scraper.client = &http.Client{Transport: transport}

	var ping shared.Ping
	err := scraper.getJSON(context.Background(), AgentConfig{URL: "http://agent", Token: "wrong"}, "/v1/ping", &ping)
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("authentication failure should be preserved: %v", err)
	}
	if transport.calls != 1 {
		t.Fatalf("authentication failure must not be retried, calls=%d", transport.calls)
	}
}

func TestScraperBoundsRepeatedNotFoundRetry(t *testing.T) {
	transport := &scriptedAgentTransport{responses: []*http.Response{
		agentResponse(http.StatusNotFound, "old route"),
		agentResponse(http.StatusNotFound, "still old"),
	}}
	scraper := NewScraper(NewMemStore(), 0)
	scraper.client = &http.Client{Transport: transport}

	var ping shared.Ping
	err := scraper.getJSON(context.Background(), AgentConfig{URL: "http://agent", Token: "token"}, "/v1/ping", &ping)
	if err == nil || !strings.Contains(err.Error(), "still old") {
		t.Fatalf("second route failure should be returned: %v", err)
	}
	if transport.calls != 2 || transport.closes != 2 {
		t.Fatalf("404 retry must remain bounded, calls=%d closes=%d", transport.calls, transport.closes)
	}
}
