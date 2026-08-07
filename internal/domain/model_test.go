package domain

import (
	"testing"
	"time"
)

func TestObservationRequiresReachabilityEvidence(t *testing.T) {
	now := time.Now().UTC()
	observation := Observation{
		HostID:     "host-a",
		ObservedAt: now,
		Workloads:  []Workload{{HostID: "host-a", Key: "docker:web", Kind: WorkloadDocker, Name: "web"}},
		Endpoints: []Endpoint{{
			HostID: "host-a", WorkloadKey: "docker:web", Key: "tcp://0.0.0.0:443",
			Protocol: "tcp", Bind: "0.0.0.0", Port: 443,
			Binding: BindingWildcard, Reachability: ReachabilityPublic,
		}},
	}
	if err := observation.Validate(); err == nil {
		t.Fatal("confirmed reachability without evidence should be rejected")
	}

	observation.Endpoints[0].ReachabilitySource = "external-probe:edge-a"
	observation.Endpoints[0].ReachabilityCheckedAt = &now
	if err := observation.Validate(); err != nil {
		t.Fatalf("evidenced reachability was rejected: %v", err)
	}
}

func TestObservationRejectsCrossHostReferences(t *testing.T) {
	observation := Observation{
		HostID:     "host-a",
		ObservedAt: time.Now().UTC(),
		Workloads:  []Workload{{HostID: "host-b", Key: "docker:web", Kind: WorkloadDocker, Name: "web"}},
	}
	if err := observation.Validate(); err == nil {
		t.Fatal("cross-host workload should be rejected")
	}
}

func TestObservationValidatesComposeIdentity(t *testing.T) {
	observation := Observation{
		HostID: "host-a", ObservedAt: time.Now().UTC(),
		Workloads: []Workload{{
			HostID: "host-a", Key: "docker:web", Kind: WorkloadDocker, Name: "web",
			ComposeProject: "site", ComposeService: "web",
		}},
	}
	if err := observation.Validate(); err != nil {
		t.Fatalf("valid Compose identity was rejected: %v", err)
	}
	observation.Workloads[0].ComposeProject = ""
	if err := observation.Validate(); err == nil {
		t.Fatal("Compose service without project should be rejected")
	}
	observation.Workloads[0].ComposeProject = "site"
	observation.Workloads[0].Kind = WorkloadSystemd
	if err := observation.Validate(); err == nil {
		t.Fatal("Compose identity on a non-Docker workload should be rejected")
	}
}

func TestObservationValidatesRedactedProxyRoutes(t *testing.T) {
	observation := Observation{
		HostID: "host-a", ObservedAt: time.Now().UTC(),
		Workloads: []Workload{{HostID: "host-a", Key: "systemd:caddy.service", Kind: WorkloadSystemd, Name: "caddy"}},
		Routes: []ProxyRoute{{
			HostID: "host-a", WorkloadKey: "systemd:caddy.service", Key: "https://app.example.test:443/",
			Scheme: "https", Host: "app.example.test", Port: 443, Path: "/", Upstreams: []string{"127.0.0.1:3000"},
		}},
	}
	if err := observation.Validate(); err != nil {
		t.Fatalf("valid redacted proxy route was rejected: %v", err)
	}
	observation.Routes[0].Key = "https://other.example.test:443/"
	if err := observation.Validate(); err == nil {
		t.Fatal("route key inconsistent with its fields should be rejected")
	}
	observation.Routes[0].Key = "https://app.example.test:443/"
	observation.Routes[0].Upstreams = []string{"https://user:secret@example.test"}
	if err := observation.Validate(); err == nil {
		t.Fatal("credential-bearing upstream should be rejected")
	}
	observation.Routes[0].Upstreams = nil
	observation.Routes[0].WorkloadKey = "docker:missing"
	if err := observation.Validate(); err == nil {
		t.Fatal("route referencing a missing workload should be rejected")
	}
}

func TestAnnotationRequiresSafeDurableIdentityAndURL(t *testing.T) {
	annotation := Annotation{
		HostID: "host-a", WorkloadKey: "systemd:caddy.service",
		URL: "https://admin.example.test/path", UpdatedAt: time.Now().UTC(),
	}
	if err := annotation.Validate(); err != nil {
		t.Fatalf("valid annotation was rejected: %v", err)
	}
	annotation.URL = "https://user:password@example.test"
	if err := annotation.Validate(); err == nil {
		t.Fatal("URL credentials should be rejected")
	}
	annotation.URL = "javascript:alert(1)"
	if err := annotation.Validate(); err == nil {
		t.Fatal("non-http annotation URL should be rejected")
	}
}
