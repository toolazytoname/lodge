//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/toolazytoname/lodge/internal/shared"
)

func TestLoadActionPolicyFileEnforcesOwnershipModeAndNoSymlink(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "actions.json")
	content := []byte(`{"version":1,"targets":[]}`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	uid := uint32(os.Getuid())
	if _, err := loadActionPolicyFile(path, uid); err != nil {
		t.Fatalf("safe policy rejected: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadActionPolicyFile(path, uid); err == nil {
		t.Fatal("group/world-readable policy was accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "actions-link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := loadActionPolicyFile(link, uid); err == nil {
		t.Fatal("symlink policy was accepted")
	}
	missing, err := loadActionPolicyFile(filepath.Join(directory, "missing.json"), uid)
	if err != nil || len(missing.Targets) != 0 {
		t.Fatalf("missing policy should disable all actions: %+v %v", missing, err)
	}
}

func TestAcquireActionLockIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "action.lock")
	uid := uint32(os.Getuid())
	release, err := acquireActionLock(path, uid)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := acquireActionLock(path, uid); err == nil {
		t.Fatal("second concurrent lock was acquired")
	}
}

func TestDecodeActionExecutionRequestIsStrictAndBounded(t *testing.T) {
	request, err := decodeActionExecutionRequest(strings.NewReader(`{"id":"restart:systemd:caddy.service"}`))
	if err != nil || request.ID == "" {
		t.Fatalf("valid request rejected: %+v %v", request, err)
	}
	for _, content := range []string{
		`{"id":"x","command":"id"}`,
		`{"id":"x"} {}`,
		`{"id":""}`,
		`{"id":"` + strings.Repeat("x", maximumActionRequest) + `"}`,
	} {
		if _, err := decodeActionExecutionRequest(strings.NewReader(content)); err == nil {
			t.Fatalf("unsafe request accepted: %.80q", content)
		}
	}
}

func TestExecuteApprovedSystemdRestartUsesFixedArguments(t *testing.T) {
	target, definition := systemdRestartFixture()
	var calls [][]string
	runner := func(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
		calls = append(calls, append([]string{name}, args...))
		switch len(calls) {
		case 1, 3:
			return []byte("active\n"), nil, nil
		case 2:
			return nil, nil, nil
		default:
			return nil, nil, errors.New("unexpected call")
		}
	}
	result := executeApprovedAction(target, definition, runner)
	want := [][]string{
		{"systemctl", "is-active", "--", "caddy.service"},
		{"systemctl", "restart", "--", "caddy.service"},
		{"systemctl", "is-active", "--", "caddy.service"},
	}
	if !result.OK || result.StateBefore != "running" || result.StateAfter != "running" || !reflect.DeepEqual(calls, want) {
		t.Fatalf("unexpected systemd execution: result=%+v calls=%v", result, calls)
	}
}

func TestExecuteApprovedDockerStopUsesFixedArguments(t *testing.T) {
	target := actionPolicyTarget{Key: "docker:api", Label: "API", Kind: shared.ActionTargetDocker, Resource: "api", Actions: []shared.ActionKind{shared.ActionStop}}
	definition := actionDefinitions(actionPolicy{Version: 1, Targets: []actionPolicyTarget{target}})[0]
	var calls [][]string
	runner := func(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
		calls = append(calls, append([]string{name}, args...))
		if len(calls) == 1 {
			return []byte("true\n"), nil, nil
		}
		if len(calls) == 2 {
			return nil, nil, nil
		}
		return []byte("false\n"), nil, nil
	}
	result := executeApprovedAction(target, definition, runner)
	want := [][]string{
		{"docker", "inspect", "--format={{.State.Running}}", "--", "api"},
		{"docker", "stop", "api"},
		{"docker", "inspect", "--format={{.State.Running}}", "--", "api"},
	}
	if !result.OK || !reflect.DeepEqual(calls, want) {
		t.Fatalf("unexpected Docker execution: result=%+v calls=%v", result, calls)
	}
}

func TestExecuteApprovedLogsReturnsOnlySanitizedLines(t *testing.T) {
	target := actionPolicyTarget{Key: "docker:api", Label: "API", Kind: shared.ActionTargetDocker, Resource: "api", Actions: []shared.ActionKind{shared.ActionLogs}}
	definition := actionDefinitions(actionPolicy{Version: 1, Targets: []actionPolicyTarget{target}})[0]
	runner := func(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
		if name != "docker" || !reflect.DeepEqual(args, []string{"logs", "--tail=200", "--timestamps", "api"}) {
			t.Fatalf("unexpected log command: %s %v", name, args)
		}
		return []byte("token=very-secret-token\nsafe\n"), nil, nil
	}
	result := executeApprovedAction(target, definition, runner)
	encoded, _ := json.Marshal(result)
	if !result.OK || len(result.Logs) != 2 || strings.Contains(string(encoded), "very-secret-token") {
		t.Fatalf("logs were not sanitized: %s", encoded)
	}
}

func systemdRestartFixture() (actionPolicyTarget, shared.ActionDefinition) {
	target := actionPolicyTarget{
		Key: "systemd:caddy.service", Label: "Caddy", Kind: shared.ActionTargetSystemd,
		Resource: "caddy.service", Actions: []shared.ActionKind{shared.ActionRestart},
	}
	definition := actionDefinitions(actionPolicy{Version: 1, Targets: []actionPolicyTarget{target}})[0]
	return target, definition
}
