package hub

import (
	"testing"
	"time"

	"github.com/toolazytoname/lodge/internal/domain"
)

type listenerEval struct {
	complete    map[string]struct{}
	established bool
}

func newListenerEval() *listenerEval {
	return &listenerEval{complete: map[string]struct{}{}}
}

func (eval *listenerEval) eval(previous *domain.Observation, current domain.Observation, active []domain.Event) []domain.EventSignal {
	signals := evaluateEventSignals(previous, current, active, eval.complete, eval.established)
	if current.Online && !listenerTelemetryMissing(current) {
		eval.complete = listenerWildcardKeys(current)
		eval.established = true
	}
	return signals
}

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
	eval := newListenerEval()
	now := time.Now().UTC()
	first := eventRuleObservation(now)
	if signals := eval.eval(nil, first, nil); len(signals) != 0 {
		t.Fatalf("first observation should establish a listener baseline: %+v", signals)
	}

	second := eventRuleObservation(now.Add(time.Minute))
	second.Endpoints = append(second.Endpoints, domain.Endpoint{
		HostID: "host-a", WorkloadKey: "docker:web", Key: "tcp://0.0.0.0:8443",
		Protocol: "tcp", Bind: "0.0.0.0", Port: 8443,
		Binding: domain.BindingWildcard, Reachability: domain.ReachabilityUnknown,
	})
	signals := eval.eval(&first, second, nil)
	if len(signals) != 1 || signals[0].Kind != "listener.added" || signals[0].DedupeKey != "host-a:listener:tcp://0.0.0.0:8443" {
		t.Fatalf("new wildcard listener was not isolated: %+v", signals)
	}

	active := domain.Event{
		ID: "evt_listener", HostID: "host-a", Kind: signals[0].Kind, Severity: signals[0].Severity,
		State: domain.EventActive, DedupeKey: signals[0].DedupeKey, Title: signals[0].Title,
		Detail: signals[0].Detail, FirstObservedAt: second.ObservedAt, LastObservedAt: second.ObservedAt,
	}
	offline := domain.Observation{HostID: "host-a", ObservedAt: now.Add(2 * time.Minute), Online: false, LastError: "timeout"}
	signals = eval.eval(&second, offline, []domain.Event{active})
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
	signals = eval.eval(&offline, recovered, []domain.Event{active, offlineEvent})
	if len(signals) != 1 || signals[0].DedupeKey != active.DedupeKey {
		t.Fatalf("recovery should retain existing listener risk without reopening baseline listeners: %+v", signals)
	}
}

func TestEventRulesUseHysteresisAndFailedWorkloads(t *testing.T) {
	eval := newListenerEval()
	now := time.Now().UTC()
	observation := eventRuleObservation(now)
	observation.Resources.Memory.UsedBytes = 86
	observation.Resources.Disks[0].UsedBytes = 92
	observation.Resources.Load1 = 6.4
	observation.Workloads[0].State = "failed"
	signals := eval.eval(nil, observation, nil)
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
	signals = eval.eval(&observation, between, active)
	if len(signals) != 3 {
		t.Fatalf("resource events should remain active inside hysteresis band: %+v", signals)
	}

	clear := eventRuleObservation(now.Add(2 * time.Minute))
	clear.Resources.Memory.UsedBytes = 79
	clear.Resources.Disks[0].UsedBytes = 84
	clear.Resources.Load1 = 3.6
	if signals := eval.eval(&between, clear, active); len(signals) != 0 {
		t.Fatalf("conditions below clear thresholds should recover: %+v", signals)
	}
}

