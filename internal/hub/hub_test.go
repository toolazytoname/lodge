package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/toolazytoname/lodge/internal/domain"
	"github.com/toolazytoname/lodge/internal/shared"
)

func newJSONRequest(method, target, body string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func newTestServer(t *testing.T, store Store, password string) *Server {
	t.Helper()
	var passwordHash string
	var sessionKey []byte
	var err error
	if password != "" {
		passwordHash, err = HashPassword(password)
		sessionKey = []byte("0123456789abcdef0123456789abcdef")
	}
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServerWithAuth(store, passwordHash, sessionKey)
	if err != nil {
		t.Fatal(err)
	}
	return server
}

type fixedWebLinkProber struct{}

func (fixedWebLinkProber) Probe(_ context.Context, targets []webLinkTarget, checkedAt time.Time) []domain.WebLinkCheck {
	checks := make([]domain.WebLinkCheck, 0, len(targets))
	for _, target := range targets {
		checks = append(checks, domain.WebLinkCheck{
			HostID: target.hostID, WorkloadKey: target.workloadKey, URL: target.url,
			State: domain.WebLinkReachable, HTTPStatus: http.StatusNoContent, LatencyMS: 9, CheckedAt: checkedAt,
		})
	}
	return checks
}

func TestJoinServices(t *testing.T) {
	services := []shared.Service{
		{Key: "docker:nginx", Name: "nginx", MaxExposure: shared.ExposurePublic},
		{Key: "systemd:caddy.service", Name: "caddy", MaxExposure: shared.ExposurePublic},
	}
	ann := map[string]Annotation{
		"docker:nginx": {Alias: "主站反代", Hidden: false},
	}
	views := JoinServices(services, ann, "")
	if views[0].Alias != "主站反代" {
		t.Errorf("nginx 应带上注解别名，得到 %q", views[0].Alias)
	}
	if views[1].Alias != "" {
		t.Errorf("caddy 无注解，别名应为空，得到 %q", views[1].Alias)
	}
}

func TestMemStoreRuntimeProjection(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	if err := s.SetAgents(ctx, []AgentConfig{{ID: "bytedragon", Name: "bytedragon", URL: "http://x", Token: "t"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(ctx, "bytedragon", true, "", shared.Ping{AgentVer: "0.1.0"},
		&shared.Status{Hostname: "dancedragon"},
		[]shared.Service{{Key: "docker:nginx", MaxExposure: shared.ExposurePublic}}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAnnotation(ctx, "bytedragon", "docker:nginx", Annotation{Alias: "反代"}); err != nil {
		t.Fatal(err)
	}

	// 顺序：Snapshot 按配置顺序
	sn := s.Snapshot()
	if len(sn) != 1 || sn[0].ID != "bytedragon" || !sn[0].Online {
		t.Fatalf("快照错误: %+v", sn)
	}
	if sn[0].Services[0].Key != "docker:nginx" {
		t.Errorf("服务丢失")
	}

	if got := s.Annotations("bytedragon")["docker:nginx"].Alias; got != "反代" {
		t.Errorf("运行时注解错误: %q", got)
	}
}

func TestScraperPullsAgent(t *testing.T) {
	// 假 agent：返回 ping + status + services。
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/ping", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(shared.Ping{OK: true, AgentVer: "0.1.0", APIVersion: shared.APIVersion, Hostname: "dancedragon"})
	})
	mux.HandleFunc("/v1/status", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(shared.Status{Hostname: "dancedragon", Load: shared.Load{CPUs: 2, One: 0.1}})
	})
	mux.HandleFunc("/v1/services", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(shared.ServicesResponse{Services: []shared.Service{
			{Key: "systemd:caddy.service", Name: "caddy", MaxExposure: shared.ExposurePublic},
		}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	store := NewMemStore()
	store.SetAgents(context.Background(), []AgentConfig{{ID: "bd", Name: "bd", URL: srv.URL, Token: "tok"}})
	sc := NewScraper(store, time.Hour) // interval 大，只手动跑一次
	sc.scrapeAll(context.Background())

	sn := store.Snapshot()[0]
	if !sn.Online {
		t.Fatal("agent 应在线")
	}
	if sn.AgentVer != "0.1.0" {
		t.Errorf("agent 版本未采集: %s", sn.AgentVer)
	}
	if len(sn.Services) != 1 || sn.Services[0].Key != "systemd:caddy.service" {
		t.Errorf("服务未采集: %+v", sn.Services)
	}
}

func TestScraperOfflineOnFailure(t *testing.T) {
	// 指向一个立即关闭的服务器 → 连接失败 → 离线，但不 panic。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	store := NewMemStore()
	store.SetAgents(context.Background(), []AgentConfig{{ID: "dead", Name: "dead", URL: srv.URL, Token: "t"}})
	sc := NewScraper(store, time.Hour)
	sc.scrapeAll(context.Background())

	sn := store.Snapshot()[0]
	if sn.Online {
		t.Error("连不上的 agent 应标记离线")
	}
	if sn.LastError == "" {
		t.Error("离线应记录失败原因")
	}
}

func TestScraperWrongToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer right" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(shared.Ping{OK: true})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	store := NewMemStore()
	store.SetAgents(context.Background(), []AgentConfig{{ID: "bd", Name: "bd", URL: srv.URL, Token: "wrong"}})
	sc := NewScraper(store, time.Hour)
	sc.scrapeAll(context.Background())

	if store.Snapshot()[0].Online {
		t.Error("token 错应导致离线")
	}
}

func TestScraperRejectsIncompatibleAgentAPI(t *testing.T) {
	statusRequested := false
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/ping", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(shared.Ping{OK: true, APIVersion: "v999"})
	})
	mux.HandleFunc("/v1/status", func(w http.ResponseWriter, r *http.Request) {
		statusRequested = true
		w.WriteHeader(http.StatusInternalServerError)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	store := NewMemStore()
	store.SetAgents(context.Background(), []AgentConfig{{ID: "future", Name: "future", URL: server.URL, Token: "token"}})
	scraper := NewScraper(store, time.Hour)
	if err := scraper.scrapeAll(context.Background()); err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("incompatible Agent API was not reported: %v", err)
	}
	snapshot := store.Snapshot()[0]
	if snapshot.Online || !strings.Contains(snapshot.LastError, "requires") {
		t.Fatalf("incompatible Agent should be explicitly offline: %+v", snapshot)
	}
	if statusRequested {
		t.Fatal("Hub should not request other endpoints after incompatible ping")
	}
}

func TestGuessURL(t *testing.T) {
	host := "bytedragon.weichao.site"
	// 80/443 优先，且不带端口号
	if got := guessURL(shared.Service{Ports: []shared.Port{{Port: 2019}, {Port: 80}}}, host); got != "http://"+host {
		t.Errorf("应优先 80 且不带端口，得到 %q", got)
	}
	if got := guessURL(shared.Service{Ports: []shared.Port{{Port: 443}}}, host); got != "https://"+host {
		t.Errorf("443 → https 不带端口，得到 %q", got)
	}
	// 非 web 端口不猜
	if got := guessURL(shared.Service{Ports: []shared.Port{{Port: 22}}}, host); got != "" {
		t.Errorf("ssh:22 不应猜，得到 %q", got)
	}
	if got := guessURL(shared.Service{Ports: []shared.Port{{Port: 9090}}}, host); got != "" {
		t.Errorf("admin:9090 不应猜，得到 %q", got)
	}
	// 9443 这类 web 端口才猜
	if got := guessURL(shared.Service{Ports: []shared.Port{{Port: 9443}}}, host); got != "https://"+host+":9443" {
		t.Errorf("9443 应猜，得到 %q", got)
	}
	// 无 publicHost → 空
	if guessURL(shared.Service{Ports: []shared.Port{{Port: 80}}}, "") != "" {
		t.Error("无 publicHost 应返回空")
	}
}

func TestJoinServicesPrefersDiscoveredProxyRouteAndKeepsAllLinks(t *testing.T) {
	services := []shared.Service{{
		Key: "systemd:caddy.service", Name: "caddy",
		Routes: []shared.ProxyRoute{
			{Scheme: "https", Port: 9443, Path: "/admin/*", Upstreams: []string{"127.0.0.1:4000"}},
			{Scheme: "https", Host: "app.example.test", Port: 8443, Path: "/", Upstreams: []string{"127.0.0.1:3000"}},
		},
	}}
	views := JoinServices(services, nil, "203.0.113.10")
	if len(views) != 1 || len(views[0].Routes) != 2 {
		t.Fatalf("proxy routes were not retained: %+v", views)
	}
	if views[0].URL != "https://app.example.test:8443/" {
		t.Fatalf("first discovered route should be the primary URL, got %q", views[0].URL)
	}
	if views[0].Routes[1].URL != "https://203.0.113.10:9443/admin/" {
		t.Fatalf("default host or wildcard path was not resolved safely: %+v", views[0].Routes[1])
	}
	encoded, err := json.Marshal(views[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(encoded), `"routes"`) != 1 || !strings.Contains(string(encoded), `"url":"https://app.example.test:8443/"`) {
		t.Fatalf("route views should replace raw embedded routes in Web JSON: %s", encoded)
	}

	views = JoinServices(services, map[string]Annotation{
		"systemd:caddy.service": {URL: "https://override.example.test"},
	}, "203.0.113.10")
	if views[0].URL != "https://override.example.test" || len(views[0].Routes) != 2 {
		t.Fatalf("annotation override should win without hiding discovered routes: %+v", views[0])
	}
	if got := proxyRouteURL(shared.ProxyRoute{Scheme: "https", Host: "2001:db8::1", Port: 443, Path: "/"}, ""); got != "https://[2001:db8::1]/" {
		t.Fatalf("IPv6 route URL was not bracketed: %q", got)
	}
}

func TestAuthCookieRoundTrip(t *testing.T) {
	passwordHash, err := HashPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	key := []byte("0123456789abcdef0123456789abcdef")
	authn, err := newAuthenticator(passwordHash, key)
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := authn.issueCookie()
	if err != nil {
		t.Fatal(err)
	}
	if !authn.validCookie(cookie) {
		t.Error("签发的 cookie 应校验通过")
	}
	otherHash, err := HashPassword("other")
	if err != nil {
		t.Fatal(err)
	}
	changedPassword, err := newAuthenticator(otherHash, key)
	if err != nil {
		t.Fatal(err)
	}
	if changedPassword.validCookie(cookie) {
		t.Error("换密码后旧 cookie 应失效")
	}
	changedKey, err := newAuthenticator(passwordHash, []byte("abcdef0123456789abcdef0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	if changedKey.validCookie(cookie) {
		t.Error("换会话密钥后旧 cookie 应失效")
	}
	if authn.validCookie(cookie + "x") {
		t.Error("篡改的 cookie 不应通过")
	}
}

func TestServerAuthGate(t *testing.T) {
	store := NewMemStore()
	s := newTestServer(t, store, "pw") // 启用认证

	// 未登录访问受保护 API → 401
	r := httptest.NewRequest("GET", "/api/agents", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("未登录应 401，得到 %d", w.Code)
	}

	// 登录
	r = newJSONRequest("POST", "/api/login", `{"password":"pw"}`)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("登录应 200，得到 %d", w.Code)
	}
	var setCookie string
	for _, c := range w.Result().Cookies() {
		if c.Name == "lodge_session" {
			setCookie = c.Value
		}
	}
	if setCookie == "" {
		t.Fatal("登录未下发会话 cookie")
	}
	loginCookie := w.Result().Cookies()[0]
	if !loginCookie.Secure || !loginCookie.HttpOnly || loginCookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("会话 cookie 缺少安全属性: %+v", loginCookie)
	}

	// 带 cookie 访问受保护 API → 200
	r = httptest.NewRequest("GET", "/api/agents", nil)
	r.AddCookie(&http.Cookie{Name: "lodge_session", Value: setCookie})
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("登录后应 200，得到 %d", w.Code)
	}

	// 错密码登录 → 401
	r = newJSONRequest("POST", "/api/login", `{"password":"wrong"}`)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("错密码应 401，得到 %d", w.Code)
	}
}

func TestServerNoPasswordIsOpen(t *testing.T) {
	// 未设 password（仅 tailnet 模式）→ /api/agents 直接放行
	s := newTestServer(t, NewMemStore(), "")
	r := httptest.NewRequest("GET", "/api/agents", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("无密码模式应放行，得到 %d", w.Code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	s := newTestServer(t, NewMemStore(), "pw")

	// 阈值内不锁，正常 401；第 threshold+1 次失败触发锁定
	for i := 0; i < rateLimitThreshold+1; i++ {
		r := newJSONRequest("POST", "/api/login", `{"password":"wrong"}`)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次错密码应 401，得到 %d", i+1, w.Code)
		}
	}

	// 已锁定，即使密码正确也拒绝
	r := newJSONRequest("POST", "/api/login", `{"password":"pw"}`)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("锁定期内应 429，得到 %d", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("锁定响应应带 Retry-After")
	}
}

func TestLoginRateLimitResetsOnSuccess(t *testing.T) {
	s := newTestServer(t, NewMemStore(), "pw")

	// 若干次失败但不到阈值
	for i := 0; i < rateLimitThreshold-1; i++ {
		r := newJSONRequest("POST", "/api/login", `{"password":"wrong"}`)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
	}
	// 登录成功
	r := newJSONRequest("POST", "/api/login", `{"password":"pw"}`)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("登录应成功，得到 %d", w.Code)
	}
	// 成功后失败计数应清零，之后错密码不会立刻被锁
	r = newJSONRequest("POST", "/api/login", `{"password":"wrong"}`)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("成功登录后失败计数应重置，得到 %d", w.Code)
	}
}

