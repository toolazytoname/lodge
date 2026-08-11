package domain

import (
	"strings"
	"testing"
	"time"
)

func TestOperationLifecycleValidation(t *testing.T) {
	requestedAt := time.Date(2026, 8, 8, 1, 2, 3, 0, time.UTC)
	operation := Operation{
		ID: "op_0123456789abcdef", HostID: "host-a", WorkloadKey: "systemd:caddy.service",
		Kind: OperationRestart, State: OperationRequested, RequestedBy: "session:0123456789abcdef",
		RequestedAt: requestedAt,
	}
	if err := operation.Validate(); err != nil {
		t.Fatalf("valid requested operation rejected: %v", err)
	}
	startedAt := requestedAt.Add(time.Second)
	operation.State, operation.StartedAt = OperationRunning, &startedAt
	if err := operation.Validate(); err != nil {
		t.Fatalf("valid running operation rejected: %v", err)
	}
	finishedAt := startedAt.Add(2 * time.Second)
	operation.State, operation.FinishedAt = OperationSucceeded, &finishedAt
	operation.ResultSummary = "Caddy：running → running"
	if err := operation.Validate(); err != nil {
		t.Fatalf("valid successful operation rejected: %v", err)
	}

	failed := operation
	failed.State, failed.ResultSummary, failed.Error = OperationFailed, "", "health_verification_failed"
	if err := failed.Validate(); err != nil {
		t.Fatalf("valid failed operation rejected: %v", err)
	}
	failed.Error = "raw stderr: password=secret"
	if err := failed.Validate(); err == nil {
		t.Fatal("raw error text was accepted as an audit category")
	}

	rolledBack := operation
	rolledBack.Kind, rolledBack.WorkloadKey = OperationDeploy, "gateway"
	rolledBack.State, rolledBack.ResultSummary, rolledBack.Error = OperationRolledBack, "已自动恢复到操作前版本", "health_verification_failed"
	if err := rolledBack.Validate(); err != nil {
		t.Fatalf("valid rolled-back deployment rejected: %v", err)
	}
	rolledBack.Error = ""
	if err := rolledBack.Validate(); err == nil {
		t.Fatal("rolled-back deployment without original failure category was accepted")
	}
}

func TestOperationRejectsInconsistentOrOversizedAuditData(t *testing.T) {
	now := time.Now().UTC()
	base := Operation{
		ID: "op_1", HostID: "host-a", WorkloadKey: "docker:api", Kind: OperationStart,
		State: OperationRequested, RequestedBy: "tailnet-operator", RequestedAt: now,
	}
	cases := map[string]Operation{
		"missing target":        func() Operation { value := base; value.WorkloadKey = ""; return value }(),
		"running without start": func() Operation { value := base; value.State = OperationRunning; return value }(),
		"requested with result": func() Operation { value := base; value.ResultSummary = "done"; return value }(),
		"oversized summary": func() Operation {
			value := base
			started, finished := now, now.Add(time.Second)
			value.State, value.StartedAt, value.FinishedAt = OperationSucceeded, &started, &finished
			value.ResultSummary = strings.Repeat("界", 241)
			return value
		}(),
	}
	for name, operation := range cases {
		t.Run(name, func(t *testing.T) {
			if err := operation.Validate(); err == nil {
				t.Fatal("invalid operation was accepted")
			}
		})
	}
}

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
			Kind: RouteProxy, Scheme: "https", Host: "app.example.test", Port: 443, Path: "/", Upstreams: []string{"127.0.0.1:3000"},
		}},
	}
	if err := observation.Validate(); err != nil {
		t.Fatalf("valid redacted proxy route was rejected: %v", err)
	}
	observation.Routes[0].Kind = "arbitrary"
	if err := observation.Validate(); err == nil {
		t.Fatal("unknown route kind should be rejected")
	}
	observation.Routes[0].Kind = RouteProxy
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

func TestWebLinkCheckRequiresConsistentEvidence(t *testing.T) {
	check := WebLinkCheck{
		HostID: "host-a", WorkloadKey: "docker:web", URL: "https://app.example.test/",
		State: WebLinkReachable, HTTPStatus: 204, LatencyMS: 12, CheckedAt: time.Now().UTC(),
	}
	if err := check.Validate(); err != nil {
		t.Fatalf("valid Web link check was rejected: %v", err)
	}
	check.URL = "https://user:secret@app.example.test/"
	if err := check.Validate(); err == nil {
		t.Fatal("credential-bearing Web link was accepted")
	}
	check.URL = "https://app.example.test/"
	check.State = WebLinkUnreachable
	check.HTTPStatus = 0
	check.ErrorKind = ""
	if err := check.Validate(); err == nil {
		t.Fatal("unreachable Web link without sanitized error evidence was accepted")
	}
	check.ErrorKind = "timeout"
	if err := check.Validate(); err != nil {
		t.Fatalf("evidenced unreachable Web link was rejected: %v", err)
	}
}

