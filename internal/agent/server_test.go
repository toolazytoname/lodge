package agent

import (
	"net/http"
	"net/http/httptest"
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
	s := NewServer("secret")
	w := do(t, s, newAuthedReq("GET", "/v1/actions", "secret"))
	if w.Code != http.StatusOK {
		t.Fatalf("列出动作应 200，得到 %d", w.Code)
	}
	// 应包含 docker-prune 动作
	if !contains(w.Body.String(), "docker-prune") {
		t.Errorf("动作列表应含 docker-prune: %s", w.Body.String())
	}
}

func TestServerActionsExecute(t *testing.T) {
	// 注入假 runPriv：动作执行不真的跑 docker。
	orig := runPriv
	t.Cleanup(func() { runPriv = orig })
	runPriv = func(argv []string) ([]byte, []byte, error) {
		if argv[0] == "docker" && len(argv) > 1 && argv[1] == "system" {
			return []byte("Total Reclaimed Space: 0B\n"), nil, nil
		}
		return nil, nil, nil
	}

	s := NewServer("secret")
	w := do(t, s, newAuthedReq("POST", "/v1/actions/docker-prune", "secret"))
	if w.Code != http.StatusOK {
		t.Fatalf("执行动作应 200，得到 %d: %s", w.Code, w.Body.String())
	}
	if !contains(w.Body.String(), `"ok":true`) {
		t.Errorf("执行成功应 ok:true: %s", w.Body.String())
	}
}

func TestServerUnknownAction(t *testing.T) {
	s := NewServer("secret")
	w := do(t, s, newAuthedReq("POST", "/v1/actions/rm-rf-slash", "secret"))
	if w.Code != http.StatusNotFound {
		t.Errorf("未知动作应 404，得到 %d", w.Code)
	}
}

func TestServerActionsMethodNotAllowed(t *testing.T) {
	s := NewServer("secret")
	// 用 GET 命中带 id 的路径 → 应拒绝（执行必须 POST）
	w := do(t, s, newAuthedReq("GET", "/v1/actions/docker-prune", "secret"))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET 执行动作应 405，得到 %d", w.Code)
	}
}

// 编译期保证返回类型对齐 shared 包契约。
var _ shared.Ping = shared.Ping{}