func loginSession(t *testing.T, s *Server, password string) (*http.Cookie, string) {
	t.Helper()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, newJSONRequest(http.MethodPost, "/api/login", fmt.Sprintf(`{"password":%q}`, password)))
	if w.Code != http.StatusOK {
		t.Fatalf("登录失败: HTTP %d %s", w.Code, w.Body.String())
	}
	var cookie *http.Cookie
	for _, candidate := range w.Result().Cookies() {
		if candidate.Name == cookieName {
			cookie = candidate
			break
		}
	}
	if cookie == nil {
		t.Fatal("登录未设置会话 cookie")
	}

	r := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("会话查询失败: HTTP %d", w.Code)
	}
	var session struct {
		Authed    bool   `json:"authed"`
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if !session.Authed || session.CSRFToken == "" {
		t.Fatalf("会话缺少认证或 CSRF token: %+v", session)
	}
	return cookie, session.CSRFToken
}

func TestAnnotationRequiresCSRFAndValidatesURL(t *testing.T) {
	store := NewMemStore()
	store.SetAgents(context.Background(), []AgentConfig{{ID: "host-a", Name: "host-a", URL: "http://agent"}})
	s := newTestServer(t, store, "pw")
	cookie, token := loginSession(t, s, "pw")
	target := "/api/annotation?agent=host-a&key=systemd:caddy.service"

	request := newJSONRequest(http.MethodPost, target, `{"url":"https://admin.example.test"}`)
	request.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, request)
	if w.Code != http.StatusForbidden {
		t.Fatalf("缺少 CSRF token 应拒绝，得到 HTTP %d", w.Code)
	}

	request = newJSONRequest(http.MethodPost, target, `{"url":"javascript:alert(1)"}`)
	request.AddCookie(cookie)
	request.Header.Set("X-CSRF-Token", token)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, request)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("危险 URL 应拒绝，得到 HTTP %d", w.Code)
	}

	request = newJSONRequest(http.MethodPost, target, `{"url":"https://admin.example.test","unexpected":true}`)
	request.AddCookie(cookie)
	request.Header.Set("X-CSRF-Token", token)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, request)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("未知 JSON 字段应拒绝，得到 HTTP %d", w.Code)
	}

	request = newJSONRequest(http.MethodPost, target, `{"url":"https://admin.example.test/path"}`)
	request.AddCookie(cookie)
	request.Header.Set("X-CSRF-Token", token)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, request)
	if w.Code != http.StatusOK {
		t.Fatalf("合法注解应保存，得到 HTTP %d %s", w.Code, w.Body.String())
	}
	if got := store.Annotations("host-a")["systemd:caddy.service"].URL; got != "https://admin.example.test/path" {
		t.Errorf("保存 URL 错误: %q", got)
	}
}

