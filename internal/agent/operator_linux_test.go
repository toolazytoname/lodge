//go:build linux

package agent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOperatorPolicyFileEnforcesOwnershipModeAndNoSymlink(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "operator.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"owners":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	uid := uint32(os.Getuid())
	if _, err := loadOperatorPolicyFile(path, uid); err != nil {
		t.Fatalf("safe policy rejected: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOperatorPolicyFile(path, uid); err == nil {
		t.Fatal("group/world-readable policy was accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "operator-link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOperatorPolicyFile(link, uid); err == nil {
		t.Fatal("symlink policy was accepted")
	}
	missing, err := loadOperatorPolicyFile(filepath.Join(directory, "missing.json"), uid)
	if err != nil || len(missing.Owners) != 0 {
		t.Fatalf("missing policy should disable the class: %+v %v", missing, err)
	}
}

func TestOperatorFileOpsStayInsideOwnerHome(t *testing.T) {
	home := t.TempDir()
	backup := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "mihomo"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".config", "mihomo", "config.yaml")
	if err := os.WriteFile(target, []byte("listen: 127.0.0.1:7890\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("shadow"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".config", "mihomo", "escape.yaml")); err != nil {
		t.Fatal(err)
	}
	account := operatorAccount{Name: "ecs-user", UID: uint32(os.Getuid()), Home: home}

	content, sum, err := readOwnerFile(account, ".config/mihomo/config.yaml")
	if err != nil || content != "listen: 127.0.0.1:7890\n" || sum == "" {
		t.Fatalf("safe read failed: %q %q %v", content, sum, err)
	}
	if _, _, err := readOwnerFile(account, ".config/mihomo/escape.yaml"); err == nil {
		t.Fatal("symlink escape was readable")
	}

	entries, err := listOwnerDir(account, ".config/mihomo")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(entries, ",")
	if !strings.Contains(joined, "config.yaml") || !strings.Contains(joined, "escape.yaml") {
		t.Fatalf("directory listing missed entries: %v", entries)
	}

	next, err := writeOwnerFile(account, ".config/mihomo/config.yaml", "listen: 127.0.0.1:1\n", sum, backup)
	if err != nil || next == sum {
		t.Fatalf("safe write failed: %q %v", next, err)
	}
	written, err := os.ReadFile(target)
	if err != nil || string(written) != "listen: 127.0.0.1:1\n" {
		t.Fatalf("file was not replaced: %q %v", written, err)
	}
	backups, err := os.ReadDir(backup)
	if err != nil || len(backups) != 1 {
		t.Fatalf("root-only backup was not created: %v %v", backups, err)
	}
	if _, err := writeOwnerFile(account, ".config/mihomo/config.yaml", "nope\n", sum, backup); err == nil {
		t.Fatal("stale sha256 compare-and-swap was accepted")
	}
	if _, err := writeOwnerFile(account, ".config/mihomo/missing.yaml", "x\n", "", backup); err == nil {
		t.Fatal("missing file create was accepted")
	}
}

func TestInspectOwnerUnitRequiresMatchingUser(t *testing.T) {
	account := operatorAccount{Name: "ecs-user", UID: 1000, Home: "/home/ecs-user"}
	var calls [][]string
	runner := func(name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		return []byte("Id=mihomo.service\nUser=ecs-user\nLoadState=loaded\nActiveState=active\n"), nil
	}
	info, err := inspectOwnerUnit(account, "mihomo.service", runner)
	if err != nil || info.User != "ecs-user" || info.ActiveState != "active" {
		t.Fatalf("matching unit rejected: %+v %v", info, err)
	}
	if len(calls) != 1 || calls[0][0] != "systemctl" || calls[0][len(calls[0])-1] != "mihomo.service" {
		t.Fatalf("systemctl argv drifted: %v", calls)
	}

	runner = func(name string, args ...string) ([]byte, error) {
		return []byte("Id=mihomo.service\nUser=root\nLoadState=loaded\nActiveState=active\n"), nil
	}
	if _, err := inspectOwnerUnit(account, "mihomo.service", runner); err == nil {
		t.Fatal("root-owned unit was accepted")
	}
	if _, err := inspectOwnerUnit(account, "sshd.service", runner); err == nil {
		t.Fatal("denied system unit was inspected")
	}
}

func TestExecuteOperatorUsesPolicyAndFixedUnitArgv(t *testing.T) {
	home := t.TempDir()
	policyPath := filepath.Join(t.TempDir(), "operator.json")
	backup := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "app.env"), []byte("PORT=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policyPath, []byte(`{"version":1,"owners":["ecs-user"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	request, err := decodeOperatorRequest(strings.NewReader(`{"owner":"ecs-user","op":"read-file","path":".config/app.env"}`))
	if err != nil {
		t.Fatal(err)
	}
	account := operatorAccount{Name: "ecs-user", UID: uint32(os.Getuid()), Home: home}
	result, err := performOperatorRequest(account, request, backup, nil)
	if err != nil || !result.OK || result.Content != "PORT=1\n" {
		t.Fatalf("policy-backed read failed: %+v %v", result, err)
	}

	var calls [][]string
	runner := func(name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		return []byte("Id=app.service\nUser=ecs-user\nLoadState=loaded\nActiveState=active\n"), nil
	}
	request, err = decodeOperatorRequest(strings.NewReader(`{"owner":"ecs-user","op":"unit-restart","unit":"app.service"}`))
	if err != nil {
		t.Fatal(err)
	}
	result, err = performOperatorRequest(account, request, backup, runner)
	if err != nil || !result.OK || result.Active != "active" {
		t.Fatalf("unit restart failed: %+v %v", result, err)
	}
	if len(calls) != 2 || calls[1][0] != "systemctl" || calls[1][1] != "restart" || calls[1][2] != "--" || calls[1][3] != "app.service" {
		t.Fatalf("restart argv drifted: %v", calls)
	}

	policy, err := loadOperatorPolicyFile(policyPath, uint32(os.Getuid()))
	if err != nil || !ownerApproved(policy, "ecs-user") {
		t.Fatalf("test policy should approve ecs-user: %+v %v", policy, err)
	}
	if ownerApproved(policy, "root") {
		t.Fatal("test policy approved root")
	}

	var listed bytes.Buffer
	if err := writeOperatorOwners(policyPath, uint32(os.Getuid()), &listed); err != nil {
		t.Fatal(err)
	}
	var response operatorListResponse
	if err := json.Unmarshal(listed.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Owners) != 1 || response.Owners[0] != "ecs-user" || len(response.Operations) != 5 {
		t.Fatalf("list response drifted: %+v", response)
	}
}
