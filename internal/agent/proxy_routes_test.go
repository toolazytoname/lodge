package agent

import (
	"bytes"
	"strings"
	"testing"

	"github.com/toolazytoname/lodge/internal/shared"
)

func TestParseCaddyRoutesExtractsOnlyExternalRouteAndSafeAuthority(t *testing.T) {
	content := []byte(`{
	email operator@example.test
}

:80 {
	reverse_proxy 127.0.0.1:3000
}

lodge.example.test:8443 {
	reverse_proxy 127.0.0.1:9102
}

api.example.test:9443 {
	handle /v1/* {
		reverse_proxy https://user:secret@127.0.0.1:7000
	}
}
`)
	routes, err := parseCaddyRoutes(content, "systemd:caddy.service")
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 3 {
		t.Fatalf("expected three Caddy routes, got %+v", routes)
	}
	byHost := make(map[string]discoveredProxyRoute)
	for _, route := range routes {
		byHost[route.Route.Host] = route
	}
	if route := byHost[""]; route.Route.Scheme != "http" || route.Route.Port != 80 || route.Route.Upstreams[0] != "127.0.0.1:3000" {
		t.Fatalf("default Caddy route was parsed incorrectly: %+v", route)
	}
	if route := byHost["lodge.example.test"]; route.Route.Port != 8443 || route.Route.Upstreams[0] != "127.0.0.1:9102" {
		t.Fatalf("named Caddy route was parsed incorrectly: %+v", route)
	}
	if route := byHost["api.example.test"]; route.Route.Path != "/v1/*" || len(route.Route.Upstreams) != 0 {
		t.Fatalf("credential-bearing upstream should be redacted, got %+v", route)
	}
}

func TestParseNginxRoutesHandlesDefaultHostTLSAndLocations(t *testing.T) {
	content := []byte(`
events {}
http {
  server {
    listen 443 ssl default_server;
    server_name _;
    location / { proxy_pass http://127.0.0.1:3001; }
  }
  server {
    listen 443 ssl;
    server_name happy.example.test happy.local;
    location /refresh { proxy_pass http://127.0.0.1:8791/private; }
    location /dynamic { proxy_pass http://$runtime_backend; }
  }
}
`)
	routes, err := parseNginxRoutes(content, "docker:nginx")
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 5 {
		t.Fatalf("expected one default and four named routes, got %+v", routes)
	}
	var defaultFound, redactedFound bool
	for _, route := range routes {
		if route.Route.Host == "" && route.Route.Path == "/" && len(route.Route.Upstreams) == 1 {
			defaultFound = route.Route.Kind == shared.RouteKindProxy && route.Route.Upstreams[0] == "127.0.0.1:3001"
		}
		if route.Route.Host == "happy.example.test" && route.Route.Path == "/dynamic" {
			redactedFound = len(route.Route.Upstreams) == 0
		}
	}
	if !defaultFound || !redactedFound {
		t.Fatalf("Nginx default or redacted route missing: %+v", routes)
	}
}

func TestParseNginxRoutesDiscoversNamedStaticSiteBesideProtectedProxy(t *testing.T) {
	content := []byte(`
events {}
http {
  server {
    listen 443 ssl;
    server_name quota.example.test;
    location / {
      auth_basic "Protected";
      root /var/www/quota;
      index index.html;
    }
    location = /refresh {
      auth_basic "Protected";
      proxy_pass http://127.0.0.1:8791/refresh;
    }
  }
}
`)
	routes, err := parseNginxRoutes(content, "systemd:nginx.service")
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected static root and refresh proxy, got %+v", routes)
	}
	byPath := make(map[string]shared.ProxyRoute)
	for _, route := range routes {
		byPath[route.Route.Path] = route.Route
	}
	if root := byPath["/"]; root.Kind != shared.RouteKindStatic || root.Host != "quota.example.test" || len(root.Upstreams) != 0 {
		t.Fatalf("static site route mismatch: %+v", root)
	}
	if refresh := byPath["/refresh"]; refresh.Kind != shared.RouteKindProxy || len(refresh.Upstreams) != 1 || refresh.Upstreams[0] != "127.0.0.1:8791" {
		t.Fatalf("refresh proxy route mismatch: %+v", refresh)
	}
}

func TestProxyDiscoveryOutputNeverContainsRejectedSecretsOrPaths(t *testing.T) {
	discovery := proxyDiscovery{
		Routes: []discoveredProxyRoute{{
			WorkloadKey: "docker:caddy",
			Route: shared.ProxyRoute{Scheme: "https", Host: "app.example.test", Port: 443, Path: "/", Upstreams: []string{
				"127.0.0.1:3000", "https://user:password@example.test:8443/private?token=secret",
			}},
		}},
		Warnings: []string{"Docker Caddy imports are not expanded"},
	}
	var output bytes.Buffer
	if err := writeProxyDiscovery(discovery, &output); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"password", "token=", "/private", "user:"} {
		if strings.Contains(output.String(), forbidden) {
			t.Fatalf("helper output leaked %q: %s", forbidden, output.String())
		}
	}
	parsed := parseProxyDiscovery(output.Bytes())
	if len(parsed.Routes) != 1 || len(parsed.Routes[0].Route.Upstreams) != 1 || len(parsed.Warnings) != 1 {
		t.Fatalf("sanitized discovery did not round trip: %+v", parsed)
	}
}

func TestParseProxyDiscoveryRejectsUnknownFieldsAndInvalidHosts(t *testing.T) {
	content := []byte(
		`{"type":"route","workloadKey":"docker:caddy","scheme":"https","host":"safe.example.test","port":443,"path":"/"}` + "\n" +
			`{"type":"route","workloadKey":"docker:caddy","scheme":"https","host":"evil host","port":443,"path":"/"}` + "\n" +
			`{"type":"route","workloadKey":"docker:caddy","scheme":"https","host":"safe.example.test","port":443,"path":"/","secret":"value"}` + "\n",
	)
	discovery := parseProxyDiscovery(content)
	if len(discovery.Routes) != 1 || discovery.Routes[0].Route.Host != "safe.example.test" {
		t.Fatalf("invalid records were not rejected: %+v", discovery)
	}
}