func TestAnnotationRejectsUnknownAgentAndOversizedBody(t *testing.T) {
	store := NewMemStore()
	store.SetAgents(context.Background(), []AgentConfig{{ID: "host-a", URL: "http://agent"}})
	s := newTestServer(t, store, "")

	request := newJSONRequest(http.MethodPost, "/api/annotation?agent=missing&key=port:tcp/80", `{"url":"https://example.test"}`)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, request)
	if w.Code != http.StatusNotFound {
		t.Fatalf("未知 agent 应返回 404，得到 %d", w.Code)
	}

	large := `{"notes":"` + strings.Repeat("x", maxAnnotationBodyBytes) + `"}`
	request = newJSONRequest(http.MethodPost, "/api/annotation?agent=host-a&key=port:tcp/80", large)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, request)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超大 body 应返回 413，得到 %d", w.Code)
	}
}

func TestWebLinkChecksRequireAuthenticationCSRFAndPersistEvidence(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	if err := store.SetAgents(ctx, []AgentConfig{{ID: "host-a", Name: "Host A"}}); err != nil {
		t.Fatal(err)
	}
	services := []shared.Service{{
		Key: "systemd:caddy.service", Kind: shared.KindSystemd, Name: "caddy",
		Routes:      []shared.ProxyRoute{{Scheme: "https", Host: "app.example.test", Port: 443, Path: "/"}},
		MaxExposure: shared.ExposurePublic,
	}}
	if err := store.Update(ctx, "host-a", true, "", shared.Ping{}, nil, services, time.Now()); err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, store, "pw")
	server.prober = fixedWebLinkProber{}

	w := httptest.NewRecorder()
	server.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/link-checks", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated link checks returned HTTP %d", w.Code)
	}

	cookie, csrfToken := loginSession(t, server, "pw")
	request := httptest.NewRequest(http.MethodPost, "/api/link-checks", nil)
	request.AddCookie(cookie)
	w = httptest.NewRecorder()
	server.ServeHTTP(w, request)
	if w.Code != http.StatusForbidden {
		t.Fatalf("link probe without CSRF returned HTTP %d", w.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/link-checks", nil)
	request.AddCookie(cookie)
	request.Header.Set("X-CSRF-Token", csrfToken)
	w = httptest.NewRecorder()
	server.ServeHTTP(w, request)
	if w.Code != http.StatusOK {
		t.Fatalf("link probe returned HTTP %d: %s", w.Code, w.Body.String())
	}
	var response WebLinkChecksResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Summary.Total != 1 || response.Summary.Reachable != 1 || len(response.Checks) != 1 {
		t.Fatalf("link probe response mismatch: %+v", response)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/link-checks", nil)
	request.AddCookie(cookie)
	w = httptest.NewRecorder()
	server.ServeHTTP(w, request)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"state":"reachable"`) {
		t.Fatalf("persisted link evidence missing: HTTP %d %s", w.Code, w.Body.String())
	}
}

func TestObservationHistoryAPIIsBoundedAndHostScoped(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	if err := store.SetAgents(ctx, []AgentConfig{{ID: "host-a", Name: "Host A"}}); err != nil {
		t.Fatal(err)
	}
	status := &shared.Status{
		Hostname: "host-a", Load: shared.Load{CPUs: 2, One: 0.75},
		Memory: shared.Memory{TotalBytes: 1000, UsedBytes: 600},
		Disks:  []shared.Disk{{Mount: "/", TotalBytes: 1000, UsedBytes: 400}},
	}
	services := []shared.Service{{
		Key: "systemd:worker.service", Kind: shared.KindSystemd, Name: "worker", Status: "failed",
		Ports: []shared.Port{{Proto: "tcp", Bind: "0.0.0.0", Port: 8080, Exposure: shared.ExposurePublic}},
	}}
	first := time.Date(2026, 8, 8, 1, 2, 3, 0, time.UTC)
	if err := store.Update(ctx, "host-a", true, "", shared.Ping{AgentVer: "0.5.0"}, status, services, first); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(ctx, "host-a", false, "fixture timeout", shared.Ping{AgentVer: "0.5.0"}, status, services, first.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, store, "")

	w := httptest.NewRecorder()
	server.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/history?agent=host-a&limit=1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("history API returned HTTP %d: %s", w.Code, w.Body.String())
	}
	var response HostHistoryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.AgentID != "host-a" || len(response.Points) != 1 || response.Points[0].Online {
		t.Fatalf("history response mismatch: %+v", response)
	}
	point := response.Points[0]
	if point.FailedWorkloadCount != 1 || point.WildcardEndpointCount != 1 || point.MemoryUsedPct != 60 || point.DiskUsedPct != 40 {
		t.Fatalf("history projection mismatch: %+v", point)
	}

	for target, expected := range map[string]int{
		"/api/history":                        http.StatusBadRequest,
		"/api/history?agent=missing":          http.StatusNotFound,
		"/api/history?agent=host-a&limit=501": http.StatusBadRequest,
	} {
		w = httptest.NewRecorder()
		server.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		if w.Code != expected {
			t.Errorf("GET %s returned HTTP %d, want %d", target, w.Code, expected)
		}
	}
}

func TestHubSecurityHeadersAndStaticAssets(t *testing.T) {
	s := newTestServer(t, NewMemStore(), "")
	for _, path := range []string{"/", "/app.css", "/app.js", "/api/session"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: HTTP %d", path, w.Code)
		}
		if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("GET %s 缺少 nosniff", path)
		}
		csp := w.Header().Get("Content-Security-Policy")
		if csp == "" || strings.Contains(csp, "unsafe-inline") {
			t.Errorf("GET %s CSP 不严格: %q", path, csp)
		}
	}

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	html := w.Body.String()
	if strings.Contains(html, "onclick=") || strings.Contains(html, "<style") {
		t.Errorf("HTML 不应包含 CSP 无法约束的内联代码")
	}
	for _, marker := range []string{
		`data-page-panel="overview"`,
		`data-page-panel="hosts"`,
		`data-page-panel="services"`,
		`data-page-panel="security"`,
		`data-page-panel="operations"`,
		`id="annotationDialog"`,
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("Web 产品壳缺少 %s", marker)
		}
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	js := w.Body.String()
	if strings.Contains(js, ".innerHTML") || strings.Contains(js, "insertAdjacentHTML") {
		t.Errorf("前端不得用 HTML 字符串渲染不可信数据")
	}
	if strings.Contains(js, "window.prompt") {
		t.Errorf("服务配置不得回退到阻塞式 prompt")
	}
}

func TestServicesAPIUsesCompactTypedAgentContract(t *testing.T) {
	store := NewMemStore()
	if err := store.SetAgents(context.Background(), []AgentConfig{{ID: "host-a", Name: "Host A", PublicHost: "host-a.example"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(context.Background(), "host-a", true, "", shared.Ping{AgentVer: "0.4.1"},
		&shared.Status{Hostname: "host-a", Load: shared.Load{CPUs: 2}},
		[]shared.Service{{Key: "systemd:web.service", Kind: shared.KindSystemd, Name: "web", Status: "active/running"}},
		time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, store, "")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/services", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/services: HTTP %d", recorder.Code)
	}
	var response []AgentServices
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response) != 1 || response[0].Agent.AgentVersion != "0.4.1" || len(response[0].Services) != 1 {
		t.Fatalf("unexpected compact service response: %+v", response)
	}
	var raw []struct {
		Agent map[string]interface{} `json:"agent"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"status", "services"} {
		if _, found := raw[0].Agent[forbidden]; found {
			t.Fatalf("service response duplicated raw agent %s payload: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestClientIPTrustsXFFOnlyFromLoopback(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	if ip := clientIP(r); ip != "1.2.3.4" {
		t.Errorf("回环连接应信任 XFF 首个地址，得到 %q", ip)
	}

	r2 := httptest.NewRequest("GET", "/", nil)
	r2.RemoteAddr = "9.9.9.9:5555"
	r2.Header.Set("X-Forwarded-For", "1.2.3.4")
	if ip := clientIP(r2); ip != "9.9.9.9" {
		t.Errorf("非回环连接不应信任 XFF，得到 %q", ip)
	}
}
