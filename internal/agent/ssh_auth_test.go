package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/toolazytoname/lodge/internal/shared"
)

func TestSummarizeSSHJournalEmitsOnlyRankedCanonicalSources(t *testing.T) {
	start := time.Date(2026, 8, 8, 8, 0, 0, 0, time.UTC)
	end := start.Add(sshAuthWindow)
	messages := []any{
		"Failed password for invalid user alice from 203.0.113.9 port 41000 ssh2",
		"Failed password for root from 203.0.113.9 port 41001 ssh2",
		"Failed publickey for deploy from 2001:db8::5 port 41002 ssh2",
		"error: maximum authentication attempts exceeded for invalid user bob from ::ffff:198.51.100.8 port 41003 ssh2 [preauth]",
		"Invalid user mallory from 192.0.2.10 port 41004",
		"Accepted publickey for deploy from 192.0.2.11 port 41005 ssh2",
		[]string{"Failed password for root from 192.0.2.12 port 41006 ssh2"},
	}
	var lines strings.Builder
	for _, message := range messages {
		encoded, err := json.Marshal(map[string]any{"MESSAGE": message, "_SYSTEMD_UNIT": "ssh.service"})
		if err != nil {
			t.Fatal(err)
		}
		lines.Write(encoded)
		lines.WriteByte('\n')
	}
	summary, err := summarizeSSHJournal([]byte(lines.String()), start, end)
	if err != nil {
		t.Fatal(err)
	}
	if summary.FailedTotal != 4 || len(summary.Sources) != 3 {
		t.Fatalf("summary counts mismatch: %+v", summary)
	}
	want := []shared.SSHAuthSource{
		{Address: "203.0.113.9", Count: 2},
		{Address: "198.51.100.8", Count: 1},
		{Address: "2001:db8::5", Count: 1},
	}
	for index := range want {
		if summary.Sources[index] != want[index] {
			t.Fatalf("source %d = %+v, want %+v", index, summary.Sources[index], want[index])
		}
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "alice") || strings.Contains(string(encoded), "root") || strings.Contains(string(encoded), "41000") {
		t.Fatalf("summary leaked username, port, or raw message: %s", encoded)
	}
	if err := validateSSHAuthSummary(summary); err != nil {
		t.Fatalf("generated summary is invalid: %v", err)
	}
}

func TestSummarizeSSHJournalBoundsSourcesAndRejectsInvalidJSON(t *testing.T) {
	var lines strings.Builder
	for index := 1; index <= maximumSSHAuthSources+5; index++ {
		fmt.Fprintf(&lines, "{\"MESSAGE\":\"Failed password for root from 192.0.2.%d port 22 ssh2\"}\n", index)
	}
	now := time.Now().UTC().Truncate(time.Second)
	summary, err := summarizeSSHJournal([]byte(lines.String()), now.Add(-sshAuthWindow), now)
	if err != nil {
		t.Fatal(err)
	}
	if summary.FailedTotal != maximumSSHAuthSources+5 || len(summary.Sources) != maximumSSHAuthSources {
		t.Fatalf("source bound mismatch: %+v", summary)
	}
	if _, err := summarizeSSHJournal([]byte("not-json\n"), now.Add(-sshAuthWindow), now); err == nil {
		t.Fatal("invalid journal JSON was accepted")
	}
}

func TestValidateSSHAuthSummaryRejectsAmbiguousOrInvalidSources(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	base := shared.SSHAuthSummary{
		WindowStart: now.Add(-sshAuthWindow).Format(time.RFC3339), WindowEnd: now.Format(time.RFC3339),
		FailedTotal: 2, Sources: []shared.SSHAuthSource{{Address: "203.0.113.9", Count: 2}},
	}
	if err := validateSSHAuthSummary(base); err != nil {
		t.Fatal(err)
	}
	tests := []shared.SSHAuthSummary{
		{WindowStart: "invalid", WindowEnd: base.WindowEnd},
		{WindowStart: now.Add(-time.Minute).Format(time.RFC3339), WindowEnd: base.WindowEnd},
		{WindowStart: now.Add(-time.Hour).Format(time.RFC3339), WindowEnd: base.WindowEnd},
		{WindowStart: base.WindowStart, WindowEnd: base.WindowEnd, FailedTotal: 0, Sources: nil},
		{WindowStart: base.WindowStart, WindowEnd: base.WindowEnd, FailedTotal: 0, Sources: base.Sources},
		{WindowStart: base.WindowStart, WindowEnd: base.WindowEnd, FailedTotal: 2, Sources: nil},
		{WindowStart: base.WindowStart, WindowEnd: base.WindowEnd, FailedTotal: 1, Sources: base.Sources},
		{WindowStart: base.WindowStart, WindowEnd: base.WindowEnd, FailedTotal: 2, Sources: []shared.SSHAuthSource{{Address: "203.000.113.009", Count: 2}}},
		{WindowStart: base.WindowStart, WindowEnd: base.WindowEnd, FailedTotal: 2, Sources: []shared.SSHAuthSource{{Address: "203.0.113.9", Count: 1}, {Address: "203.0.113.9", Count: 1}}},
	}
	for index, summary := range tests {
		if err := validateSSHAuthSummary(summary); err == nil {
			t.Fatalf("invalid SSH summary %d was accepted: %+v", index, summary)
		}
	}
}
