package hub

import (
	"testing"
	"time"

	"github.com/toolazytoname/lodge/internal/domain"
	"github.com/toolazytoname/lodge/internal/shared"
)

func TestProjectObservationSeparatesBindingFromReachability(t *testing.T) {
	now := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	observation, err := projectObservation(
		AgentConfig{ID: "host-a", Name: "Host A"},
		true,
		"",
		shared.Ping{Hostname: "host-a", AgentVer: "0.1.0"},
		&shared.Status{Hostname: "host-a", Load: shared.Load{CPUs: 4, One: 0.5}},
		[]shared.Service{{
			Key: "docker:web", Kind: shared.KindDocker, Name: "web", Status: "running",
			ComposeProject: "site", ComposeService: "web",
			Ports: []shared.Port{
				{Proto: "tcp", Bind: "0.0.0.0", Port: 443, Exposure: shared.ExposurePublic},
				{Proto: "tcp6", Bind: "100.105.1.2", Port: 8080, Exposure: shared.ExposureTailnet},
			},
			Routes: []shared.ProxyRoute{{
				Scheme: "https", Host: "web.example.test", Port: 443, Path: "/",
				Upstreams: []string{"127.0.0.1:8080"},
			}},
		}},
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(observation.Workloads) != 1 || len(observation.Endpoints) != 2 {
		t.Fatalf("unexpected projection sizes: %+v", observation)
	}
	if observation.Endpoints[0].Binding != domain.BindingWildcard {
		t.Fatalf("wildcard bind classified as %q", observation.Endpoints[0].Binding)
	}
	if observation.Endpoints[0].Reachability != domain.ReachabilityUnknown {
		t.Fatal("wildcard binding must not imply public reachability")
	}
	if observation.Endpoints[1].Binding != domain.BindingTailnet || observation.Endpoints[1].Protocol != "tcp" {
		t.Fatalf("tailnet endpoint projection failed: %+v", observation.Endpoints[1])
	}
	if observation.Resources == nil || observation.Resources.CPUs != 4 {
		t.Fatalf("resource projection failed: %+v", observation.Resources)
	}
	if observation.Workloads[0].ComposeProject != "site" || observation.Workloads[0].ComposeService != "web" {
		t.Fatalf("Compose identity projection failed: %+v", observation.Workloads[0])
	}
	if len(observation.Routes) != 1 || observation.Routes[0].WorkloadKey != "docker:web" || observation.Routes[0].Host != "web.example.test" {
		t.Fatalf("proxy route projection failed: %+v", observation.Routes)
	}
}

func TestProjectObservationRejectsDuplicateWorkloadIdentity(t *testing.T) {
	services := []shared.Service{
		{Key: "systemd:caddy.service", Kind: shared.KindSystemd, Name: "caddy"},
		{Key: "systemd:caddy.service", Kind: shared.KindSystemd, Name: "duplicate"},
	}
	if _, err := projectObservation(AgentConfig{ID: "host-a"}, true, "", shared.Ping{}, nil, services, time.Now()); err == nil {
		t.Fatal("duplicate workload identity should fail projection")
	}
}

func TestProjectObservationDeduplicatesExactEndpoints(t *testing.T) {
	port := shared.Port{Proto: "tcp", Bind: "127.0.0.1", Port: 8080, Exposure: shared.ExposureLocal}
	observation, err := projectObservation(
		AgentConfig{ID: "host-a"}, true, "", shared.Ping{}, nil,
		[]shared.Service{{Key: "process:web", Kind: shared.KindProcess, Name: "web", Ports: []shared.Port{port, port}}},
		time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(observation.Endpoints) != 1 {
		t.Fatalf("exact endpoint duplicates were not collapsed: %+v", observation.Endpoints)
	}
}
