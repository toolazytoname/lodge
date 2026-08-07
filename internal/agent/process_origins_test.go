package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseProcessOriginsAcceptsOnlyStrongRedactedRecords(t *testing.T) {
	content := strings.Join([]string{
		`{"pid":481732,"uid":1001,"comm":"node","executable":"node","cwdBase":"image","cwdFingerprint":"0123456789abcdef"}`,
		`not json`,
		`{"pid":1,"uid":0,"comm":"init","cwdBase":"root","cwdFingerprint":"0123456789abcdef"}`,
		`{"pid":2,"uid":0,"comm":"worker","cwdBase":"app","cwdFingerprint":"not-a-fingerprint"}`,
		`{"pid":3,"uid":0,"comm":"worker","cwdFingerprint":"0123456789abcdef"}`,
	}, "\n")

	origins := parseProcessOrigins([]byte(content))
	if len(origins) != 1 {
		t.Fatalf("expected one strong record, got %+v", origins)
	}
	if origin := origins[481732]; origin.CWDBase != "image" || origin.Executable != "node" {
		t.Fatalf("valid origin was not preserved: %+v", origin)
	}
}

func TestProcessOriginStableKeyAndName(t *testing.T) {
	base := processOrigin{
		PID: 481732, UID: 1001, Comm: "node", Executable: "node",
		CWDBase: "image", CWDFingerprint: "0123456789abcdef",
	}
	restarted := base
	restarted.PID = 999999
	if base.workloadKey() != restarted.workloadKey() {
		t.Fatal("process restart must not change workload key")
	}
	otherUser := base
	otherUser.UID++
	if base.workloadKey() == otherUser.workloadKey() {
		t.Fatal("different uid must not share workload key")
	}
	otherPath := base
	otherPath.CWDFingerprint = "fedcba9876543210"
	if base.workloadKey() == otherPath.workloadKey() {
		t.Fatal("different working directory fingerprint must not share workload key")
	}
	if got := base.workloadName("fallback"); got != "image · node" {
		t.Fatalf("unexpected workload name: %q", got)
	}
}

func TestProcessOriginJSONContainsNoSensitiveProcessFields(t *testing.T) {
	origin := processOrigin{
		PID: 7, UID: 1001, Comm: "node", Executable: "node",
		CWDBase: "app", CWDFingerprint: "0123456789abcdef",
	}
	encoded, err := json.Marshal(origin)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"cmdline", "commandLine", "environ", "environment", "cwdPath"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("redacted schema leaked forbidden field %q: %s", forbidden, encoded)
		}
	}
}

func TestSanitizeProcessLabel(t *testing.T) {
	long := strings.Repeat("x", 140)
	got := sanitizeProcessLabel("  app\x00name\n" + long + "  ")
	if strings.ContainsAny(got, "\x00\n") {
		t.Fatalf("control characters were not removed: %q", got)
	}
	if length := len([]rune(got)); length == 0 || length > 128 {
		t.Fatalf("label length must be between 1 and 128 runes: %d", length)
	}
}
