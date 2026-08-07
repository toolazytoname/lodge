//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecuteApprovedDeploymentCommitsVerifiedImmutableRelease(t *testing.T) {
	stack := deploymentPolicyFixture().Stacks[0]
	stored := deploymentState{
		Version: deploymentStateVersion, StackKey: stack.Key,
		Current:   deploymentStateRelease{ID: "v1", Label: "Version 1", Image: stack.Releases[0].Image},
		UpdatedAt: "2026-08-08T00:00:00Z",
	}
	definition := deploymentDefinitions(deploymentPolicyFixture(), map[string]deploymentState{stack.Key: stored})[0]
	directory := filepath.Join(t.TempDir(), "states")
	runner := &deploymentRunnerFixture{t: t, stack: stack}

	result := executeApprovedDeployment(stack, definition, stored, directory, uint32(os.Getuid()), runner.run, func(context.Context, string) (int, error) {
		return 204, nil
	})
	if !result.OK || result.ErrorKind != "" || result.RollbackPerformed {
		t.Fatalf("deployment result=%+v", result)
	}
	content, err := os.ReadFile(deploymentOverridePath(directory, stack.Key))
	if err != nil {
		t.Fatal(err)
	}
	state, err := decodeDeploymentOverride(content, stack)
	if err != nil || state.Current.ID != "v2" || state.Previous.ID != "v1" {
		t.Fatalf("committed state=%+v err=%v", state, err)
	}
	foundApply := false
	for _, command := range runner.commands {
		if strings.HasPrefix(command, "docker compose --project-directory /srv/gateway --file /srv/gateway/compose.yaml --file ") && strings.HasSuffix(command, " up --detach --no-deps gateway") {
			foundApply = true
		}
	}
	if !foundApply {
		t.Fatalf("fixed Compose invocation missing: %v", runner.commands)
	}
	for _, command := range runner.commands {
		if strings.Contains(command, "sh -c") || strings.ContainsAny(command, ";|`$") {
			t.Fatalf("shell-capable deployment invocation: %q", command)
		}
	}
}

func TestExecuteApprovedDeploymentAutomaticallyRollsBackFailedApply(t *testing.T) {
	stack := deploymentPolicyFixture().Stacks[0]
	stored := deploymentState{
		Version: deploymentStateVersion, StackKey: stack.Key,
		Current:   deploymentStateRelease{ID: "v1", Label: "Version 1", Image: stack.Releases[0].Image},
		UpdatedAt: "2026-08-08T00:00:00Z",
	}
	definition := deploymentDefinitions(deploymentPolicyFixture(), map[string]deploymentState{stack.Key: stored})[0]
	directory := filepath.Join(t.TempDir(), "states")
	runner := &deploymentRunnerFixture{t: t, stack: stack, failTargetApply: true}

	result := executeApprovedDeployment(stack, definition, stored, directory, uint32(os.Getuid()), runner.run, func(context.Context, string) (int, error) {
		return 200, nil
	})
	if result.OK || result.ErrorKind != "compose_apply_failed" || !result.RollbackPerformed {
		t.Fatalf("rollback result=%+v", result)
	}
	if runner.applyCount != 2 {
		t.Fatalf("apply count=%d, want candidate and rollback", runner.applyCount)
	}
	content, err := os.ReadFile(deploymentOverridePath(directory, stack.Key))
	if err != nil {
		t.Fatal(err)
	}
	state, err := decodeDeploymentOverride(content, stack)
	if err != nil || state.Current.ID != "v1" || state.Previous != (deploymentStateRelease{}) {
		t.Fatalf("rollback state=%+v err=%v", state, err)
	}
}

func TestExecuteApprovedDeploymentCapturesInitialContainerRepoDigest(t *testing.T) {
	stack := deploymentPolicyFixture().Stacks[0]
	definitions := deploymentDefinitions(deploymentPolicyFixture(), map[string]deploymentState{})
	definition := definitions[1]
	directory := filepath.Join(t.TempDir(), "states")
	runner := &deploymentRunnerFixture{t: t, stack: stack}

	result := executeApprovedDeployment(stack, definition, deploymentState{}, directory, uint32(os.Getuid()), runner.run, func(context.Context, string) (int, error) {
		return 200, nil
	})
	if !result.OK {
		t.Fatalf("initial deployment result=%+v", result)
	}
	content, err := os.ReadFile(deploymentOverridePath(directory, stack.Key))
	if err != nil {
		t.Fatal(err)
	}
	state, err := decodeDeploymentOverride(content, stack)
	if err != nil || state.Current.ID != "v2" || state.Previous.ID != "external" || state.Previous.Image != immutableImage("0") {
		t.Fatalf("initial rollback point=%+v err=%v", state, err)
	}
}

