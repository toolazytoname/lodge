package agent

import (
	"encoding/json"
	"errors"
	"io"
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
	token              string
	limiter            *failureLimiter
	mux                *http.ServeMux
	actionMu           sync.Mutex
	actionsHandler     http.Handler
	deploymentsHandler http.Handler
}

var (
	listApprovedActions           = collectApprovedActionDefinitions
	executeApprovedActionByID     = executeApprovedActionDefinition
	listApprovedDeployments       = collectApprovedDeploymentDefinitions
	executeApprovedDeploymentByID = executeApprovedDeploymentDefinition
)

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
	s.actionsHandler = s.handle(s.actions)
	s.mux.Handle("/v1/actions", s.actionsHandler)
	s.mux.Handle("/v1/actions/", s.actionsHandler)
	s.deploymentsHandler = s.handle(s.deployments)
	s.mux.Handle("/v1/deployments", s.deploymentsHandler)
	s.mux.Handle("/v1/deployments/", s.deploymentsHandler)
	return s
}

// ServeHTTP 让 Server 直接满足 http.Handler。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// ServeMux canonicalizes paths before dispatch and can redirect a POST such
	// as /v1/actions//id to /v1/actions/id. Action paths must reach the strict
	// parser unchanged so ambiguous or encoded separators are rejected, not
	// normalized into an executable capability.
	if r.URL.Path == "/v1/actions" || strings.HasPrefix(r.URL.Path, "/v1/actions/") {
		s.actionsHandler.ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/v1/deployments" || strings.HasPrefix(r.URL.Path, "/v1/deployments/") {
		s.deploymentsHandler.ServeHTTP(w, r)
		return
	}
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

func (s *Server) actions(w http.ResponseWriter, r *http.Request) {
	// 路径 /v1/actions → 列出；/v1/actions/{id} → 执行（POST）。
	if r.URL.RawQuery != "" {
		writeErr(w, http.StatusBadRequest, "query_not_allowed")
		return
	}
	suffix := strings.TrimPrefix(r.URL.Path, "/v1/actions")
	if suffix == "" || suffix == "/" {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "GET 列出动作")
			return
		}
		response, err := listApprovedActions()
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "action_policy_unavailable")
			return
		}
		writeJSON(w, http.StatusOK, response)
		return
	}
	if !strings.HasPrefix(suffix, "/") || strings.Contains(suffix[1:], "/") {
		writeErr(w, http.StatusNotFound, "action_not_found")
		return
	}
	rest := suffix[1:]

	// 执行动作
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST 执行动作")
		return
	}
	if !requestBodyIsEmpty(r.Body) {
		writeErr(w, http.StatusBadRequest, "request_body_must_be_empty")
		return
	}
	response, err := listApprovedActions()
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "action_policy_unavailable")
		return
	}
	definition, found := findActionDefinition(response.Actions, rest)
	if !found {
		writeErr(w, http.StatusNotFound, "action_not_found")
		return
	}
	if !s.actionMu.TryLock() {
		writeErr(w, http.StatusConflict, "action_in_progress")
		return
	}
	defer s.actionMu.Unlock()
	result, err := executeApprovedActionByID(definition)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "action_execution_unavailable")
		return
	}
	if !result.OK {
		writeJSON(w, http.StatusBadGateway, result)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) deployments(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeErr(w, http.StatusBadRequest, "query_not_allowed")
		return
	}
	suffix := strings.TrimPrefix(r.URL.Path, "/v1/deployments")
	if suffix == "" || suffix == "/" {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "GET 列出部署")
			return
		}
		response, err := listApprovedDeployments()
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "deployment_policy_unavailable")
			return
		}
		writeJSON(w, http.StatusOK, response)
		return
	}
	if !strings.HasPrefix(suffix, "/") || strings.Contains(suffix[1:], "/") {
		writeErr(w, http.StatusNotFound, "deployment_not_found")
		return
	}
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST 执行部署")
		return
	}
	if !requestBodyIsEmpty(r.Body) {
		writeErr(w, http.StatusBadRequest, "request_body_must_be_empty")
		return
	}
	response, err := listApprovedDeployments()
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "deployment_policy_unavailable")
		return
	}
	definition, found := findDeploymentDefinition(response.Deployments, suffix[1:])
	if !found {
		writeErr(w, http.StatusNotFound, "deployment_not_found")
		return
	}
	// 普通动作和部署共享一把本机锁：不能在 Compose 切换期间重启同一服务。
	if !s.actionMu.TryLock() {
		writeErr(w, http.StatusConflict, "action_in_progress")
		return
	}
	defer s.actionMu.Unlock()
	result, err := executeApprovedDeploymentByID(definition)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "deployment_execution_unavailable")
		return
	}
	if !result.OK {
		writeJSON(w, http.StatusBadGateway, result)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func collectApprovedActionDefinitions() (shared.ActionsResponse, error) {
	stdout, _, err := runPrivileged(listActionsCommand)
	if err != nil {
		return shared.ActionsResponse{}, errors.New("action policy helper failed")
	}
	return decodeActionsResponse(stdout)
}

func executeApprovedActionDefinition(definition shared.ActionDefinition) (shared.ActionExecutionResult, error) {
	request, err := json.Marshal(shared.ActionExecutionRequest{ID: definition.ID})
	if err != nil {
		return shared.ActionExecutionResult{}, errors.New("action request encoding failed")
	}
	stdout, _, err := runPrivilegedInput(executeActionCommand, request)
	if err != nil {
		return shared.ActionExecutionResult{}, errors.New("action execution helper failed")
	}
	return decodeActionExecutionResult(stdout, definition)
}

func collectApprovedDeploymentDefinitions() (shared.DeploymentsResponse, error) {
	stdout, _, err := runPrivileged(listDeploymentsCommand)
	if err != nil {
		return shared.DeploymentsResponse{}, errors.New("deployment policy helper failed")
	}
	return decodeDeploymentsResponse(stdout)
}

func executeApprovedDeploymentDefinition(definition shared.DeploymentDefinition) (shared.DeploymentExecutionResult, error) {
	request, err := json.Marshal(shared.DeploymentExecutionRequest{ID: definition.ID})
	if err != nil {
		return shared.DeploymentExecutionResult{}, errors.New("deployment request encoding failed")
	}
	stdout, _, err := runPrivilegedInput(executeDeploymentCommand, request)
	if err != nil {
		return shared.DeploymentExecutionResult{}, errors.New("deployment execution helper failed")
	}
	return decodeDeploymentExecutionResult(stdout, definition)
}

func requestBodyIsEmpty(body io.Reader) bool {
	if body == nil {
		return true
	}
	content, err := io.ReadAll(io.LimitReader(body, 1))
	return err == nil && len(content) == 0
}

func findActionDefinition(definitions []shared.ActionDefinition, id string) (shared.ActionDefinition, bool) {
	for _, definition := range definitions {
		if definition.ID == id {
			return definition, true
		}
	}
	return shared.ActionDefinition{}, false
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
