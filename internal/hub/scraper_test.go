package hub

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

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

func TestNewScraperDisablesProxyAndRedirects(t *testing.T) {
	scraper := NewScraper(NewMemStore(), 0)
	transport, ok := scraper.client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatal("scraper must clone a transport with environment proxies disabled")
	}
	request, err := http.NewRequest(http.MethodGet, "http://agent.example.test/v1/ping", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := scraper.client.CheckRedirect(request, []*http.Request{request}); err != http.ErrUseLastResponse {
		t.Fatalf("scraper must refuse redirects, got %v", err)
	}
}

func TestScraperRejectsOversizedBody(t *testing.T) {
	transport := &scriptedAgentTransport{responses: []*http.Response{
		agentResponse(http.StatusOK, strings.Repeat("x", maximumAgentObservationBody+2)),
	}}
	scraper := NewScraper(NewMemStore(), 0)
	scraper.client = &http.Client{Transport: transport}

	var ping shared.Ping
	err := scraper.getJSON(context.Background(), AgentConfig{URL: "http://agent", Token: "token"}, "/v1/ping", &ping)
	if err == nil || !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("oversized agent body should be rejected: %v", err)
	}
}

type rejectOnlineStore struct {
	Store
	rejected bool
}

func (store *rejectOnlineStore) Update(ctx context.Context, id string, online bool, lastError string, ping shared.Ping, status *shared.Status, services []shared.Service, observedAt time.Time) error {
	if online {
		store.rejected = true
		return errors.New("projected observation invalid")
	}
	return store.Store.Update(ctx, id, online, lastError, ping, status, services, observedAt)
}

type pathAgentTransport struct {
	bodies map[string]string
}

func (transport *pathAgentTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	body, ok := transport.bodies[request.URL.Path]
	if !ok {
		return agentResponse(http.StatusNotFound, "missing"), nil
	}
	response := agentResponse(http.StatusOK, body)
	response.Request = request
	return response, nil
}

func TestScraperPersistsOfflineWhenObservationIsRejected(t *testing.T) {
	inner := NewMemStore()
	if err := inner.SetAgents(context.Background(), []AgentConfig{{ID: "host-a", Name: "A", URL: "http://agent", Token: "token"}}); err != nil {
		t.Fatal(err)
	}
	if err := inner.Update(context.Background(), "host-a", true, "", shared.Ping{APIVersion: shared.APIVersion, AgentVer: "0.9.0"}, nil, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	store := &rejectOnlineStore{Store: inner}
	scraper := NewScraper(store, 0)
	scraper.client = &http.Client{Transport: &pathAgentTransport{bodies: map[string]string{
		"/v1/ping":     `{"ok":true,"hostname":"agent-a","agentVersion":"0.9.0","apiVersion":"v1"}`,
		"/v1/status":   `{"hostname":"agent-a","uptimeSec":1,"load":{"one":0,"five":0,"fifteen":0,"cpus":1},"memory":{"totalBytes":1,"usedBytes":0},"disks":[]}`,
		"/v1/services": `{"services":[]}`,
	}}}
	err := scraper.scrapeOne(context.Background(), AgentConfig{ID: "host-a", Name: "A", URL: "http://agent", Token: "token"})
	if err == nil || !store.rejected {
		t.Fatalf("rejected observation should surface: %v", err)
	}
	snapshot := inner.Snapshot()[0]
	if snapshot.Online {
		t.Fatalf("rejected observation must not leave the host online: %+v", snapshot)
	}
	if snapshot.LastError != "observation rejected" {
		t.Fatalf("offline error should be categorized: %+v", snapshot)
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