func TestObservationValidatesPrivacyMinimizedSSHAuthSummary(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	observation := Observation{
		HostID: "host-a", ObservedAt: now, Online: true,
		SSH: &SSHAuthObservation{
			WindowStart: now.Add(-10 * time.Minute), WindowEnd: now, FailedTotal: 3,
			Sources: []SSHAuthSource{{Address: "203.0.113.9", Count: 2}, {Address: "2001:db8::5", Count: 1}},
		},
	}
	if err := observation.Validate(); err != nil {
		t.Fatalf("valid SSH summary was rejected: %v", err)
	}
	observation.SSH.Sources[0].Address = "203.000.113.009"
	if err := observation.Validate(); err == nil {
		t.Fatal("non-canonical SSH source was accepted")
	}
	observation.SSH.Sources[0].Address = "203.0.113.9"
	observation.Online = false
	if err := observation.Validate(); err == nil {
		t.Fatal("offline observation carried SSH telemetry")
	}
	observation.Online = true
	observation.ObservedAt = now.Add(10 * time.Minute)
	if err := observation.Validate(); err == nil {
		t.Fatal("stale SSH window was accepted")
	}
}

func TestEventSignalAndLifecycleConsistency(t *testing.T) {
	now := time.Now().UTC()
	signal := EventSignal{
		HostID: "host-a", Kind: "resource.memory", Severity: SeverityWarning,
		DedupeKey: "host-a:resource:memory", Title: "Memory pressure", Detail: "86% used",
	}
	if err := signal.Validate(); err != nil {
		t.Fatalf("valid signal was rejected: %v", err)
	}
	crossHost := signal
	crossHost.DedupeKey = "host-b:resource:memory"
	if err := crossHost.Validate(); err == nil {
		t.Fatal("cross-host dedupe key was accepted")
	}
	invalidSeverity := signal
	invalidSeverity.Severity = "urgent"
	if err := invalidSeverity.Validate(); err == nil {
		t.Fatal("unknown severity was accepted")
	}

	event := Event{
		ID: "evt_fixture", HostID: signal.HostID, Kind: signal.Kind, Severity: signal.Severity,
		State: EventActive, DedupeKey: signal.DedupeKey, Title: signal.Title, Detail: signal.Detail,
		FirstObservedAt: now, LastObservedAt: now,
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("valid active event was rejected: %v", err)
	}
	acknowledgedAt := now.Add(time.Minute)
	event.State = EventAcknowledged
	event.AcknowledgedAt = &acknowledgedAt
	if err := event.Validate(); err != nil {
		t.Fatalf("valid acknowledged event was rejected: %v", err)
	}
	resolvedAt := now.Add(2 * time.Minute)
	event.State = EventResolved
	event.ResolvedAt = &resolvedAt
	if err := event.Validate(); err != nil {
		t.Fatalf("valid resolved event was rejected: %v", err)
	}
	event.ResolvedAt = nil
	if err := event.Validate(); err == nil {
		t.Fatal("resolved event without recovery time was accepted")
	}
}

func TestSummarizeObservationBoundsTimelineData(t *testing.T) {
	observation := Observation{
		HostID: "host-a", ObservedAt: time.Now().UTC(), Online: true, AgentVersion: "0.5.0",
		Resources: &Resources{
			CPUs: 4, Load1: 1.25,
			Memory: MemoryResources{TotalBytes: 1000, UsedBytes: 760},
			Disks:  []DiskResources{{Mount: "/", TotalBytes: 2000, UsedBytes: 900}},
		},
		Workloads: []Workload{
			{HostID: "host-a", Key: "docker:web", Kind: WorkloadDocker, Name: "web", State: "running"},
			{HostID: "host-a", Key: "systemd:worker.service", Kind: WorkloadSystemd, Name: "worker", State: "failed"},
		},
		Endpoints: []Endpoint{
			{HostID: "host-a", WorkloadKey: "docker:web", Key: "tcp://0.0.0.0:443", Protocol: "tcp", Bind: "0.0.0.0", Port: 443, Binding: BindingWildcard, Reachability: ReachabilityUnknown},
			{HostID: "host-a", WorkloadKey: "docker:web", Key: "tcp://127.0.0.1:8080", Protocol: "tcp", Bind: "127.0.0.1", Port: 8080, Binding: BindingLocal, Reachability: ReachabilityUnknown},
		},
		Warnings: []string{"partial collection"},
	}
	summary, err := SummarizeObservation(observation)
	if err != nil {
		t.Fatal(err)
	}
	if summary.WorkloadCount != 2 || summary.FailedWorkloadCount != 1 || summary.WildcardEndpointCount != 1 || summary.WarningCount != 1 {
		t.Fatalf("summary counts mismatch: %+v", summary)
	}
	if summary.CPUs != 4 || summary.Load1 != 1.25 || summary.MemoryUsedPct != 76 || summary.DiskUsedPct != 45 {
		t.Fatalf("summary resources mismatch: %+v", summary)
	}
}
