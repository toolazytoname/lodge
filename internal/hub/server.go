package hub

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/toolazytoname/lodge/internal/shared"
)

//go:embed web/*
var webFS embed.FS

const (
	maxAnnotationBodyBytes = 16 << 10
	maxAgentIDBytes        = 128
	maxServiceKeyBytes     = 512
	maxAliasRunes          = 120
	maxNotesRunes          = 4000
	maxURLBytes            = 2048
)

// Server is the Hub HTTP boundary. A configured authenticator protects every
// API except login and session discovery.
type Server struct {
	store   Store
	authn   *authenticator
	mux     *http.ServeMux
	limiter *loginLimiter
}

// NewServerWithAuth requires an Argon2id verifier and an independent signing
// key. Passing an empty verifier explicitly selects private-tailnet-only mode.
func NewServerWithAuth(store Store, passwordHash string, sessionKey []byte) (*Server, error) {
	authn, err := newAuthenticator(passwordHash, sessionKey)
	if err != nil {
		return nil, err
	}
	s := &Server{store: store, authn: authn, limiter: newLoginLimiter()}
	s.mux = http.NewServeMux()

	// 公开路由：登录、会话查询、前端静态资源。
	s.mux.HandleFunc("/api/login", s.handleLogin)
	s.mux.HandleFunc("/api/logout", s.handleLogout)
	s.mux.HandleFunc("/api/session", s.handleSession)
	staticFS, err := fs.Sub(webFS, "web")
	if err != nil {
		panic("加载内嵌前端失败: " + err.Error())
	}
	s.mux.Handle("/", http.FileServer(http.FS(staticFS)))

	// 受保护路由：需登录。
	s.mux.HandleFunc("/api/agents", s.auth(s.agents))
	s.mux.HandleFunc("/api/services", s.auth(s.services))
	s.mux.HandleFunc("/api/annotation", s.auth(s.annotation))
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)
	s.mux.ServeHTTP(w, r)
}

// setSecurityHeaders applies to HTML, assets, and API responses so an error path
// cannot silently lose the browser security boundary.
func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; img-src 'self' data:; object-src 'none'; script-src 'self'; style-src 'self'")
	w.Header().Set("Permissions-Policy", "camera=(), geolocation=(), microphone=()")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
}

// RunCleanup 后台定期清理登录限速的陈旧记录，ctx 取消时退出。
func (s *Server) RunCleanup(ctx context.Context) {
	s.limiter.runCleanup(ctx)
}

// auth 包一层登录校验。未登录返回 401，前端据此跳登录页。
func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authed(r) {
			writeJSONHub(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		if isStateChanging(r.Method) && !s.validCSRF(r) {
			writeJSONHub(w, http.StatusForbidden, map[string]string{"error": "csrf"})
			return
		}
		h(w, r)
	}
}