func TestEventRulesDoNotRecoverFromZeroedOrWarnedResourceCollection(t *testing.T) {
	eval := newListenerEval()
	now := time.Now().UTC()
	healthy := eventRuleObservation(now)
	active := []domain.Event{
		{ID: "evt_memory", HostID: "host-a", Kind: "resource.memory", Severity: domain.SeverityWarning, State: domain.EventActive, DedupeKey: "host-a:resource:memory", Title: "memory", FirstObservedAt: now.Add(-time.Minute), LastObservedAt: now.Add(-time.Minute)},
		{ID: "evt_disk", HostID: "host-a", Kind: "resource.disk", Severity: domain.SeverityWarning, State: domain.EventActive, DedupeKey: "host-a:resource:disk:/", Title: "disk", FirstObservedAt: now.Add(-time.Minute), LastObservedAt: now.Add(-time.Minute)},
		{ID: "evt_load", HostID: "host-a", Kind: "resource.load", Severity: domain.SeverityWarning, State: domain.EventActive, DedupeKey: "host-a:resource:load", Title: "load", FirstObservedAt: now.Add(-time.Minute), LastObservedAt: now.Add(-time.Minute)},
	}

	eval.complete = listenerWildcardKeys(healthy)
	eval.established = true

	zeroed := eventRuleObservation(now)
	zeroed.Resources.Memory = domain.MemoryResources{}
	zeroed.Resources.Disks = nil
	zeroed.Resources.Load1 = 0
	zeroed.Warnings = []string{"读取 /proc/loadavg 失败: permission denied"}
	signals := eval.eval(&healthy, zeroed, active)
	if len(signals) != 3 {
		t.Fatalf("zeroed resource collection recovered alerts: %+v", signals)
	}

	warned := eventRuleObservation(now)
	warned.Resources.Memory.UsedBytes = 10
	warned.Resources.Disks[0].UsedBytes = 10
	warned.Resources.Load1 = 0.1
	warned.Warnings = []string{"读取 /proc/meminfo 失败: io error", "采集磁盘失败: statfs failed", "读取 /proc/loadavg 失败: io error"}
	signals = eval.eval(&healthy, warned, active)
	if len(signals) != 3 {
		t.Fatalf("warned resource collection recovered alerts: %+v", signals)
	}
}

func TestEventRulesDoNotRecoverFromPartialServiceDiscovery(t *testing.T) {
	eval := newListenerEval()
	now := time.Now().UTC()
	previous := eventRuleObservation(now)
	previous.Workloads = append(previous.Workloads, domain.Workload{
		HostID: "host-a", Key: "systemd:caddy.service", Kind: domain.WorkloadSystemd,
		Name: "caddy", State: "failed",
	})
	active := []domain.Event{
		{ID: "evt_docker", HostID: "host-a", Kind: "workload.failed", Severity: domain.SeverityCritical, State: domain.EventActive, DedupeKey: "host-a:workload:docker:web:failed", Title: "web", FirstObservedAt: now.Add(-time.Minute), LastObservedAt: now.Add(-time.Minute)},
		{ID: "evt_unit", HostID: "host-a", Kind: "workload.failed", Severity: domain.SeverityCritical, State: domain.EventActive, DedupeKey: "host-a:workload:systemd:caddy.service:failed", Title: "caddy", FirstObservedAt: now.Add(-time.Minute), LastObservedAt: now.Add(-time.Minute)},
		{ID: "evt_listener", HostID: "host-a", Kind: "listener.added", Severity: domain.SeverityWarning, State: domain.EventActive, DedupeKey: "host-a:listener:tcp://0.0.0.0:443", Title: "listener", FirstObservedAt: now.Add(-time.Minute), LastObservedAt: now.Add(-time.Minute)},
	}

	partial := eventRuleObservation(now.Add(time.Minute))
	partial.Workloads = []domain.Workload{{
		HostID: "host-a", Key: "systemd:caddy.service", Kind: domain.WorkloadSystemd,
		Name: "caddy", State: "running",
	}}
	partial.Endpoints = nil
	partial.Warnings = []string{"docker ps 失败: permission denied", "ss 采集失败（端口维度将缺失）: sudoers"}
	signals := eval.eval(&previous, partial, active)
	if len(signals) != 2 {
		t.Fatalf("partial discovery should keep docker and listener risk: %+v", signals)
	}
	if signals[0].DedupeKey != "host-a:listener:tcp://0.0.0.0:443" || signals[1].DedupeKey != "host-a:workload:docker:web:failed" {
		t.Fatalf("partial discovery carried the wrong risks: %+v", signals)
	}
}

