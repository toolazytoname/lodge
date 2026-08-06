package hub

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	cookieName = "lodge_session"
	sessionTTL = 30 * 24 * time.Hour
)

// authKey 从登录密码派生 HMAC 密钥。好处：改密码即让所有旧会话 cookie 失效，
// 无需单独维护密钥或黑名单。
func authKey(password string) []byte {
	h := sha256.Sum256([]byte("lodge-hub-session:" + password))
	return h[:]
}

// issueCookie 签发会话 cookie 值：<过期unix>.<hex(hmac-sha256(过期unix))>。
func issueCookie(password string) string {
	exp := time.Now().Add(sessionTTL).Unix()
	payload := strconv.FormatInt(exp, 10)
	mac := hmac.New(sha256.New, authKey(password))
	mac.Write([]byte(payload))
	return payload + "." + hex.EncodeToString(mac.Sum(nil))
}

// validCookie 校验签名与有效期。
func validCookie(cookie, password string) bool {
	parts := strings.SplitN(cookie, ".", 2)
	if len(parts) != 2 {
		return false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	mac := hmac.New(sha256.New, authKey(password))
	mac.Write([]byte(parts[0]))
	want := mac.Sum(nil)
	got, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}
	return hmac.Equal(want, got)
}

// checkPassword 常数时间比较，杜绝按耗时侧信道爆破。
func checkPassword(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// authed 判断请求是否持有有效会话。password 为空（未启用认证）时恒为 true。
func (s *Server) authed(r *http.Request) bool {
	if s.password == "" {
		return true
	}
	c, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	return validCookie(c.Value, s.password)
}

// csrfToken binds a browser-readable CSRF value to the signed, HttpOnly session.
// A cross-site page can submit a request but cannot read this value from
// /api/session because Lodge does not enable cross-origin reads.
func csrfToken(cookie, password string) string {
	mac := hmac.New(sha256.New, authKey(password))
	mac.Write([]byte("csrf:" + cookie))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) validCSRF(r *http.Request) bool {
	if s.password == "" {
		// Passwordless mode is allowed only on a private tailnet. JSON content-type
		// enforcement still prevents form-based cross-site writes.
		return true
	}
	c, err := r.Cookie(cookieName)
	if err != nil || !validCookie(c.Value, s.password) {
		return false
	}
	want := csrfToken(c.Value, s.password)
	got := r.Header.Get("X-CSRF-Token")
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// handleLogin POST /api/login {password}。成功设置会话 cookie。
// 连续失败达到阈值后按指数退避锁定该 IP，见 ratelimit.go。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if s.password == "" {
		writeJSONHub(w, http.StatusOK, map[string]bool{"authed": true})
		return
	}
	if !hasJSONContentType(r) {
		writeJSONHub(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type 必须是 application/json"})
		return
	}
	ip := clientIP(r)
	if wait, locked := s.limiter.locked(ip); locked {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeJSONHub(w, http.StatusTooManyRequests, map[string]string{"error": "too_many_attempts"})
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || ensureJSONEOF(dec) != nil || len(body.Password) > 1024 {
		writeJSONHub(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if !checkPassword(body.Password, s.password) {
		s.limiter.recordFailure(ip)
		// 不区分「密码错」与「缺密码」，统一 401，减少信息泄露。
		writeJSONHub(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	s.limiter.recordSuccess(ip)
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: issueCookie(s.password),
		Path: "/", HttpOnly: true, MaxAge: int(sessionTTL / time.Second),
		Expires: time.Now().Add(sessionTTL), Secure: true,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSONHub(w, http.StatusOK, map[string]bool{"authed": true})
}

// handleLogout 删除会话 cookie。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if !s.authed(r) {
		writeJSONHub(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !s.validCSRF(r) {
		writeJSONHub(w, http.StatusForbidden, map[string]string{"error": "csrf"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Path: "/", MaxAge: -1, Expires: time.Unix(1, 0),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
	writeJSONHub(w, http.StatusOK, map[string]bool{"authed": false})
}

// handleSession GET /api/session —— 前端据此决定显示登录页还是 dashboard。
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	type sessionResponse struct {
		Authed    bool   `json:"authed"`
		CSRFToken string `json:"csrfToken,omitempty"`
	}
	res := sessionResponse{Authed: s.authed(r)}
	if res.Authed && s.password != "" {
		if c, err := r.Cookie(cookieName); err == nil {
			res.CSRFToken = csrfToken(c.Value, s.password)
		}
	}
	writeJSONHub(w, http.StatusOK, res)
}
