package hub

import (
	"testing"
	"time"

	"github.com/toolazytoname/lodge/internal/domain"
)

func eventRuleObservation(at time.Time) domain.Observation {
	return domain.Observation{
		HostID: "host-a", ObservedAt: at, Online: true,
		Resources: &domain.Resources{
			CPUs: 4, Load1: 1,
			Memory: domain.MemoryResources{TotalBytes: 100, UsedBytes: 50},
			Disks:  []domain.DiskResources{{Mount: "/", TotalBytes: 100, UsedBytes: 50}},
		},
		Workloads: []domain.Workload{{
			HostID: "host-a", Key: "docker:web", Kind: domain.WorkloadDocker,
			Name: "web", State: "running",
		}},
		Endpoints: []domain.Endpoint{{
			HostID: "host-a", WorkloadKey: "docker:web", Key: "tcp://0.0.0.0:443",
			Protocol: "tcp", Bind: "0.0.0.0", Port: 443,
			Binding: domain.BindingWildcard, Reachability: domain.ReachabilityUnknown,
		}},
	}
}

func TestEventRulesBaselineAndTrackNewWildcardListener(t *testing.T) {
	now := time.Now().UTC()
	first := eventRuleObservation(now)
	if signals := evaluateEventSignals(nil, first, nil); len(signals) != 0 {
		t.Fatalf("first observation should establish a listener baseline: %+v", signals)
	}

	second := eventRuleObservation(now.Add(time.Minute))
	second.Endpoints = append(second.Endpoints, domain.Endpoint{
		HostID: "host-a", WorkloadKey: "docker:web", Key: "tcp://0.0.0.0:8443",
		Protocol: "tcp", Bind: "0.0.0.0", Port: 8443,
		Binding: domain.BindingWildcard, Reachability: domain.ReachabilityUnknown,
	})
	signals := evaluateEventSignals(&first, second, nil)
	if len(signals) != 1 || signals[0].Kind != "listener.added" || signals[0].DedupeKey != "host-a:listener:tcp://0.0.0.0:8443" {
		t.Fatalf("new wildcard listener was not isolated: %+v", signals)
	}

	active := domain.Event{
		ID: "evt_listener", HostID: "host-a", Kind: signals[0].Kind, Severity: signals[0].Severity,
		State: domain.EventActive, DedupeKey: signals[0].DedupeKey, Title: signals[0].Title,
		Detail: signals[0].Detail, FirstObservedAt: second.ObservedAt, LastObservedAt: second.ObservedAt,
	}
	offline := domain.Observation{HostID: "host-a", ObservedAt: now.Add(2 * time.Minute), Online: false, LastError: "timeout"}
	signals = evaluateEventSignals(&second, offline, []domain.Event{active})
	if len(signals) != 2 || signals[0].DedupeKey != "host-a:host:offline" || signals[1].DedupeKey != active.DedupeKey {
		t.Fatalf("offline observation should carry listener state and add host event: %+v", signals)
	}

	offlineEvent := domain.Event{
		ID: "evt_offline", HostID: "host-a", Kind: "host.offline", Severity: domain.SeverityCritical,
		State: domain.EventActive, DedupeKey: "host-a:host:offline", Title: "offline",
		FirstObservedAt: offline.ObservedAt, LastObservedAt: offline.ObservedAt,
	}
	recovered := second
	recovered.ObservedAt = now.Add(3 * time.Minute)
	signals = evaluateEventSignals(&offline, recovered, []domain.Event{active, offlineEvent})
	if len(signals) != 1 || signals[0].DedupeKey != active.DedupeKey {
		t.Fatalf("recovery should retain existing listener risk without reopening baseline listeners: %+v", signals)
	}
}

func TestEventRulesUseHysteresisAndFailedWorkloads(t *testing.T) {
	now := time.Now().UTC()
	observation := eventRuleObservation(now)
	observation.Resources.Memory.UsedBytes = 86
	observation.Resources.Disks[0].UsedBytes = 92
	observation.Resources.Load1 = 6.4
	observation.Workloads[0].State = "failed"
	signals := evaluateEventSignals(nil, observation, nil)
	if len(signals) != 4 {
		t.Fatalf("threshold and workload rules emitted %d signals: %+v", len(signals), signals)
	}
	active := make([]domain.Event, 0, len(signals))
	for index, signal := range signals {
		active = append(active, domain.Event{
			ID: "evt_active_" + string(rune('a'+index)), HostID: signal.HostID,
			Kind: signal.Kind, Severity: signal.Severity, State: domain.EventActive,
			DedupeKey: signal.DedupeKey, Title: signal.Title, Detail: signal.Detail,
			FirstObservedAt: now, LastObservedAt: now,
		})
	}

	between := eventRuleObservation(now.Add(time.Minute))
	between.Resources.Memory.UsedBytes = 82
	between.Resources.Disks[0].UsedBytes = 87
	between.Resources.Load1 = 4.4
	signals = evaluateEventSignals(&observation, between, active)
	if len(signals) != 3 {
		t.Fatalf("resource events should remain active inside hysteresis band: %+v", signals)
	}

	clear := eventRuleObservation(now.Add(2 * time.Minute))
	clear.Resources.Memory.UsedBytes = 79
	clear.Resources.Disks[0].UsedBytes = 84
	clear.Resources.Load1 = 3.6
	if signals := evaluateEventSignals(&between, clear, active); len(signals) != 0 {
		t.Fatalf("conditions below clear thresholds should recover: %+v", signals)
	}
}

func TestEventRulesDoNotInferRecoveryFromMissingTelemetry(t *testing.T) {
	now := time.Now().UTC()
	current := eventRuleObservation(now)
	current.Resources = nil
	current.Workloads = nil
	current.Endpoints = nil
	active := []domain.Event{
		{ID: "evt_memory", HostID: "host-a", Kind: "resource.memory", Severity: domain.SeverityWarning, State: domain.EventActive, DedupeKey: "host-a:resource:memory", Title: "memory", FirstObservedAt: now.Add(-time.Minute), LastObservedAt: now.Add(-time.Minute)},
		{ID: "evt_workload", HostID: "host-a", Kind: "workload.failed", Severity: domain.SeverityCritical, State: domain.EventActive, DedupeKey: "host-a:workload:docker:web:failed", Title: "workload", FirstObservedAt: now.Add(-time.Minute), LastObservedAt: now.Add(-time.Minute)},
		{ID: "evt_listener", HostID: "host-a", Kind: "listener.added", Severity: domain.SeverityWarning, State: domain.EventActive, DedupeKey: "host-a:listener:tcp://0.0.0.0:8443", Title: "listener", FirstObservedAt: now.Add(-time.Minute), LastObservedAt: now.Add(-time.Minute)},
	}
	signals := evaluateEventSignals(nil, current, active)
	if len(signals) != len(active) {
		t.Fatalf("partial collection incorrectly recovered active conditions: %+v", signals)
	}
}
