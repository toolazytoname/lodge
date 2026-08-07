package hub

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	cookieName                  = "lodge_session"
	sessionTTL                  = 30 * 24 * time.Hour
	maxConcurrentPasswordChecks = 1
)

type authenticator struct {
	passwordHash string
	sessionKey   []byte
	verifySlots  chan struct{}
}

func newAuthenticator(passwordHash string, sessionKey []byte) (*authenticator, error) {
	authn := &authenticator{}
	if passwordHash == "" {
		return authn, nil
	}
	if err := validatePasswordHash(passwordHash); err != nil {
		return nil, err
	}
	if len(sessionKey) < sessionKeyBytes {
		return nil, errors.New("authenticated mode requires an independent session key of at least 32 bytes")
	}
	authn.passwordHash = passwordHash
	authn.sessionKey = append([]byte(nil), sessionKey...)
	authn.verifySlots = make(chan struct{}, maxConcurrentPasswordChecks)
	return authn, nil
}

func (a *authenticator) enabled() bool {
	return a != nil && a.passwordHash != ""
}

// issueCookie signs an expiry and random nonce with a key independent of the
// login password. Including the verifier in the MAC input expires every old
// session when the operator changes the password hash.
func (a *authenticator) issueCookie() (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	expiry := strconv.FormatInt(time.Now().Add(sessionTTL).Unix(), 10)
	payload := expiry + "." + base64.RawURLEncoding.EncodeToString(nonce)
	signature := a.sign("session:" + a.passwordHash + ":" + payload)
	return payload + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (a *authenticator) validCookie(cookie string) bool {
	if !a.enabled() {
		return false
	}
	parts := strings.Split(cookie, ".")
	if len(parts) != 3 {
		return false
	}
	expiry, err := strconv.ParseInt(parts[0], 10, 64)
	now := time.Now()
	if err != nil || expiry < now.Unix() || expiry > now.Add(sessionTTL+time.Minute).Unix() {
		return false
	}
	nonce, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil || len(nonce) != 16 {
		return false
	}
	got, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil {
		return false
	}
	payload := parts[0] + "." + parts[1]
	want := a.sign("session:" + a.passwordHash + ":" + payload)
	return hmac.Equal(got, want)
}

func (a *authenticator) sign(value string) []byte {
	mac := hmac.New(sha256.New, a.sessionKey)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

func (a *authenticator) csrfToken(cookie string) string {
	return hex.EncodeToString(a.sign("csrf:" + cookie))
}

// verifyPassword bounds the aggregate Argon2 memory footprint. Returning
// available=false tells the handler to reject excess concurrent attempts
// immediately instead of letting them queue and exhaust Hub memory.
func (a *authenticator) verifyPassword(password string) (valid, available bool) {
	select {
	case a.verifySlots <- struct{}{}:
		defer func() { <-a.verifySlots }()
		return VerifyPassword(a.passwordHash, password), true
	default:
		return false, false
	}
}

// authed determines whether the request carries a valid session. Authentication
// is intentionally disabled only when no password hash was configured.
func (s *Server) authed(r *http.Request) bool {
	if !s.authn.enabled() {
		return true
	}
	cookie, err := r.Cookie(cookieName)
	return err == nil && s.authn.validCookie(cookie.Value)
}

// validCSRF binds a browser-readable CSRF value to the signed, HttpOnly
// session. A cross-site page cannot read /api/session because Lodge does not
// enable cross-origin reads.
func (s *Server) validCSRF(r *http.Request) bool {
	if !s.authn.enabled() {
		// Passwordless mode is allowed only on a private tailnet. JSON content-type
		// enforcement still prevents form-based cross-site writes.
		return true
	}
	cookie, err := r.Cookie(cookieName)
	if err != nil || !s.authn.validCookie(cookie.Value) {
		return false
	}
	want := s.authn.csrfToken(cookie.Value)
	got := r.Header.Get("X-CSRF-Token")
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// operationRequester is a pseudonymous session identifier for the single-user
// audit trail. It cannot be reversed into the HttpOnly cookie and changes with
// each login. Passwordless mode is explicitly identified as a tailnet session.
func (s *Server) operationRequester(r *http.Request) string {
	if !s.authn.enabled() {
		return "tailnet-operator"
	}
	cookie, err := r.Cookie(cookieName)
	if err != nil || !s.authn.validCookie(cookie.Value) {
		return "unknown-session"
	}
	digest := s.authn.sign("operator:" + cookie.Value)
	return "session:" + hex.EncodeToString(digest[:8])
}

// handleLogin POST /api/login {password}. Consecutive failures trigger the
// per-IP exponential backoff implemented in ratelimit.go.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if !s.authn.enabled() {
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
	passwordValid, verifierAvailable := s.authn.verifyPassword(body.Password)
	if !verifierAvailable {
		w.Header().Set("Retry-After", "1")
		writeJSONHub(w, http.StatusTooManyRequests, map[string]string{"error": "authentication_busy"})
		return
	}
	if !passwordValid {
		s.limiter.recordFailure(ip)
		// Do not distinguish a missing password from a wrong one.
		writeJSONHub(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	cookieValue, err := s.authn.issueCookie()
	if err != nil {
		writeJSONHub(w, http.StatusInternalServerError, map[string]string{"error": "session_unavailable"})
		return
	}
	s.limiter.recordSuccess(ip)
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: cookieValue,
		Path: "/", HttpOnly: true, MaxAge: int(sessionTTL / time.Second),
		Expires: time.Now().Add(sessionTTL), Secure: true,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSONHub(w, http.StatusOK, map[string]bool{"authed": true})
}

// handleLogout deletes the browser session cookie.
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

// handleSession lets the UI select the login or dashboard state.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	res := SessionResponse{Authed: s.authed(r)}
	if res.Authed && s.authn.enabled() {
		if cookie, err := r.Cookie(cookieName); err == nil {
			res.CSRFToken = s.authn.csrfToken(cookie.Value)
		}
	}
	writeJSONHub(w, http.StatusOK, res)
}