func TestEventRulesDoNotInferRecoveryFromMissingTelemetry(t *testing.T) {
	eval := newListenerEval()
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
	signals := eval.eval(nil, current, active)
	if len(signals) != len(active) {
		t.Fatalf("partial collection incorrectly recovered active conditions: %+v", signals)
	}
}

func TestEventRulesDetectSSHFailureSpikeWithSourcesAndHysteresis(t *testing.T) {
	eval := newListenerEval()
	now := time.Now().UTC().Truncate(time.Second)
	observation := eventRuleObservation(now)
	observation.SSH = &domain.SSHAuthObservation{
		WindowStart: now.Add(-10 * time.Minute), WindowEnd: now, FailedTotal: 34,
		Sources: []domain.SSHAuthSource{
			{Address: "2001:db8::5", Count: 3},
			{Address: "203.0.113.9", Count: 26},
			{Address: "198.51.100.8", Count: 5},
		},
	}
	signals := eval.eval(nil, observation, nil)
	if len(signals) != 1 || signals[0].Kind != "ssh.bruteforce" || signals[0].Severity != domain.SeverityWarning {
		t.Fatalf("SSH spike did not open one warning: %+v", signals)
	}
	if signals[0].DedupeKey != "host-a:ssh:authentication-failures" || signals[0].Title != "SSH 认证失败突增" {
		t.Fatalf("SSH signal identity mismatch: %+v", signals[0])
	}
	if want := "10 分钟内 SSH 认证失败 34 次；主要来源 203.0.113.9 × 26、198.51.100.8 × 5、2001:db8::5 × 3"; signals[0].Detail != want {
		t.Fatalf("SSH source detail = %q, want %q", signals[0].Detail, want)
	}
	active := domain.Event{
		ID: "evt_ssh", HostID: "host-a", Kind: signals[0].Kind, Severity: signals[0].Severity,
		State: domain.EventActive, DedupeKey: signals[0].DedupeKey, Title: signals[0].Title, Detail: signals[0].Detail,
		FirstObservedAt: now, LastObservedAt: now,
	}

	between := eventRuleObservation(now.Add(time.Minute))
	between.SSH = &domain.SSHAuthObservation{
		WindowStart: now.Add(-9 * time.Minute), WindowEnd: now.Add(time.Minute), FailedTotal: 12,
		Sources: []domain.SSHAuthSource{{Address: "203.0.113.9", Count: 3}, {Address: "198.51.100.8", Count: 9}},
	}
	if signals := eval.eval(&observation, between, []domain.Event{active}); len(signals) != 1 || signals[0].Kind != "ssh.bruteforce" {
		t.Fatalf("SSH event did not remain active inside hysteresis band: %+v", signals)
	}

	missing := eventRuleObservation(now.Add(2 * time.Minute))
	if signals := eval.eval(&between, missing, []domain.Event{active}); len(signals) != 1 || signals[0].Detail != active.Detail {
		t.Fatalf("missing SSH telemetry incorrectly recovered or rewrote the event: %+v", signals)
	}

	clear := eventRuleObservation(now.Add(3 * time.Minute))
	clear.SSH = &domain.SSHAuthObservation{
		WindowStart: now.Add(-7 * time.Minute), WindowEnd: now.Add(3 * time.Minute), FailedTotal: 2,
		Sources: []domain.SSHAuthSource{{Address: "203.0.113.9", Count: 2}},
	}
	if signals := eval.eval(&missing, clear, []domain.Event{active}); len(signals) != 0 {
		t.Fatalf("quiet SSH window did not recover the event: %+v", signals)
	}

	critical := observation
	critical.ObservedAt = now.Add(4 * time.Minute)
	critical.SSH = &domain.SSHAuthObservation{
		WindowStart: now.Add(-6 * time.Minute), WindowEnd: now.Add(4 * time.Minute), FailedTotal: 100,
		Sources: []domain.SSHAuthSource{{Address: "203.0.113.9", Count: 60}, {Address: "198.51.100.8", Count: 40}},
	}
	if signals := eval.eval(&clear, critical, nil); len(signals) != 1 || signals[0].Severity != domain.SeverityCritical {
		t.Fatalf("critical SSH spike was not escalated: %+v", signals)
	}
}

