package hub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/toolazytoname/lodge/internal/domain"
	"github.com/toolazytoname/lodge/internal/shared"
)

func TestWebLinkProberUsesHeadWithoutFollowingRedirects(t *testing.T) {
	var redirected bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodHead {
			t.Errorf("probe method = %s, want HEAD", request.Method)
		}
		switch request.URL.Path {
		case "/ok":
			redirected = true
			w.WriteHeader(http.StatusNoContent)
		case "/redirect":
			http.Redirect(w, request, "/ok", http.StatusFound)
		case "/degraded":
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	prober := newWebLinkProber()
	checkedAt := time.Now().UTC()
	targets := []webLinkTarget{
		{hostID: "host-a", workloadKey: "docker:redirect", url: server.URL + "/redirect"},
		{hostID: "host-a", workloadKey: "docker:degraded", url: server.URL + "/degraded"},
	}
	checks := prober.Probe(context.Background(), targets, checkedAt)
	if len(checks) != 2 || checks[0].State != domain.WebLinkReachable || checks[0].HTTPStatus != http.StatusFound {
		t.Fatalf("redirect probe mismatch: %+v", checks)
	}
	if redirected {
		t.Fatal("probe followed a redirect")
	}
	if checks[1].State != domain.WebLinkDegraded || checks[1].HTTPStatus != http.StatusServiceUnavailable {
		t.Fatalf("degraded probe mismatch: %+v", checks[1])
	}
	for _, check := range checks {
		if err := check.Validate(); err != nil {
			t.Fatalf("invalid probe evidence: %v", err)
		}
	}
}

func TestWebLinkProberClassifiesTLSFailureWithoutRawError(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	prober := newWebLinkProber()
	checks := prober.Probe(context.Background(), []webLinkTarget{{
		hostID: "host-a", workloadKey: "docker:web", url: server.URL,
	}}, time.Now().UTC())
	if len(checks) != 1 || checks[0].State != domain.WebLinkUnreachable || checks[0].ErrorKind != "tls" {
		t.Fatalf("TLS failure was not sanitized: %+v", checks)
	}
}

func TestCurrentWebLinkTargetsDeduplicatesPrimaryRoute(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	if err := store.SetAgents(ctx, []AgentConfig{{ID: "host-a", PublicHost: "host-a.example.test"}}); err != nil {
		t.Fatal(err)
	}
	services := []shared.Service{{
		Key: "systemd:caddy.service", Kind: shared.KindSystemd, Name: "caddy",
		Routes:      []shared.ProxyRoute{{Scheme: "https", Host: "app.example.test", Port: 443, Path: "/"}},
		MaxExposure: shared.ExposurePublic,
	}}
	if err := store.Update(ctx, "host-a", true, "", shared.Ping{}, nil, services, time.Now()); err != nil {
		t.Fatal(err)
	}
	targets := currentWebLinkTargets(store)
	if len(targets) != 1 || targets[0].url != "https://app.example.test/" {
		t.Fatalf("Web link targets = %+v", targets)
	}
}
