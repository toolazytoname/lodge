package hub

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"

	"github.com/toolazytoname/lodge/internal/shared"
)

//go:embed web/index.html
var webFS embed.FS

// Server 是 hub 的 HTTP 服务，给前端提供 API。
// 设了 password 即对所有 /api/*（登录/会话查询除外）强制会话认证。
type Server struct {
	store    Store
	password string
	mux      *http.ServeMux
	limiter  *loginLimiter
}

func NewServer(store Store, password string) *Server {
	s := &Server{store: store, password: password, limiter: newLoginLimiter()}
	s.mux = http.NewServeMux()

	// 公开路由：登录、会话查询、前端页面。
	s.mux.HandleFunc("/api/login", s.handleLogin)
	s.mux.HandleFunc("/api/logout", s.handleLogout)
	s.mux.HandleFunc("/api/session", s.handleSession)
	html, _ := fs.ReadFile(webFS, "web/index.html")
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(html)
	})

	// 受保护路由：需登录。
	s.mux.HandleFunc("/api/agents", s.auth(s.agents))
	s.mux.HandleFunc("/api/services", s.auth(s.services))
	s.mux.HandleFunc("/api/annotation", s.auth(s.annotation))
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
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
		h(w, r)
	}
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
	if r.Method != http.MethodPost {
		writeJSONHub(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST"})
		return
	}
	agentID := r.URL.Query().Get("agent")
	key := r.URL.Query().Get("key")
	if agentID == "" || key == "" {
		writeJSONHub(w, http.StatusBadRequest, map[string]string{"error": "需要 agent 和 key"})
		return
	}
	var ann Annotation
	if err := json.NewDecoder(r.Body).Decode(&ann); err != nil {
		writeJSONHub(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	s.store.SetAnnotation(agentID, key, ann)
	writeJSONHub(w, http.StatusOK, map[string]bool{"ok": true})
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
