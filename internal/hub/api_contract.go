package hub

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
