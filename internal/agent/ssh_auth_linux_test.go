//go:build linux

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSummarizeSSHAuthFileRejectsSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "auth.log.real")
	link := filepath.Join(directory, "auth.log")
	if err := os.WriteFile(target, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, found, err := summarizeSSHAuthFile(link, now.Add(-sshAuthWindow), now); !found || err == nil {
		t.Fatalf("authentication log symlink was accepted: found=%v err=%v", found, err)
	}
}

func TestSummarizeSSHAuthFileRejectsWritableByUntrustedUsers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.log")
	if err := os.WriteFile(path, []byte(""), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, found, err := summarizeSSHAuthFile(path, now.Add(-sshAuthWindow), now); !found || err == nil {
		t.Fatalf("writable authentication log was accepted: found=%v err=%v", found, err)
	}
}

func TestSummarizeSSHAuthFileReadsOnlyBoundedCompleteTail(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "auth.log")
	end := time.Now().UTC().Truncate(time.Second)
	start := end.Add(-sshAuthWindow)
	old := start.Add(-time.Minute).Format(time.RFC3339) + " host sshd[1]: session closed\n"
	failure := end.Add(-time.Minute).Format(time.RFC3339) + " host sshd[2]: Failed password for root from 203.0.113.9 port 22 ssh2\n"
	padding := strings.Repeat("x", int(maximumSSHAuthLogBytes)+1024)
	contents := padding + "\n" + old + failure
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	summary, found, err := summarizeSSHAuthFile(path, start, end)
	if err != nil || !found || summary.FailedTotal != 1 || len(summary.Sources) != 1 {
		t.Fatalf("bounded authentication log tail mismatch: found=%v summary=%+v err=%v", found, summary, err)
	}
}
