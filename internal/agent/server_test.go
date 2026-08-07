package agent

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/toolazytoname/lodge/internal/shared"
)

func newAuthedReq(method, path, token string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func do(t *testing.T, s *Server, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestServerRejectsMissingToken(t *testing.T) {
	s := NewServer("secret")
	w := do(t, s, newAuthedReq("GET", "/v1/ping", ""))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("无 token 应 401，得到 %d", w.Code)
	}
}

func TestServerRejectsWrongToken(t *testing.T) {
	s := NewServer("secret")
	w := do(t, s, newAuthedReq("GET", "/v1/ping", "wrong"))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("错 token 应 401，得到 %d", w.Code)
	}
}

func TestServerAcceptsCorrectToken(t *testing.T) {
	s := NewServer("secret")
	w := do(t, s, newAuthedReq("GET", "/v1/ping", "secret"))
	if w.Code != http.StatusOK {
		t.Errorf("正确 token 应 200，得到 %d: %s", w.Code, w.Body.String())
	}
	// ping 不校验 token 前缀泄露：这里验证返回体含版本
	if !contains(w.Body.String(), agentVersionJSON) {
		t.Errorf("ping 响应应含 agentVersion，得到 %s", w.Body.String())
	}
}

// agentVersionJSON 是 ping 响应里预期出现的片段。
const agentVersionJSON = `"agentVersion":"` + AgentVersion + `"`

func contains(s, sub string) bool { return len(s) >= len(sub) && indexOf(s, sub) >= 0 }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestFailureLimiterLocks(t *testing.T) {
	// 阈值 3，窗口 1 分钟，锁定 1 分钟
	f := newFailureLimiter(3, time.Minute, time.Minute)
	for i := 0; i < 3; i++ {
		f.fail()
	}
	if !f.locked() {
		t.Fatal("失败达阈值后应进入锁定")
	}
	// 成功一次也不应解除锁定（锁定期内必须等时间）
	f.success()
	if !f.locked() {
		t.Error("锁定期内 success 不应解锁")
	}
}

func TestServerActionsList(t *testing.T) {
	withActionHandlers(t,
		func() (shared.ActionsResponse, error) { return actionResponseFixture(), nil },
		func(shared.ActionDefinition) (shared.ActionExecutionResult, error) {
			return shared.ActionExecutionResult{}, nil
		},
	)
	s := NewServer("secret")
	w := do(t, s, newAuthedReq("GET", "/v1/actions", "secret"))
	if w.Code != http.StatusOK {
		t.Fatalf("列出动作应 200，得到 %d", w.Code)
	}
	if !contains(w.Body.String(), `"id":"logs:systemd:caddy.service"`) || contains(w.Body.String(), `"cmd"`) {
		t.Errorf("动作列表应只暴露类型化策略定义，不得暴露命令: %s", w.Body.String())
	}
}

func TestServerActionsExecute(t *testing.T) {
	var received shared.ActionDefinition
	withActionHandlers(t,
		func() (shared.ActionsResponse, error) { return actionResponseFixture(), nil },
		func(definition shared.ActionDefinition) (shared.ActionExecutionResult, error) {
			received = definition
			return shared.ActionExecutionResult{
				OK: true, ActionID: definition.ID, TargetKey: definition.TargetKey,
				Kind: definition.Kind, Logs: []string{"safe line"}, Summary: "返回 1 行有界脱敏日志",
			}, nil
		},
	)

	s := NewServer("secret")
	w := do(t, s, newAuthedReq("POST", "/v1/actions/logs:systemd:caddy.service", "secret"))
	if w.Code != http.StatusOK {
		t.Fatalf("执行动作应 200，得到 %d: %s", w.Code, w.Body.String())
	}
	if received.ID != "logs:systemd:caddy.service" || !contains(w.Body.String(), `"ok":true`) {
		t.Errorf("执行成功应 ok:true: %s", w.Body.String())
	}
}

func TestServerUnknownAction(t *testing.T) {
	withActionHandlers(t,
		func() (shared.ActionsResponse, error) { return actionResponseFixture(), nil },
		func(shared.ActionDefinition) (shared.ActionExecutionResult, error) {
			return shared.ActionExecutionResult{}, nil
		},
	)
	s := NewServer("secret")
	w := do(t, s, newAuthedReq("POST", "/v1/actions/rm-rf-slash", "secret"))
	if w.Code != http.StatusNotFound {
		t.Errorf("未知动作应 404，得到 %d", w.Code)
	}
}

