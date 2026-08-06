// Package domain defines Lodge's durable business contracts. It has no
// dependency on HTTP, Agent wire payloads, SQLite, HTML, or command execution.
package domain

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type HostID string

type Host struct {
	ID         HostID `json:"id"`
	Name       string `json:"name"`
	PublicHost string `json:"publicHost,omitempty"`
}

type WorkloadKind string

const (
	WorkloadDocker  WorkloadKind = "docker"
	WorkloadCompose WorkloadKind = "compose"
	WorkloadSystemd WorkloadKind = "systemd"
	WorkloadProcess WorkloadKind = "process"
)

// Workload is a host-scoped durable identity. Key comes from the native
// runtime (for example docker:nginx or systemd:caddy.service) and remains
// stable when a PID or container instance changes.
type Workload struct {
	HostID       HostID       `json:"hostId"`
	Key          string       `json:"key"`
	Kind         WorkloadKind `json:"kind"`
	Name         string       `json:"name"`
	State        string       `json:"state"`
	Image        string       `json:"image,omitempty"`
	Unit         string       `json:"unit,omitempty"`
	Health       string       `json:"health,omitempty"`
	PID          int          `json:"pid,omitempty"`
	StartedAt    *time.Time   `json:"startedAt,omitempty"`
	Unidentified bool         `json:"unidentified,omitempty"`
}

// BindingScope is derived only from the local socket binding. Wildcard means
// 0.0.0.0, ::, or *, not that the port is reachable from the internet.
type BindingScope string

const (
	BindingUnknown   BindingScope = "unknown"
	BindingLocal     BindingScope = "local"
	BindingTailnet   BindingScope = "tailnet"
	BindingWildcard  BindingScope = "wildcard"
	BindingInterface BindingScope = "interface"
)

// Reachability requires independent evidence. A newly observed endpoint is
// always unknown, including endpoints bound to a wildcard address.
type Reachability string

const (
	ReachabilityUnknown     Reachability = "unknown"
	ReachabilityUnreachable Reachability = "unreachable"
	ReachabilityTailnet     Reachability = "tailnet"
	ReachabilityPublic      Reachability = "public"
)

type Endpoint struct {
	HostID                HostID       `json:"hostId"`
	WorkloadKey           string       `json:"workloadKey"`
	Key                   string       `json:"key"`
	Protocol              string       `json:"protocol"`
	Bind                  string       `json:"bind"`
	Port                  int          `json:"port"`
	Binding               BindingScope `json:"binding"`
	Reachability          Reachability `json:"reachability"`
	ReachabilitySource    string       `json:"reachabilitySource,omitempty"`
	ReachabilityCheckedAt *time.Time   `json:"reachabilityCheckedAt,omitempty"`
}

type Resources struct {
	CPUs   int              `json:"cpus,omitempty"`
	Load1  float64          `json:"load1,omitempty"`
	Load5  float64          `json:"load5,omitempty"`
	Load15 float64          `json:"load15,omitempty"`
	Memory MemoryResources  `json:"memory"`
	Disks  []DiskResources  `json:"disks,omitempty"`
	Docker *DockerResources `json:"docker,omitempty"`
}

type MemoryResources struct {
	TotalBytes     int64 `json:"totalBytes,omitempty"`
	AvailableBytes int64 `json:"availableBytes,omitempty"`
	UsedBytes      int64 `json:"usedBytes,omitempty"`
	SwapTotalBytes int64 `json:"swapTotalBytes,omitempty"`
	SwapUsedBytes  int64 `json:"swapUsedBytes,omitempty"`
}

type DiskResources struct {
	Mount      string `json:"mount"`
	Filesystem string `json:"filesystem"`
	TotalBytes int64  `json:"totalBytes"`
	FreeBytes  int64  `json:"freeBytes"`
	UsedBytes  int64  `json:"usedBytes"`
}

type DockerResources struct {
	Containers        int   `json:"containers"`
	ContainersRunning int   `json:"containersRunning"`
	Images            int   `json:"images"`
	Volumes           int   `json:"volumes"`
	ReclaimableBytes  int64 `json:"reclaimableBytes"`
	TotalBytes        int64 `json:"totalBytes"`
}

