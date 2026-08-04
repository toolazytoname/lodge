package agent

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"crypto/subtle"

	"github.com/toolazytoname/lodge/internal/shared"
)

// Server 是 lodge-agent 的 HTTP 服务。只应监听 127.0.0.1，由 tailscale serve
// 套 TLS 暴露到 tailnet —— 绝不直接监听 0.0.0.0。
type Server struct {
	token   string
	limiter *failureLimiter
	mux     *http.ServeMux
}

// NewServer 构造一个挂好路由的 agent 服务。
func NewServer(token string) *Server {
	s := &Server{
		token:   token,
		limiter: newFailureLimiter(10, time.Minute, time.Minute),
	}
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("/v1/ping", s.handle(s.ping))
	s.mux.HandleFunc("/v1/status", s.handle(s.status))
	s.mux.HandleFunc("/v1/services", s.handle(s.services))
	// /v1/actions 精确匹配列出；/v1/actions/ 子树匹配执行 {id}。
	// Go 1.19 ServeMux 不带尾斜杠的模式只匹配精确路径，故两个都注册。
	s.mux.HandleFunc("/v1/actions", s.handle(s.actions))
	s.mux.HandleFunc("/v1/actions/", s.handle(s.actions))
	return s
}

// ServeHTTP 让 Server 直接满足 http.Handler。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// handle 把「鉴权 + 限速」统一包到每个业务 handler 外层，避免每处手写。
func (s *Server) handle(h func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if s.limiter.locked() {
			writeErr(w, http.StatusTooManyRequests, "鉴权失败次数过多，稍后再试")
			return
		}
		if !s.authorized(r) {
			s.limiter.fail()
			// 不返回「token 错误」之类的细节，统一 401，减少信息泄露。
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		s.limiter.success()
		h(w, r)
	}
}

// authorized 常数时间比较 Bearer token，杜绝按耗时侧信道爆破。
func (s *Server) authorized(r *http.Request) bool {
	got := r.Header.Get("Authorization")
	want := "Bearer " + s.token
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func (s *Server) ping(w http.ResponseWriter, r *http.Request) {
	hostname, _, _, uptime := hostInfo()
	writeJSON(w, http.StatusOK, shared.Ping{
		OK: true, Hostname: hostname, AgentVer: AgentVersion, APIVersion: shared.APIVersion, UptimeSec: uptime,
	})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, CollectStatus())
}

func (s *Server) services(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, Discover())
}

// actionDef 是 GET /v1/actions 的元素：把白名单动作以安全形式暴露给前端。
type actionDef struct {
	ID   string `json:"id"`
	Desc string `json:"desc"`
	// Cmd 是给用户看的命令摘要（不含完整路径），便于二次确认时心里有数。
	Cmd string `json:"cmd"`
}

func (s *Server) actions(w http.ResponseWriter, r *http.Request) {
	// 路径 /v1/actions → 列出；/v1/actions/{id} → 执行（POST）。
	rest := strings.TrimPrefix(r.URL.Path, "/v1/actions")
	rest = strings.Trim(rest, "/")
	if rest == "" {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "GET 列出动作")
			return
		}
		defs := make([]actionDef, 0, len(privilegedWrite))
		for _, c := range privilegedWrite {
			defs = append(defs, actionDef{ID: c.ID, Desc: c.Desc, Cmd: strings.Join(c.Argv, " ")})
		}
		writeJSON(w, http.StatusOK, defs)
		return
	}

	// 执行动作
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST 执行动作")
		return
	}
	c, ok := commandByID(rest)
	if !ok {
		writeErr(w, http.StatusNotFound, "未知动作: "+rest)
		return
	}
	// 二次防线：runPriv 内部会再校验 argv 命中白名单。双重保险。
	stdout, stderr, err := runPriv(c.Argv)
	type actionResult struct {
		OK     bool   `json:"ok"`
		Stdout string `json:"stdout,omitempty"`
		Stderr string `json:"stderr,omitempty"`
		Error  string `json:"error,omitempty"`
	}
	res := actionResult{OK: err == nil}
	res.Stdout = strings.TrimSpace(string(stdout))
	res.Stderr = strings.TrimSpace(string(stderr))
	if err != nil {
		res.Error = err.Error()
	}
	code := http.StatusOK
	if err != nil {
		code = http.StatusInternalServerError
	}
	writeJSON(w, code, res)
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(shared.Error{Error: msg})
}

// failureLimiter 是一个简易的鉴权失败限流器：窗口内失败超过阈值则锁定一段时间。
//
// agent 是单租户、只在 tailnet 内可达，全局计数足够。token 本身高熵，
// 限流主要防「拿到网络位置但没 token」的人暴力试。
type failureLimiter struct {
	mu          sync.Mutex
	threshold   int
	window      time.Duration
	lockout     time.Duration
	fails       int
	windowStart time.Time
	lockedUntil time.Time
}

func newFailureLimiter(threshold int, window, lockout time.Duration) *failureLimiter {
	return &failureLimiter{threshold: threshold, window: window, lockout: lockout, windowStart: time.Now()}
}

func (f *failureLimiter) locked() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return time.Now().Before(f.lockedUntil)
}

func (f *failureLimiter) fail() {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	if now.Sub(f.windowStart) > f.window {
		f.fails = 0
		f.windowStart = now
	}
	f.fails++
	if f.fails >= f.threshold {
		f.lockedUntil = now.Add(f.lockout)
	}
}

func (f *failureLimiter) success() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fails = 0
}
