// Package domain defines Lodge's durable business contracts. It has no
// dependency on HTTP, Agent wire payloads, SQLite, HTML, or command execution.
package domain

import (
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"strconv"
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
	HostID         HostID       `json:"hostId"`
	Key            string       `json:"key"`
	Kind           WorkloadKind `json:"kind"`
	Name           string       `json:"name"`
	State          string       `json:"state"`
	Image          string       `json:"image,omitempty"`
	Unit           string       `json:"unit,omitempty"`
	ComposeProject string       `json:"composeProject,omitempty"`
	ComposeService string       `json:"composeService,omitempty"`
	Health         string       `json:"health,omitempty"`
	PID            int          `json:"pid,omitempty"`
	StartedAt      *time.Time   `json:"startedAt,omitempty"`
	Unidentified   bool         `json:"unidentified,omitempty"`
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

// ProxyRoute is an immutable, redacted route observed on a proxy workload.
// Upstreams are authorities only and are informational; they are not treated
// as confirmed reachability or as credentials.
type ProxyRoute struct {
	HostID      HostID   `json:"hostId"`
	WorkloadKey string   `json:"workloadKey"`
	Key         string   `json:"key"`
	Scheme      string   `json:"scheme"`
	Host        string   `json:"host,omitempty"`
	Port        int      `json:"port"`
	Path        string   `json:"path"`
	Upstreams   []string `json:"upstreams,omitempty"`
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
	HostID       HostID       `json:"hostId"`
	ObservedAt   time.Time    `json:"observedAt"`
	Online       bool         `json:"online"`
	LastError    string       `json:"lastError,omitempty"`
	Hostname     string       `json:"hostname,omitempty"`
	AgentVersion string       `json:"agentVersion,omitempty"`
	Resources    *Resources   `json:"resources,omitempty"`
	Workloads    []Workload   `json:"workloads"`
	Endpoints    []Endpoint   `json:"endpoints"`
	Routes       []ProxyRoute `json:"routes"`
	Warnings     []string     `json:"warnings,omitempty"`
}

// ObservationSummary is the bounded history projection used by operators and
// rule evaluation. It deliberately excludes full workload, endpoint, route,
// and resource payloads so a timeline request cannot multiply large snapshots.
type ObservationSummary struct {
	HostID                HostID    `json:"hostId"`
	ObservedAt            time.Time `json:"observedAt"`
	Online                bool      `json:"online"`
	LastError             string    `json:"lastError,omitempty"`
	AgentVersion          string    `json:"agentVersion,omitempty"`
	CPUs                  int       `json:"cpus,omitempty"`
	Load1                 float64   `json:"load1,omitempty"`
	MemoryUsedPct         int       `json:"memoryUsedPct,omitempty"`
	DiskUsedPct           int       `json:"diskUsedPct,omitempty"`
	WorkloadCount         int       `json:"workloadCount"`
	FailedWorkloadCount   int       `json:"failedWorkloadCount"`
	WildcardEndpointCount int       `json:"wildcardEndpointCount"`
	WarningCount          int       `json:"warningCount"`
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

// WebLinkState is an active HTTP probe result from the Hub's network
// perspective. It is intentionally separate from socket binding and endpoint
// reachability: a valid URL can still be unavailable or return a server error.
type WebLinkState string

const (
	WebLinkReachable   WebLinkState = "reachable"
	WebLinkDegraded    WebLinkState = "degraded"
	WebLinkUnreachable WebLinkState = "unreachable"
)

// WebLinkCheck stores only bounded probe metadata. Response bodies, headers,
// resolved addresses, and raw network errors are never retained.
type WebLinkCheck struct {
	HostID      HostID       `json:"hostId"`
	WorkloadKey string       `json:"workloadKey"`
	URL         string       `json:"url"`
	State       WebLinkState `json:"state"`
	HTTPStatus  int          `json:"httpStatus,omitempty"`
	LatencyMS   int64        `json:"latencyMs"`
	ErrorKind   string       `json:"errorKind,omitempty"`
	CheckedAt   time.Time    `json:"checkedAt"`
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
		if workload.ComposeService != "" && workload.ComposeProject == "" {
			return fmt.Errorf("workload %q has a Compose service without a project", workload.Key)
		}
		if workload.ComposeProject != "" || workload.ComposeService != "" {
			if workload.Kind != WorkloadDocker {
				return fmt.Errorf("workload %q has Compose metadata but is not Docker", workload.Key)
			}
			if err := validateIdentifier("Compose project", workload.ComposeProject, 128); err != nil {
				return err
			}
			if workload.ComposeService != "" {
				if err := validateIdentifier("Compose service", workload.ComposeService, 128); err != nil {
					return err
				}
			}
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
	routes := make(map[string]struct{}, len(o.Routes))
	for _, route := range o.Routes {
		if route.HostID != o.HostID {
			return fmt.Errorf("proxy route %q belongs to another host", route.Key)
		}
		if _, exists := workloads[route.WorkloadKey]; !exists {
			return fmt.Errorf("proxy route %q references missing workload %q", route.Key, route.WorkloadKey)
		}
		if err := validateIdentifier("proxy route key", route.Key, 1024); err != nil {
			return err
		}
		if route.Scheme != "http" && route.Scheme != "https" {
			return fmt.Errorf("proxy route %q has invalid scheme %q", route.Key, route.Scheme)
		}
		if !validProxyHost(route.Host, true) {
			return fmt.Errorf("proxy route %q has invalid host", route.Key)
		}
		if route.Port < 1 || route.Port > 65535 {
			return fmt.Errorf("proxy route %q has invalid port %d", route.Key, route.Port)
		}
		if !validProxyPath(route.Path) {
			return fmt.Errorf("proxy route %q has invalid path", route.Key)
		}
		expectedKey := route.Scheme + "://" + net.JoinHostPort(route.Host, strconv.Itoa(route.Port)) + route.Path
		if route.Key != expectedKey {
			return fmt.Errorf("proxy route %q does not match its route fields", route.Key)
		}
		if len(route.Upstreams) > 16 {
			return fmt.Errorf("proxy route %q has too many upstreams", route.Key)
		}
		seenUpstreams := make(map[string]struct{}, len(route.Upstreams))
		for _, upstream := range route.Upstreams {
			if !validProxyUpstream(upstream) {
				return fmt.Errorf("proxy route %q has invalid upstream", route.Key)
			}
			if _, duplicate := seenUpstreams[upstream]; duplicate {
				return fmt.Errorf("proxy route %q has duplicate upstream", route.Key)
			}
			seenUpstreams[upstream] = struct{}{}
		}
		compositeKey := route.WorkloadKey + "\x00" + route.Key
		if _, duplicate := routes[compositeKey]; duplicate {
			return fmt.Errorf("duplicate proxy route key %q", route.Key)
		}
		routes[compositeKey] = struct{}{}
	}
	return nil
}

func SummarizeObservation(observation Observation) (ObservationSummary, error) {
	if err := observation.Validate(); err != nil {
		return ObservationSummary{}, err
	}
	summary := ObservationSummary{
		HostID: observation.HostID, ObservedAt: observation.ObservedAt, Online: observation.Online,
		LastError: observation.LastError, AgentVersion: observation.AgentVersion,
		WorkloadCount: len(observation.Workloads), WarningCount: len(observation.Warnings),
	}
	for _, workload := range observation.Workloads {
		if strings.EqualFold(strings.TrimSpace(workload.State), "failed") ||
			strings.EqualFold(strings.TrimSpace(workload.Health), "unhealthy") {
			summary.FailedWorkloadCount++
		}
	}
	for _, endpoint := range observation.Endpoints {
		if endpoint.Binding == BindingWildcard {
			summary.WildcardEndpointCount++
		}
	}
	if observation.Resources != nil {
		summary.CPUs = observation.Resources.CPUs
		summary.Load1 = observation.Resources.Load1
		summary.MemoryUsedPct = UsagePercent(
			observation.Resources.Memory.UsedBytes,
			observation.Resources.Memory.TotalBytes,
		)
		for _, disk := range observation.Resources.Disks {
			if disk.Mount == "/" {
				summary.DiskUsedPct = UsagePercent(disk.UsedBytes, disk.TotalBytes)
				break
			}
		}
	}
	if err := summary.Validate(); err != nil {
		return ObservationSummary{}, err
	}
	return summary, nil
}

func (summary ObservationSummary) Validate() error {
	if err := validateIdentifier("host id", string(summary.HostID), 128); err != nil {
		return err
	}
	if summary.ObservedAt.IsZero() {
		return errors.New("observation summary time must not be zero")
	}
	if !utf8.ValidString(summary.LastError) || len(summary.LastError) > 4096 {
		return errors.New("observation summary error is invalid")
	}
	if !utf8.ValidString(summary.AgentVersion) || len(summary.AgentVersion) > 128 {
		return errors.New("observation summary Agent version is invalid")
	}
	if summary.CPUs < 0 || summary.CPUs > 4096 || summary.Load1 < 0 || math.IsNaN(summary.Load1) || math.IsInf(summary.Load1, 0) {
		return errors.New("observation summary load is invalid")
	}
	for _, value := range []int{summary.MemoryUsedPct, summary.DiskUsedPct} {
		if value < 0 || value > 100 {
			return errors.New("observation summary percentage is invalid")
		}
	}
	for _, value := range []int{
		summary.WorkloadCount, summary.FailedWorkloadCount,
		summary.WildcardEndpointCount, summary.WarningCount,
	} {
		if value < 0 {
			return errors.New("observation summary count is invalid")
		}
	}
	if summary.FailedWorkloadCount > summary.WorkloadCount {
		return errors.New("failed workload count exceeds workload count")
	}
	return nil
}

// UsagePercent converts byte counters to an integer percentage while keeping
// malformed or racing OS counters inside the public 0..100 contract.
func UsagePercent(used, total int64) int {
	if used <= 0 || total <= 0 {
		return 0
	}
	if used >= total {
		return 100
	}
	return int(float64(used) * 100 / float64(total))
}

func validProxyHost(host string, allowEmpty bool) bool {
	if host == "" {
		return allowEmpty
	}
	if len(host) > 253 || strings.HasSuffix(host, ".") {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	if strings.ContainsAny(host, "/:@?#[]") {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if len(label) < 1 || len(label) > 63 || !asciiAlphaNumeric(label[0]) || !asciiAlphaNumeric(label[len(label)-1]) {
			return false
		}
		for index := 1; index < len(label)-1; index++ {
			if !asciiAlphaNumeric(label[index]) && label[index] != '-' {
				return false
			}
		}
	}
	return true
}

func asciiAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func validProxyPath(path string) bool {
	if path == "" || len(path) > 512 || path[0] != '/' || !utf8.ValidString(path) || strings.ContainsAny(path, "?#") {
		return false
	}
	for _, character := range path {
		if character == 0 || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validProxyUpstream(upstream string) bool {
	if len(upstream) < 3 || len(upstream) > 256 || strings.ContainsAny(upstream, "/@?#") {
		return false
	}
	host, portText, err := net.SplitHostPort(upstream)
	if err != nil || !validProxyHost(host, false) {
		return false
	}
	port, err := strconv.Atoi(portText)
	return err == nil && port >= 1 && port <= 65535
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

func (check WebLinkCheck) Validate() error {
	if err := validateIdentifier("host id", string(check.HostID), 128); err != nil {
		return err
	}
	if err := validateIdentifier("workload key", check.WorkloadKey, 512); err != nil {
		return err
	}
	if check.CheckedAt.IsZero() {
		return errors.New("Web link check time must not be zero")
	}
	if check.LatencyMS < 0 || check.LatencyMS > int64((time.Hour)/time.Millisecond) {
		return errors.New("Web link latency is out of range")
	}
	if len(check.URL) > 2048 {
		return errors.New("Web link URL exceeds 2048 bytes")
	}
	parsed, err := url.Parse(check.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return errors.New("Web link URL must be absolute http/https without credentials")
	}
	if len(check.ErrorKind) > 64 || !utf8.ValidString(check.ErrorKind) {
		return errors.New("Web link error kind is invalid")
	}
	switch check.State {
	case WebLinkReachable:
		if check.HTTPStatus < 100 || check.HTTPStatus >= 500 || check.ErrorKind != "" {
			return errors.New("reachable Web link requires HTTP 100..499 and no error")
		}
	case WebLinkDegraded:
		if check.HTTPStatus < 500 || check.HTTPStatus > 599 || check.ErrorKind != "" {
			return errors.New("degraded Web link requires HTTP 5xx and no error")
		}
	case WebLinkUnreachable:
		if check.HTTPStatus != 0 || check.ErrorKind == "" {
			return errors.New("unreachable Web link requires a sanitized error kind and no HTTP status")
		}
	default:
		return fmt.Errorf("invalid Web link state %q", check.State)
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
