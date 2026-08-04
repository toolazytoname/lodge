package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/toolazytoname/lodge/internal/shared"
)

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

func TestMemStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	s := NewMemStore(path)
	s.SetAgents([]AgentConfig{{ID: "bytedragon", Name: "bytedragon", URL: "http://x", Token: "t"}})
	s.Update("bytedragon", true, "", shared.Ping{AgentVer: "0.1.0"},
		&shared.Status{Hostname: "dancedragon"},
		[]shared.Service{{Key: "docker:nginx", MaxExposure: shared.ExposurePublic}})
	s.SetAnnotation("bytedragon", "docker:nginx", Annotation{Alias: "反代"})

	// 顺序：Snapshot 按配置顺序
	sn := s.Snapshot()
	if len(sn) != 1 || sn[0].ID != "bytedragon" || !sn[0].Online {
		t.Fatalf("快照错误: %+v", sn)
	}
	if sn[0].Services[0].Key != "docker:nginx" {
		t.Errorf("服务丢失")
	}

	// 重启：重新加载，agents + 注解应在，online 应重置为 false（观测不落盘）
	s2 := NewMemStore(path)
	sn2 := s2.Snapshot()
	if len(sn2) != 1 {
		t.Fatalf("重启后 agents 应保留，得到 %d", len(sn2))
	}
	if sn2[0].Online {
		t.Error("重启后 online 应为 false（观测快照不落盘）")
	}
	ann2 := s2.Annotations("bytedragon")
	if ann2["docker:nginx"].Alias != "反代" {
		t.Errorf("注解应持久化，得到 %+v", ann2)
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
		json.NewEncoder(w).Encode(shared.Ping{OK: true, AgentVer: "0.1.0", Hostname: "dancedragon"})
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

	store := NewMemStore("")
	store.SetAgents([]AgentConfig{{ID: "bd", Name: "bd", URL: srv.URL, Token: "tok"}})
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

	store := NewMemStore("")
	store.SetAgents([]AgentConfig{{ID: "dead", Name: "dead", URL: srv.URL, Token: "t"}})
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

	store := NewMemStore("")
	store.SetAgents([]AgentConfig{{ID: "bd", Name: "bd", URL: srv.URL, Token: "wrong"}})
	sc := NewScraper(store, time.Hour)
	sc.scrapeAll(context.Background())

	if store.Snapshot()[0].Online {
		t.Error("token 错应导致离线")
	}
}

// 安抚：测试不写 state 文件时不应残留。
var _ = os.Remove

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

func TestAuthCookieRoundTrip(t *testing.T) {
	pw := "s3cret"
	cookie := issueCookie(pw)
	if !validCookie(cookie, pw) {
		t.Error("签发的 cookie 应校验通过")
	}
	// 改密码后旧 cookie 失效
	if validCookie(cookie, "other") {
		t.Error("换密码后旧 cookie 应失效")
	}
	// 篡改 cookie
	if validCookie(cookie+".x", pw) {
		t.Error("篡改的 cookie 不应通过")
	}
}

func TestServerAuthGate(t *testing.T) {
	store := NewMemStore("")
	s := NewServer(store, "pw") // 启用认证

	// 未登录访问受保护 API → 401
	r := httptest.NewRequest("GET", "/api/agents", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("未登录应 401，得到 %d", w.Code)
	}

	// 登录
	body := strings.NewReader(`{"password":"pw"}`)
	r = httptest.NewRequest("POST", "/api/login", body)
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

	// 带 cookie 访问受保护 API → 200
	r = httptest.NewRequest("GET", "/api/agents", nil)
	r.AddCookie(&http.Cookie{Name: "lodge_session", Value: setCookie})
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("登录后应 200，得到 %d", w.Code)
	}

	// 错密码登录 → 401
	r = httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"password":"wrong"}`))
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("错密码应 401，得到 %d", w.Code)
	}
}

func TestServerNoPasswordIsOpen(t *testing.T) {
	// 未设 password（仅 tailnet 模式）→ /api/agents 直接放行
	s := NewServer(NewMemStore(""), "")
	r := httptest.NewRequest("GET", "/api/agents", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("无密码模式应放行，得到 %d", w.Code)
	}
}
