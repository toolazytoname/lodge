package hub

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	first, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	second, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("random salts should produce distinct password hashes")
	}
	if !VerifyPassword(first, "correct horse battery staple") {
		t.Fatal("correct password did not verify")
	}
	if VerifyPassword(first, "wrong password") {
		t.Fatal("wrong password verified")
	}
}

func TestPasswordHashRejectsMalformedAndUnsafeParameters(t *testing.T) {
	unsafe := "$argon2id$v=19$m=1,t=3,p=1$MDEyMzQ1Njc4OWFiY2RlZg$MDEyMzQ1Njc4OWFiY2RlZg"
	for _, encoded := range []string{"", "not-a-hash", unsafe} {
		if VerifyPassword(encoded, "password") {
			t.Fatalf("unsafe verifier was accepted: %q", encoded)
		}
		if err := validatePasswordHash(encoded); err == nil {
			t.Fatalf("unsafe verifier passed validation: %q", encoded)
		}
	}
}

func TestSessionKeyCreatedSecurelyAndReused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets", "session-secret")
	first, err := LoadOrCreateSessionKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != sessionKeyBytes {
		t.Fatalf("session key length = %d, want %d", len(first), sessionKeyBytes)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("session secret mode = %04o, want 0600", got)
	}
	second, err := LoadOrCreateSessionKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("session key changed across loads")
	}
}

func TestSessionKeyRejectsLoosePermissionsAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session-secret")
	if err := os.WriteFile(path, []byte("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateSessionKey(path); err == nil {
		t.Fatal("world-readable session key should be rejected")
	}

	link := filepath.Join(dir, "session-link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateSessionKey(link); err == nil {
		t.Fatal("symlinked session key should be rejected")
	}
}

func TestNewServerWithAuthRequiresIndependentSecret(t *testing.T) {
	passwordHash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewServerWithAuth(NewMemStore(), passwordHash, nil); err == nil {
		t.Fatal("authenticated server started without an independent session key")
	}
}

func TestLoginRejectsExcessConcurrentPasswordChecks(t *testing.T) {
	server := newTestServer(t, NewMemStore(), "pw")
	server.authn.verifySlots <- struct{}{}
	defer func() { <-server.authn.verifySlots }()

	w := httptest.NewRecorder()
	server.ServeHTTP(w, newJSONRequest(http.MethodPost, "/api/login", `{"password":"pw"}`))
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("busy password verifier should fail fast with 429, got %d", w.Code)
	}
}

func TestLoadConfigAndLegacyPasswordMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	passwordHash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	valid := `{"passwordHash":` + strconv.Quote(passwordHash) + `,"agents":[{"id":"host-a","url":"http://100.64.0.1:9101/","token":"secret"}]}`
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Agents[0].Name != "host-a" || config.Agents[0].URL != "http://100.64.0.1:9101" {
		t.Fatalf("config was not normalized: %+v", config.Agents[0])
	}
	resolved, legacy, err := ResolvePasswordHash(config)
	if err != nil || legacy || resolved != passwordHash {
		t.Fatalf("passwordHash resolution failed: legacy=%v err=%v", legacy, err)
	}

	legacyConfig := Config{Password: "legacy-password"}
	resolved, legacy, err = ResolvePasswordHash(legacyConfig)
	if err != nil || !legacy || !VerifyPassword(resolved, legacyConfig.Password) {
		t.Fatalf("legacy password migration failed: legacy=%v err=%v", legacy, err)
	}
}

func TestLoadConfigRejectsAmbiguousOrUnsafeInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cases := []string{
		`{"password":"one","passwordHash":"two","agents":[]}`,
		`{"unknown":true,"agents":[]}`,
		`{"agents":[{"id":"duplicate","url":"http://host-a","token":"a"},{"id":"duplicate","url":"http://host-b","token":"b"}]}`,
		`{"agents":[{"id":"host-a","url":"file:///etc/passwd","token":"a"}]}`,
		`{"agents":[{"id":"host-a","url":"http://user:pass@host-a","token":"a"}]}`,
		`{"agents":[{"id":"host-a","url":"http://host-a"}]}`,
	}
	for _, body := range cases {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil {
			t.Fatalf("unsafe config was accepted: %s", body)
		}
	}
}

func TestLoadConfigRejectsLoosePermissionsAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"agents":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("group/world-readable config should be rejected")
	}

	link := filepath.Join(dir, "config-link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(link); err == nil {
		t.Fatal("symlinked config should be rejected")
	}
}