func TestServerActionsRejectsAmbiguousPathAndQuery(t *testing.T) {
	withActionHandlers(t,
		func() (shared.ActionsResponse, error) { return actionResponseFixture(), nil },
		func(shared.ActionDefinition) (shared.ActionExecutionResult, error) {
			return shared.ActionExecutionResult{}, nil
		},
	)
	s := NewServer("secret")
	for _, path := range []string{
		"/v1/actions//logs:systemd:caddy.service",
		"/v1/actions/logs:systemd:caddy.service/extra",
	} {
		w := do(t, s, newAuthedReq(http.MethodPost, path, "secret"))
		if w.Code != http.StatusNotFound {
			t.Fatalf("ambiguous action path %q should be 404, got %d", path, w.Code)
		}
	}
	w := do(t, s, newAuthedReq(http.MethodPost, "/v1/actions/logs:systemd:caddy.service?command=id", "secret"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("action query should be rejected, got %d", w.Code)
	}
}

func TestServerActionsMethodNotAllowed(t *testing.T) {
	s := NewServer("secret")
	// 用 GET 命中带 id 的路径 → 应拒绝（执行必须 POST）
	w := do(t, s, newAuthedReq("GET", "/v1/actions/logs:systemd:caddy.service", "secret"))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET 执行动作应 405，得到 %d", w.Code)
	}
}

func TestServerActionsRejectsBody(t *testing.T) {
	withActionHandlers(t,
		func() (shared.ActionsResponse, error) { return actionResponseFixture(), nil },
		func(shared.ActionDefinition) (shared.ActionExecutionResult, error) {
			return shared.ActionExecutionResult{}, nil
		},
	)
	s := NewServer("secret")
	r := httptest.NewRequest(http.MethodPost, "/v1/actions/logs:systemd:caddy.service", strings.NewReader(`{"command":"rm -rf /"}`))
	r.Header.Set("Authorization", "Bearer secret")
	w := do(t, s, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("action body must be rejected, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServerActionsHidesHelperErrors(t *testing.T) {
	withActionHandlers(t,
		func() (shared.ActionsResponse, error) { return actionResponseFixture(), nil },
		func(shared.ActionDefinition) (shared.ActionExecutionResult, error) {
			return shared.ActionExecutionResult{}, errors.New("sudo: super secret raw stderr")
		},
	)
	s := NewServer("secret")
	w := do(t, s, newAuthedReq(http.MethodPost, "/v1/actions/logs:systemd:caddy.service", "secret"))
	if w.Code != http.StatusBadGateway || contains(w.Body.String(), "super secret") {
		t.Fatalf("helper failures must be generic, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServerActionsRejectsConcurrentExecution(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	withActionHandlers(t,
		func() (shared.ActionsResponse, error) { return actionResponseFixture(), nil },
		func(definition shared.ActionDefinition) (shared.ActionExecutionResult, error) {
			close(entered)
			<-release
			return shared.ActionExecutionResult{OK: true, ActionID: definition.ID, TargetKey: definition.TargetKey, Kind: definition.Kind}, nil
		},
	)
	s := NewServer("secret")
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- do(t, s, newAuthedReq(http.MethodPost, "/v1/actions/logs:systemd:caddy.service", "secret"))
	}()
	<-entered
	second := do(t, s, newAuthedReq(http.MethodPost, "/v1/actions/logs:systemd:caddy.service", "secret"))
	if second.Code != http.StatusConflict {
		t.Fatalf("concurrent action should be 409, got %d: %s", second.Code, second.Body.String())
	}
	close(release)
	if first := <-done; first.Code != http.StatusOK {
		t.Fatalf("first action should complete, got %d: %s", first.Code, first.Body.String())
	}
}

func actionResponseFixture() shared.ActionsResponse {
	return shared.ActionsResponse{Actions: []shared.ActionDefinition{{
		ID: "logs:systemd:caddy.service", TargetKey: "systemd:caddy.service", TargetLabel: "Caddy",
		TargetKind: shared.ActionTargetSystemd, Kind: shared.ActionLogs,
		Description: "读取最多 200 行脱敏日志：Caddy", Confirmation: "确认读取日志 Caddy", Risk: shared.ActionRiskRead,
	}}}
}

func withActionHandlers(t *testing.T, list func() (shared.ActionsResponse, error), execute func(shared.ActionDefinition) (shared.ActionExecutionResult, error)) {
	t.Helper()
	originalList, originalExecute := listApprovedActions, executeApprovedActionByID
	listApprovedActions, executeApprovedActionByID = list, execute
	t.Cleanup(func() {
		listApprovedActions, executeApprovedActionByID = originalList, originalExecute
	})
}

// 编译期保证返回类型对齐 shared 包契约。
var _ shared.Ping = shared.Ping{}