// Observation is an immutable result of one host collection attempt.
type Observation struct {
	HostID       HostID     `json:"hostId"`
	ObservedAt   time.Time  `json:"observedAt"`
	Online       bool       `json:"online"`
	LastError    string     `json:"lastError,omitempty"`
	Hostname     string     `json:"hostname,omitempty"`
	AgentVersion string     `json:"agentVersion,omitempty"`
	Resources    *Resources `json:"resources,omitempty"`
	Workloads    []Workload `json:"workloads"`
	Endpoints    []Endpoint `json:"endpoints"`
	Warnings     []string   `json:"warnings,omitempty"`
}

// Annotation is user-maintained metadata joined to an observed Workload. It
// never replaces discovery data and is durable independently of observations.
type Annotation struct {
	HostID      HostID    `json:"hostId"`
	WorkloadKey string    `json:"workloadKey"`
	Alias       string    `json:"alias,omitempty"`
	URL         string    `json:"url,omitempty"`
	Hidden      bool      `json:"hidden,omitempty"`
	Notes       string    `json:"notes,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

type EventState string

const (
	EventActive       EventState = "active"
	EventAcknowledged EventState = "acknowledged"
	EventResolved     EventState = "resolved"
)

type Event struct {
	ID              string     `json:"id"`
	HostID          HostID     `json:"hostId"`
	Kind            string     `json:"kind"`
	Severity        Severity   `json:"severity"`
	State           EventState `json:"state"`
	DedupeKey       string     `json:"dedupeKey"`
	Title           string     `json:"title"`
	Detail          string     `json:"detail,omitempty"`
	FirstObservedAt time.Time  `json:"firstObservedAt"`
	LastObservedAt  time.Time  `json:"lastObservedAt"`
	AcknowledgedAt  *time.Time `json:"acknowledgedAt,omitempty"`
	ResolvedAt      *time.Time `json:"resolvedAt,omitempty"`
}

type OperationKind string

const (
	OperationStart    OperationKind = "start"
	OperationStop     OperationKind = "stop"
	OperationRestart  OperationKind = "restart"
	OperationLogs     OperationKind = "logs"
	OperationDeploy   OperationKind = "deploy"
	OperationRollback OperationKind = "rollback"
)

type OperationState string

const (
	OperationRequested  OperationState = "requested"
	OperationRunning    OperationState = "running"
	OperationSucceeded  OperationState = "succeeded"
	OperationFailed     OperationState = "failed"
	OperationRolledBack OperationState = "rolled_back"
)

type Operation struct {
	ID            string         `json:"id"`
	HostID        HostID         `json:"hostId"`
	WorkloadKey   string         `json:"workloadKey,omitempty"`
	Kind          OperationKind  `json:"kind"`
	State         OperationState `json:"state"`
	RequestedBy   string         `json:"requestedBy"`
	RequestedAt   time.Time      `json:"requestedAt"`
	StartedAt     *time.Time     `json:"startedAt,omitempty"`
	FinishedAt    *time.Time     `json:"finishedAt,omitempty"`
	ResultSummary string         `json:"resultSummary,omitempty"`
	Error         string         `json:"error,omitempty"`
}

func (h Host) Validate() error {
	if err := validateIdentifier("host id", string(h.ID), 128); err != nil {
		return err
	}
	if strings.TrimSpace(h.Name) == "" {
		return errors.New("host name must not be empty")
	}
	return nil
}

func (o Observation) Validate() error {
	if err := validateIdentifier("host id", string(o.HostID), 128); err != nil {
		return err
	}
	if o.ObservedAt.IsZero() {
		return errors.New("observation time must not be zero")
	}
	workloads := make(map[string]struct{}, len(o.Workloads))
	for _, workload := range o.Workloads {
		if workload.HostID != o.HostID {
			return fmt.Errorf("workload %q belongs to another host", workload.Key)
		}
		if err := validateIdentifier("workload key", workload.Key, 512); err != nil {
			return err
		}
		if !validWorkloadKind(workload.Kind) {
			return fmt.Errorf("workload %q has invalid kind %q", workload.Key, workload.Kind)
		}
		if strings.TrimSpace(workload.Name) == "" {
			return fmt.Errorf("workload %q has no name", workload.Key)
		}
		if _, duplicate := workloads[workload.Key]; duplicate {
			return fmt.Errorf("duplicate workload key %q", workload.Key)
		}
		workloads[workload.Key] = struct{}{}
	}
	endpoints := make(map[string]struct{}, len(o.Endpoints))
	for _, endpoint := range o.Endpoints {
		if endpoint.HostID != o.HostID {
			return fmt.Errorf("endpoint %q belongs to another host", endpoint.Key)
		}
		if _, exists := workloads[endpoint.WorkloadKey]; !exists {
			return fmt.Errorf("endpoint %q references missing workload %q", endpoint.Key, endpoint.WorkloadKey)
		}
		if err := validateIdentifier("endpoint key", endpoint.Key, 512); err != nil {
			return err
		}
		if endpoint.Protocol != "tcp" && endpoint.Protocol != "udp" {
			return fmt.Errorf("endpoint %q has invalid protocol %q", endpoint.Key, endpoint.Protocol)
		}
		if endpoint.Port < 1 || endpoint.Port > 65535 {
			return fmt.Errorf("endpoint %q has invalid port %d", endpoint.Key, endpoint.Port)
		}
		compositeKey := endpoint.WorkloadKey + "\x00" + endpoint.Key
		if _, duplicate := endpoints[compositeKey]; duplicate {
			return fmt.Errorf("duplicate endpoint key %q", endpoint.Key)
		}
		endpoints[compositeKey] = struct{}{}
		if !validBinding(endpoint.Binding) {
			return fmt.Errorf("endpoint %q has invalid binding %q", endpoint.Key, endpoint.Binding)
		}
		if !validReachability(endpoint.Reachability) {
			return fmt.Errorf("endpoint %q has invalid reachability %q", endpoint.Key, endpoint.Reachability)
		}
		if endpoint.Reachability != ReachabilityUnknown && (endpoint.ReachabilitySource == "" || endpoint.ReachabilityCheckedAt == nil) {
			return fmt.Errorf("endpoint %q reachability lacks evidence", endpoint.Key)
		}
		if endpoint.Reachability == ReachabilityUnknown && (endpoint.ReachabilitySource != "" || endpoint.ReachabilityCheckedAt != nil) {
			return fmt.Errorf("endpoint %q has evidence but unknown reachability", endpoint.Key)
		}
	}
	return nil
}

func (a Annotation) Validate() error {
	if err := validateIdentifier("host id", string(a.HostID), 128); err != nil {
		return err
	}
	if err := validateIdentifier("workload key", a.WorkloadKey, 512); err != nil {
		return err
	}
	if a.UpdatedAt.IsZero() {
		return errors.New("annotation update time must not be zero")
	}
	if !utf8.ValidString(a.Alias) || !utf8.ValidString(a.URL) || !utf8.ValidString(a.Notes) {
		return errors.New("annotation must be valid UTF-8")
	}
	if utf8.RuneCountInString(a.Alias) > 120 {
		return errors.New("annotation alias exceeds 120 characters")
	}
	if len(a.URL) > 2048 {
		return errors.New("annotation URL exceeds 2048 bytes")
	}
	if utf8.RuneCountInString(a.Notes) > 4000 {
		return errors.New("annotation notes exceed 4000 characters")
	}
	if a.URL != "" {
		parsed, err := url.Parse(a.URL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
			return errors.New("annotation URL must be absolute http/https without credentials")
		}
	}
	return nil
}

func validWorkloadKind(kind WorkloadKind) bool {
	switch kind {
	case WorkloadDocker, WorkloadCompose, WorkloadSystemd, WorkloadProcess:
		return true
	default:
		return false
	}
}

func validBinding(binding BindingScope) bool {
	switch binding {
	case BindingUnknown, BindingLocal, BindingTailnet, BindingWildcard, BindingInterface:
		return true
	default:
		return false
	}
}

func validReachability(reachability Reachability) bool {
	switch reachability {
	case ReachabilityUnknown, ReachabilityUnreachable, ReachabilityTailnet, ReachabilityPublic:
		return true
	default:
		return false
	}
}

func validateIdentifier(label, value string, maxBytes int) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty", label)
	}
	if len(value) > maxBytes {
		return fmt.Errorf("%s exceeds %d bytes", label, maxBytes)
	}
	for _, r := range value {
		if r == 0 || unicode.IsControl(r) {
			return fmt.Errorf("%s contains control characters", label)
		}
	}
	return nil
}