func TestDeploymentOverrideRejectsMetadataBodyMismatch(t *testing.T) {
	stack := deploymentPolicyFixture().Stacks[0]
	state := deploymentState{
		Version: deploymentStateVersion, StackKey: stack.Key,
		Current:   deploymentStateRelease{ID: "v1", Label: "Version 1", Image: stack.Releases[0].Image},
		UpdatedAt: "2026-08-08T00:00:00Z",
	}
	content, err := encodeDeploymentOverride(state, stack)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(content), stack.Releases[0].Image, stack.Releases[1].Image, 1)
	if _, err := decodeDeploymentOverride([]byte(tampered), stack); err == nil {
		t.Fatal("override body differing from authenticated state metadata was accepted")
	}
}

func TestDeploymentPolicyFileAndStateDirectoryFailClosedOnUnsafeMetadata(t *testing.T) {
	directory := t.TempDir()
	policyPath := filepath.Join(directory, "deployments.json")
	content, err := json.Marshal(deploymentPolicyFixture())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policyPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDeploymentPolicyFile(policyPath, uint32(os.Getuid())); err != nil {
		t.Fatalf("safe policy rejected: %v", err)
	}
	if err := os.Chmod(policyPath, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDeploymentPolicyFile(policyPath, uint32(os.Getuid())); err == nil {
		t.Fatal("group-readable deployment policy was accepted")
	}
	linkPath := filepath.Join(directory, "policy-link.json")
	if err := os.Symlink(policyPath, linkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDeploymentPolicyFile(linkPath, uint32(os.Getuid())); err == nil {
		t.Fatal("symlink deployment policy was accepted")
	}

	stateDirectory := filepath.Join(directory, "unsafe-state")
	if err := os.Mkdir(stateDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	state := deploymentState{
		Version: deploymentStateVersion, StackKey: "gateway",
		Current:   deploymentStateRelease{ID: "v1", Label: "Version 1", Image: immutableImage("1")},
		UpdatedAt: "2026-08-08T00:00:00Z",
	}
	if _, _, _, err := writeDeploymentOverrideCandidate(stateDirectory, deploymentPolicyFixture().Stacks[0], state, uint32(os.Getuid())); err == nil {
		t.Fatal("non-private deployment state directory was accepted")
	}
}

type deploymentRunnerFixture struct {
	t               *testing.T
	stack           deploymentPolicyStack
	commands        []string
	applyCount      int
	failTargetApply bool
}

func (fixture *deploymentRunnerFixture) run(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	fixture.t.Helper()
	fixture.commands = append(fixture.commands, strings.Join(append([]string{name}, args...), " "))
	if name != "docker" {
		return nil, nil, errors.New("unexpected executable")
	}
	joined := strings.Join(args, " ")
	switch {
	case strings.HasSuffix(joined, "config --services"):
		return []byte(fixture.stack.Service + "\n"), nil, nil
	case len(args) >= 3 && args[0] == "image" && args[1] == "inspect" && args[2] == "--format={{json .RepoDigests}}":
		return []byte(`["` + immutableImage("0") + `"]`), nil, nil
	case len(args) >= 2 && args[0] == "image" && args[1] == "inspect":
		return []byte("image\n"), nil, nil
	case strings.HasSuffix(joined, "ps --quiet "+fixture.stack.Service):
		return []byte("container-id\n"), nil, nil
	case len(args) >= 2 && args[0] == "inspect" && args[1] == "--format={{.State.Running}}":
		return []byte("true\n"), nil, nil
	case len(args) >= 2 && args[0] == "inspect" && args[1] == "--format={{.Image}}":
		return []byte("sha256:" + strings.Repeat("a", 64) + "\n"), nil, nil
	case strings.Contains(joined, " up --detach --no-deps "+fixture.stack.Service):
		fixture.applyCount++
		override := composeOverrideArgument(args)
		content, err := os.ReadFile(override)
		if err != nil {
			fixture.t.Fatalf("read override: %v", err)
		}
		if fixture.failTargetApply && strings.Contains(string(content), fixture.stack.Releases[1].Image) {
			return nil, nil, errors.New("simulated candidate failure")
		}
		return nil, nil, nil
	default:
		fixture.t.Fatalf("unexpected deployment command: docker %s", joined)
		return nil, nil, errors.New("unexpected command")
	}
}

func composeOverrideArgument(args []string) string {
	for index := len(args) - 2; index >= 0; index-- {
		if args[index] == "--file" {
			return args[index+1]
		}
	}
	return ""
}
