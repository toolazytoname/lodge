package hub

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

func TestMigrateConfigPasswordIsAtomicOwnerOnlyAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	const plaintext = "legacy password must disappear"
	body := `{"_comment":"preserve","password":` + strconv.Quote(plaintext) + `,"agents":[{"id":"host-a","url":"http://agent","token":"secret"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	migrated, err := MigrateConfigPassword(path)
	if err != nil {
		t.Fatal(err)
	}
	if !migrated {
		t.Fatal("plaintext config was not migrated")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), plaintext) || strings.Contains(string(contents), `"password"`) {
		t.Fatal("plaintext password remained in migrated config")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("migrated config mode = %04o, want 0600", info.Mode().Perm())
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Comment != "preserve" || !VerifyPassword(config.PasswordHash, plaintext) || config.Agents[0].Token != "secret" {
		t.Fatal("migration did not preserve config or verifier semantics")
	}
	before := append([]byte(nil), contents...)
	migrated, err = MigrateConfigPassword(path)
	if err != nil || migrated {
		t.Fatalf("repeat migration: migrated=%v err=%v", migrated, err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("idempotent migration rewrote an already migrated config")
	}
}

func TestUpsertAgentConfigIsAtomicOwnerOnlyAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	passwordHash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	body := `{"_comment":"preserve","_comment_agents":"also preserve","passwordHash":` + strconv.Quote(passwordHash) + `,"agents":[{"id":"host-a","name":"A","url":"http://agent-a:9101","token":"secret-a"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	added, err := UpsertAgentConfig(path, AgentConfig{
		ID: "host-b", URL: "http://agent-b:9101/", Token: "secret-b", PublicHost: "host-b.example",
	})
	if err != nil || !added {
		t.Fatalf("add Agent: added=%v err=%v", added, err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Comment != "preserve" || config.CommentAgents != "also preserve" || config.PasswordHash != passwordHash {
		t.Fatal("upsert did not preserve unrelated owner configuration")
	}
	if len(config.Agents) != 2 || config.Agents[0].ID != "host-a" || config.Agents[1].Name != "host-b" || config.Agents[1].URL != "http://agent-b:9101" {
		t.Fatalf("Agent was not appended and normalized: %+v", config.Agents)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("updated config mode = %04o, want 0600", info.Mode().Perm())
	}

	updated := config.Agents[0]
	updated.Name = "A updated"
	changed, err := UpsertAgentConfig(path, updated)
	if err != nil || !changed {
		t.Fatalf("update Agent: changed=%v err=%v", changed, err)
	}
	config, err = LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Agents) != 2 || config.Agents[0].Name != "A updated" || config.Agents[1].ID != "host-b" {
		t.Fatalf("update changed Agent order or wrong entry: %+v", config.Agents)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed, err = UpsertAgentConfig(path, config.Agents[0])
	if err != nil || changed {
		t.Fatalf("idempotent upsert: changed=%v err=%v", changed, err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("idempotent upsert rewrote an unchanged config")
	}
}

func TestUpsertAgentConfigRejectsUnsafeInputWithoutModifyingFile(t *testing.T) {
	unsafe := []AgentConfig{
		{ID: "host\nline", URL: "http://agent:9101", Token: "secret"},
		{ID: "host", URL: "http://agent:9101/admin", Token: "secret"},
		{ID: "host", URL: "http://agent:9101", Token: "secret\n"},
		{ID: "host", URL: "http://agent:9101", Token: "secret", PublicHost: "example.com:443"},
	}
	for index, agent := range unsafe {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		const original = `{"agents":[]}`
		if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
			t.Fatal(err)
		}
		if changed, err := UpsertAgentConfig(path, agent); err == nil || changed {
			t.Fatalf("unsafe Agent %d accepted: changed=%v err=%v", index, changed, err)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(contents) != original {
			t.Fatalf("unsafe Agent %d modified config", index)
		}
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
		`{"agents":[{"id":"host-a","url":"http://host-a/admin","token":"a"}]}`,
		`{"agents":[{"id":"host-a\nline","url":"http://host-a","token":"a"}]}`,
		`{"agents":[{"id":"host-a","url":"http://host-a","token":"a\n"}]}`,
		`{"agents":[{"id":"host-a","url":"http://host-a","token":"a","publicHost":"example.com:443"}]}`,
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

func TestLoadConfigNormalizesSecureWebhook(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := `{"webhook":{"url":" https://hooks.example.test/lodge?tenant=one ","secretFile":"/etc/lodge-hub/webhook-secret"},"agents":[]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Webhook == nil || config.Webhook.URL != "https://hooks.example.test/lodge?tenant=one" || config.Webhook.CooldownSeconds != defaultWebhookCooldownSeconds {
		t.Fatalf("webhook was not normalized: %+v", config.Webhook)
	}
}

func TestLoadConfigRejectsUnsafeWebhook(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cases := []string{
		`{"webhook":{"url":"http://hooks.example.test/lodge"},"agents":[]}`,
		`{"webhook":{"url":"https://user:pass@hooks.example.test/lodge"},"agents":[]}`,
		`{"webhook":{"url":"https://hooks.example.test/lodge#secret"},"agents":[]}`,
		`{"webhook":{"url":"https://hooks.example.test/lodge","secretFile":"relative-secret"},"agents":[]}`,
		`{"webhook":{"url":"https://hooks.example.test/lodge","secretFile":"/etc/lodge-hub/../secret"},"agents":[]}`,
		`{"webhook":{"url":"https://hooks.example.test/lodge","cooldownSeconds":29},"agents":[]}`,
		`{"webhook":{"url":"https://hooks.example.test/lodge","cooldownSeconds":86401},"agents":[]}`,
	}
	for _, body := range cases {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil {
			t.Fatalf("unsafe webhook was accepted: %s", body)
		}
	}
}

func TestLoadWebhookSecretRequiresBoundedOwnerOnlyRegularFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "webhook-secret")
	if err := os.WriteFile(path, []byte("opaque-webhook-secret\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secret, err := LoadWebhookSecret(path)
	if err != nil || secret != "opaque-webhook-secret" {
		t.Fatalf("secret = %q, err = %v", secret, err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWebhookSecret(path); err == nil {
		t.Fatal("group-readable webhook secret was accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "webhook-secret-link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWebhookSecret(link); err == nil {
		t.Fatal("symlinked webhook secret was accepted")
	}
	if err := os.WriteFile(path, []byte("contains space"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWebhookSecret(path); err == nil {
		t.Fatal("whitespace-containing webhook secret was accepted")
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maximumWebhookSecretBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWebhookSecret(path); err == nil {
		t.Fatal("oversized webhook secret was accepted")
	}
}