func isStateChanging(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

// agentsResponse 是 /api/agents 的响应：每台机器的在线状态 + 指标摘要。
type agentSummary struct {
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

func (s *Server) agents(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	snaps := s.store.Snapshot()
	out := make([]agentSummary, 0, len(snaps))
	for _, sn := range snaps {
		sum := agentSummary{
			ID: sn.ID, Name: sn.Name, Online: sn.Online,
			LastSeen: sn.LastSeen, LastError: sn.LastError,
			ServiceCount: len(sn.Services),
		}
		for _, svc := range sn.Services {
			if svc.MaxExposure == shared.ExposurePublic {
				sum.PublicCount++
			}
		}
		if sn.Status != nil {
			sum.CPUs = sn.Status.Load.CPUs
			sum.Load1 = sn.Status.Load.One
			if sn.Status.Memory.TotalBytes > 0 {
				sum.MemUsedPct = int(sn.Status.Memory.UsedBytes * 100 / sn.Status.Memory.TotalBytes)
			}
			if d := rootDisk(sn.Status.Disks); d != nil && d.TotalBytes > 0 {
				sum.DiskUsedPct = int(d.UsedBytes * 100 / d.TotalBytes)
			}
		}
		out = append(out, sum)
	}
	writeJSONHub(w, http.StatusOK, out)
}

func (s *Server) services(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	agentID := r.URL.Query().Get("agent")
	snaps := s.store.Snapshot()
	hostByID := map[string]string{}
	for _, a := range s.store.Agents() {
		hostByID[a.ID] = a.PublicHost
	}
	type agentServices struct {
		Agent    AgentSnapshot `json:"agent"`
		Services []ServiceView `json:"services"`
	}
	out := make([]agentServices, 0, len(snaps))
	for _, sn := range snaps {
		if agentID != "" && sn.ID != agentID {
			continue
		}
		views := JoinServices(sn.Services, s.store.Annotations(sn.ID), hostByID[sn.ID])
		views = sortByExposure(views)
		out = append(out, agentServices{Agent: sn, Services: views})
	}
	writeJSONHub(w, http.StatusOK, out)
}

// annotation POST /api/annotation?agent=<id>&key=<key>
// body: {alias,url,hidden,notes} —— 设置某服务的注解（点服务直达的 URL 在此）。
func (s *Server) annotation(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	agentID := r.URL.Query().Get("agent")
	key := r.URL.Query().Get("key")
	if agentID == "" || key == "" || len(agentID) > maxAgentIDBytes || len(key) > maxServiceKeyBytes {
		writeJSONHub(w, http.StatusBadRequest, map[string]string{"error": "需要 agent 和 key"})
		return
	}
	if !s.hasAgent(agentID) {
		writeJSONHub(w, http.StatusNotFound, map[string]string{"error": "unknown agent"})
		return
	}
	if !hasJSONContentType(r) {
		writeJSONHub(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type 必须是 application/json"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAnnotationBodyBytes)
	var ann Annotation
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ann); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSONHub(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "body too large"})
			return
		}
		writeJSONHub(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if err := ensureJSONEOF(dec); err != nil {
		writeJSONHub(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	normalized, err := validateAnnotation(ann)
	if err != nil {
		writeJSONHub(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.store.SetAnnotation(r.Context(), agentID, key, normalized); err != nil {
		log.Printf("lodge hub annotation persistence: %v", err)
		writeJSONHub(w, http.StatusInternalServerError, map[string]string{"error": "annotation persistence failed"})
		return
	}
	writeJSONHub(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) hasAgent(id string) bool {
	for _, agent := range s.store.Agents() {
		if agent.ID == id {
			return true
		}
	}
	return false
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	writeJSONHub(w, http.StatusMethodNotAllowed, map[string]string{"error": method})
	return false
}

func validateAnnotation(ann Annotation) (Annotation, error) {
	ann.Alias = strings.TrimSpace(ann.Alias)
	ann.URL = strings.TrimSpace(ann.URL)
	ann.Notes = strings.TrimSpace(ann.Notes)
	if !utf8.ValidString(ann.Alias) || !utf8.ValidString(ann.Notes) || !utf8.ValidString(ann.URL) {
		return Annotation{}, errors.New("annotation must be valid UTF-8")
	}
	if utf8.RuneCountInString(ann.Alias) > maxAliasRunes {
		return Annotation{}, errors.New("alias too long")
	}
	if utf8.RuneCountInString(ann.Notes) > maxNotesRunes {
		return Annotation{}, errors.New("notes too long")
	}
	if len(ann.URL) > maxURLBytes {
		return Annotation{}, errors.New("url too long")
	}
	if ann.URL == "" {
		return ann, nil
	}
	u, err := url.Parse(ann.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return Annotation{}, errors.New("url must be an absolute http/https URL without credentials")
	}
	ann.URL = u.String()
	return ann, nil
}

func ensureJSONEOF(dec *json.Decoder) error {
	var extra interface{}
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func hasJSONContentType(r *http.Request) bool {
	contentType := r.Header.Get("Content-Type")
	if semi := strings.IndexByte(contentType, ';'); semi >= 0 {
		contentType = contentType[:semi]
	}
	return strings.EqualFold(strings.TrimSpace(contentType), "application/json")
}

func writeJSONHub(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func rootDisk(disks []shared.Disk) *shared.Disk {
	for i := range disks {
		if disks[i].Mount == "/" {
			return &disks[i]
		}
	}
	return nil
}

func sortByExposure(vs []ServiceView) []ServiceView {
	rank := map[string]int{
		string(shared.ExposurePublic):  0,
		string(shared.ExposureOther):   1,
		string(shared.ExposureTailnet): 2,
		string(shared.ExposureLocal):   3,
	}
	sorted := make([]ServiceView, len(vs))
	copy(sorted, vs)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && rank[string(sorted[j].MaxExposure)] < rank[string(sorted[j-1].MaxExposure)]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	return sorted
}

// 安抚未用 import（strings 留作路径扩展）。
var _ = strings.TrimSpace
