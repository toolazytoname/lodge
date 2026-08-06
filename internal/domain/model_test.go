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