func TestEventRulesKeepCompleteListenerBaselineAcrossPartialCollection(t *testing.T) {
	eval := newListenerEval()
	now := time.Now().UTC()
	baseline := eventRuleObservation(now)
	if signals := eval.eval(nil, baseline, nil); len(signals) != 0 {
		t.Fatalf("first complete collection should be a listener baseline: %+v", signals)
	}

	partial := eventRuleObservation(now.Add(time.Minute))
	partial.Endpoints = nil
	partial.Warnings = []string{"ss 采集失败（端口维度将缺失）: denied"}
	if signals := eval.eval(&baseline, partial, nil); len(signals) != 0 {
		t.Fatalf("failed port scrape should not invent listeners: %+v", signals)
	}

	recovered := eventRuleObservation(now.Add(2 * time.Minute))
	if signals := eval.eval(&partial, recovered, nil); len(signals) != 0 {
		t.Fatalf("unchanged listener falsely reported as new after failed scrape: %+v", signals)
	}
}

func TestEventRulesAlertNewDockerListenerDuringSSFailure(t *testing.T) {
	eval := newListenerEval()
	now := time.Now().UTC()
	baseline := eventRuleObservation(now)
	if signals := eval.eval(nil, baseline, nil); len(signals) != 0 {
		t.Fatalf("first complete collection should be a listener baseline: %+v", signals)
	}

	partial := eventRuleObservation(now.Add(time.Minute))
	partial.Warnings = []string{"ss 采集失败（端口维度将缺失）: denied"}
	partial.Endpoints = append(partial.Endpoints, domain.Endpoint{
		HostID: "host-a", WorkloadKey: "docker:web", Key: "tcp://0.0.0.0:8443",
		Protocol: "tcp", Bind: "0.0.0.0", Port: 8443,
		Binding: domain.BindingWildcard, Reachability: domain.ReachabilityUnknown,
	})
	first := eval.eval(&baseline, partial, nil)
	recovered := partial
	recovered.ObservedAt = now.Add(2 * time.Minute)
	recovered.Warnings = nil
	second := eval.eval(&partial, recovered, nil)
	for _, signal := range append(append([]domain.EventSignal{}, first...), second...) {
		if signal.DedupeKey == "host-a:listener:tcp://0.0.0.0:8443" {
			return
		}
	}
	t.Fatalf("new Docker listener never alerted, even after recovery: partial=%+v recovered=%+v", first, second)
}

func TestEventRulesFirstPartialCollectionIsNotProofOfNewListener(t *testing.T) {
	current := eventRuleObservation(time.Now().UTC())
	current.Warnings = []string{"ss 采集失败: denied"}
	signals := evaluateEventSignals(nil, current, nil, map[string]struct{}{}, false)
	if len(signals) != 0 {
		t.Fatalf("first-ever partial collection invented listener novelty: %+v", signals)
	}
}

func TestEventRulesEstablishedEmptyBaselineAlertsNewPort(t *testing.T) {
	eval := newListenerEval()
	now := time.Now().UTC()
	empty := eventRuleObservation(now)
	empty.Endpoints = nil
	if signals := eval.eval(nil, empty, nil); len(signals) != 0 {
		t.Fatalf("complete collection with no wildcards should establish an empty baseline: %+v", signals)
	}
	if !eval.established || len(eval.complete) != 0 {
		t.Fatalf("empty baseline was not established: established=%v keys=%d", eval.established, len(eval.complete))
	}

	added := eventRuleObservation(now.Add(time.Minute))
	signals := eval.eval(&empty, added, nil)
	if len(signals) != 1 || signals[0].DedupeKey != "host-a:listener:tcp://0.0.0.0:443" {
		t.Fatalf("port added after an established empty baseline was not alerted: %+v", signals)
	}
}
