package hub

import (
	"github.com/toolazytoname/lodge/internal/domain"
	"github.com/toolazytoname/lodge/internal/shared"
)

// SessionResponse is the browser-visible authentication state.
type SessionResponse struct {
	Authed    bool   `json:"authed"`
	CSRFToken string `json:"csrfToken,omitempty"`
}

// AgentSummary is the compact fleet overview returned by /api/agents.
type AgentSummary struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Online       bool    `json:"online"`
	LastSeen     string  `json:"lastSeen,omitempty"`
	LastError    string  `json:"lastError,omitempty"`
	CPUs         int     `json:"cpus,omitempty"`
	Load1        float64 `json:"load1,omitempty"`
	MemUsedPct   int     `json:"memUsedPct,omitempty"`
	DiskUsedPct  int     `json:"diskUsedPct,omitempty"`
	ServiceCount int     `json:"serviceCount"`
	PublicCount  int     `json:"publicCount"`
	// Security is the current, privacy-minimized host posture. It is a live
	// snapshot rather than a claim about cloud security groups or Internet reachability.
	Security *shared.SecurityPosture `json:"security,omitempty"`
}

// ServiceAgent keeps /api/services from repeating the full status and raw
// service arrays that are already represented by the joined service views.
type ServiceAgent struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Online       bool   `json:"online"`
	LastSeen     string `json:"lastSeen,omitempty"`
	LastError    string `json:"lastError,omitempty"`
	AgentVersion string `json:"agentVersion,omitempty"`
}

// AgentServices is one host and its annotation-joined service directory.
type AgentServices struct {
	Agent    ServiceAgent  `json:"agent"`
	Services []ServiceView `json:"services"`
}

// AnnotationInput is the only annotation shape accepted from the browser.
// Identity comes from the validated query parameters, never the JSON body.
type AnnotationInput struct {
	Alias  string `json:"alias,omitempty"`
	URL    string `json:"url,omitempty"`
	Hidden bool   `json:"hidden,omitempty"`
	Notes  string `json:"notes,omitempty"`
}

// ObservationHistoryPoint is a bounded timeline projection. Full immutable
// observations remain in SQLite and are never multiplied into the browser API.
type ObservationHistoryPoint struct {
	ObservedAt            string  `json:"observedAt"`
	Online                bool    `json:"online"`
	LastError             string  `json:"lastError,omitempty"`
	AgentVersion          string  `json:"agentVersion,omitempty"`
	CPUs                  int     `json:"cpus,omitempty"`
	Load1                 float64 `json:"load1,omitempty"`
	MemoryUsedPct         int     `json:"memoryUsedPct,omitempty"`
	DiskUsedPct           int     `json:"diskUsedPct,omitempty"`
	WorkloadCount         int     `json:"workloadCount"`
	FailedWorkloadCount   int     `json:"failedWorkloadCount"`
	WildcardEndpointCount int     `json:"wildcardEndpointCount"`
	WarningCount          int     `json:"warningCount"`
}

type HostHistoryResponse struct {
	AgentID string                    `json:"agentId"`
	Points  []ObservationHistoryPoint `json:"points"`
}

// EventView excludes the internal deduplication key while retaining the audit
// timestamps an operator needs to understand and acknowledge an incident.
type EventView struct {
	ID              string            `json:"id"`
	AgentID         string            `json:"agentId"`
	Kind            string            `json:"kind"`
	Severity        domain.Severity   `json:"severity"`
	State           domain.EventState `json:"state"`
	Title           string            `json:"title"`
	Detail          string            `json:"detail,omitempty"`
	FirstObservedAt string            `json:"firstObservedAt"`
	LastObservedAt  string            `json:"lastObservedAt"`
	AcknowledgedAt  string            `json:"acknowledgedAt,omitempty"`
	ResolvedAt      string            `json:"resolvedAt,omitempty"`
}

type EventsResponse struct {
	AgentID string      `json:"agentId,omitempty"`
	Events  []EventView `json:"events"`
}

// WebLinkCheckView is bounded probe evidence from the Hub's network view.
type WebLinkCheckView struct {
	AgentID    string              `json:"agentId"`
	ServiceKey string              `json:"serviceKey"`
	URL        string              `json:"url"`
	State      domain.WebLinkState `json:"state"`
	HTTPStatus int                 `json:"httpStatus,omitempty"`
	LatencyMS  int64               `json:"latencyMs"`
	ErrorKind  string              `json:"errorKind,omitempty"`
	CheckedAt  string              `json:"checkedAt"`
}

type WebLinkCheckSummary struct {
	Total       int    `json:"total"`
	Reachable   int    `json:"reachable"`
	Degraded    int    `json:"degraded"`
	Unreachable int    `json:"unreachable"`
	CheckedAt   string `json:"checkedAt,omitempty"`
}

type WebLinkChecksResponse struct {
	Checks  []WebLinkCheckView  `json:"checks"`
	Summary WebLinkCheckSummary `json:"summary"`
}

// AgentActionsResponse is a live capability projection. It contains no Agent
// URL, bearer token, executable, command line, or policy file path.
type AgentActionsResponse struct {
	AgentID      string                    `json:"agentId"`
	AgentName    string                    `json:"agentName"`
	AgentVersion string                    `json:"agentVersion,omitempty"`
	Actions      []shared.ActionDefinition `json:"actions"`
}

type ActionExecutionInput struct {
	AgentID      string `json:"agentId"`
	ActionID     string `json:"actionId"`
	Confirmation string `json:"confirmation"`
}

type OperationView struct {
	ID            string                `json:"id"`
	AgentID       string                `json:"agentId"`
	TargetKey     string                `json:"targetKey,omitempty"`
	Kind          domain.OperationKind  `json:"kind"`
	State         domain.OperationState `json:"state"`
	RequestedBy   string                `json:"requestedBy"`
	RequestedAt   string                `json:"requestedAt"`
	StartedAt     string                `json:"startedAt,omitempty"`
	FinishedAt    string                `json:"finishedAt,omitempty"`
	ResultSummary string                `json:"resultSummary,omitempty"`
	ErrorKind     string                `json:"errorKind,omitempty"`
}

type OperationsResponse struct {
	AgentID    string          `json:"agentId,omitempty"`
	Operations []OperationView `json:"operations"`
}

type ActionExecutionResponse struct {
	Operation OperationView                 `json:"operation"`
	Result    *shared.ActionExecutionResult `json:"result,omitempty"`
	ErrorKind string                        `json:"errorKind,omitempty"`
}

// AgentDeploymentsResponse is the live root-policy projection. Host paths,
// Compose/environment content, Agent URL, token, and health implementation are
// intentionally absent.
type AgentDeploymentsResponse struct {
	AgentID      string                        `json:"agentId"`
	AgentName    string                        `json:"agentName"`
	AgentVersion string                        `json:"agentVersion,omitempty"`
	Deployments  []shared.DeploymentDefinition `json:"deployments"`
}

type DeploymentExecutionInput struct {
	AgentID      string `json:"agentId"`
	DeploymentID string `json:"deploymentId"`
	Confirmation string `json:"confirmation"`
}

type DeploymentExecutionResponse struct {
	Operation OperationView `json:"operation"`
}
