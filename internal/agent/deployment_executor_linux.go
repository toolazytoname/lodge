//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/toolazytoname/lodge/internal/shared"
)

const (
	deploymentExecutionTimeout = 10 * time.Minute
	deploymentRollbackTimeout  = 7 * time.Minute
	deploymentProbeInterval    = 500 * time.Millisecond
	deploymentCommandOutputMax = 64 << 10
)

type deploymentRunner func(context.Context, string, ...string) ([]byte, []byte, error)
type deploymentHTTPProber func(context.Context, string) (int, error)

func executeApprovedDeployment(
	stack deploymentPolicyStack,
	definition shared.DeploymentDefinition,
	stored deploymentState,
	stateDirectory string,
	stateOwnerUID uint32,
	runner deploymentRunner,
	probe deploymentHTTPProber,
) shared.DeploymentExecutionResult {
	result := shared.DeploymentExecutionResult{
		ActionID: definition.ID, StackKey: stack.Key, Kind: definition.Kind,
		ReleaseID: definition.ReleaseID, PreviousReleaseID: definition.CurrentReleaseID,
	}
	ctx, cancel := context.WithTimeout(context.Background(), deploymentExecutionTimeout)
	defer cancel()

	if err := validateComposeService(ctx, stack, "", runner); err != nil {
		return failedDeploymentResult(definition, "preflight_failed", false, definition.CurrentReleaseID)
	}
	if err := ensureImmutableImage(ctx, definition.Image, runner); err != nil {
		return failedDeploymentResult(definition, "image_prepare_failed", false, definition.CurrentReleaseID)
	}

	current := stored.Current
	if current == (deploymentStateRelease{}) {
		discovered, err := discoverCurrentRelease(ctx, stack, runner)
		if err != nil {
			return failedDeploymentResult(definition, "current_release_unknown", false, definition.CurrentReleaseID)
		}
		current = discovered
	}
	result.PreviousReleaseID = current.ID
	target := deploymentStateRelease{ID: definition.ReleaseID, Label: definition.ReleaseLabel, Image: definition.Image}
	if immutableImageDigest(current.Image) == immutableImageDigest(target.Image) {
		if err := verifyDeploymentHealth(ctx, stack, "", runner, probe); err != nil {
			return failedDeploymentResult(definition, "health_verification_failed", false, current.ID)
		}
		state := deploymentState{
			Version: deploymentStateVersion, StackKey: stack.Key, Current: target,
			UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
		_, commit, cleanup, err := writeDeploymentOverrideCandidate(stateDirectory, stack, state, stateOwnerUID)
		if err != nil {
			return failedDeploymentResult(definition, "state_commit_failed", false, current.ID)
		}
		defer cleanup()
		if err := commit(); err != nil {
			return failedDeploymentResult(definition, "state_commit_failed", false, current.ID)
		}
		result.OK = true
		result.Summary = fmt.Sprintf("%s 已运行目标版本 %s，健康验证通过", stack.Label, definition.ReleaseLabel)
		return result
	}

	next := deploymentState{
		Version: deploymentStateVersion, StackKey: stack.Key, Current: target, Previous: current,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	candidatePath, commit, cleanup, err := writeDeploymentOverrideCandidate(stateDirectory, stack, next, stateOwnerUID)
	if err != nil {
		return failedDeploymentResult(definition, "state_prepare_failed", false, current.ID)
	}
	defer cleanup()
	if err := validateComposeService(ctx, stack, candidatePath, runner); err != nil {
		return failedDeploymentResult(definition, "preflight_failed", false, current.ID)
	}
	if err := applyDeploymentOverride(ctx, stack, candidatePath, runner); err != nil {
		return rollbackFailedDeployment(stack, definition, stored, current, stateDirectory, stateOwnerUID, runner, probe, "compose_apply_failed")
	}
	if err := verifyDeploymentHealth(ctx, stack, candidatePath, runner, probe); err != nil {
		return rollbackFailedDeployment(stack, definition, stored, current, stateDirectory, stateOwnerUID, runner, probe, "health_verification_failed")
	}
	if err := commit(); err != nil {
		return rollbackFailedDeployment(stack, definition, stored, current, stateDirectory, stateOwnerUID, runner, probe, "state_commit_failed")
	}
	result.OK = true
	if definition.Kind == shared.DeploymentRollback {
		result.Summary = fmt.Sprintf("%s 已回滚到 %s，健康验证通过", stack.Label, definition.ReleaseLabel)
	} else {
		result.Summary = fmt.Sprintf("%s 已部署 %s，健康验证通过", stack.Label, definition.ReleaseLabel)
	}
	return result
}

func failedDeploymentResult(definition shared.DeploymentDefinition, errorKind string, rollbackPerformed bool, previousReleaseID string) shared.DeploymentExecutionResult {
	result := shared.DeploymentExecutionResult{
		ActionID: definition.ID, StackKey: definition.StackKey, Kind: definition.Kind,
		ReleaseID: definition.ReleaseID, PreviousReleaseID: previousReleaseID,
		RollbackPerformed: rollbackPerformed, ErrorKind: errorKind,
	}
	if rollbackPerformed {
		result.Summary = "部署未通过验证，已自动恢复到操作前版本"
	}
	return result
}

func rollbackFailedDeployment(
	stack deploymentPolicyStack,
	definition shared.DeploymentDefinition,
	stored deploymentState,
	current deploymentStateRelease,
	stateDirectory string,
	stateOwnerUID uint32,
	runner deploymentRunner,
	probe deploymentHTTPProber,
	originalError string,
) shared.DeploymentExecutionResult {
	// Candidate timeout must not consume the recovery budget. Once Compose may
	// have changed the service, rollback gets one independent bounded context.
	ctx, cancel := context.WithTimeout(context.Background(), deploymentRollbackTimeout)
	defer cancel()
	rollbackState := stored
	if rollbackState.Current == (deploymentStateRelease{}) {
		rollbackState = deploymentState{
			Version: deploymentStateVersion, StackKey: stack.Key, Current: current,
			UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
	}
	rollbackPath, commit, cleanup, err := writeDeploymentOverrideCandidate(stateDirectory, stack, rollbackState, stateOwnerUID)
	if err != nil {
		return failedDeploymentResult(definition, "rollback_failed", false, current.ID)
	}
	defer cleanup()
	if err := validateComposeService(ctx, stack, rollbackPath, runner); err != nil {
		return failedDeploymentResult(definition, "rollback_failed", false, current.ID)
	}
	if err := applyDeploymentOverride(ctx, stack, rollbackPath, runner); err != nil {
		return failedDeploymentResult(definition, "rollback_failed", false, current.ID)
	}
	if err := verifyDeploymentHealth(ctx, stack, rollbackPath, runner, probe); err != nil {
		return failedDeploymentResult(definition, "rollback_failed", false, current.ID)
	}
	if err := commit(); err != nil {
		return failedDeploymentResult(definition, "rollback_failed", false, current.ID)
	}
	return failedDeploymentResult(definition, originalError, true, current.ID)
}

func validateComposeService(ctx context.Context, stack deploymentPolicyStack, overridePath string, runner deploymentRunner) error {
	stdout, _, err := runner(ctx, "docker", composeArguments(stack, overridePath, "config", "--services")...)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(stdout)), "\n") {
		if strings.TrimSpace(line) == stack.Service {
			return nil
		}
	}
	return errors.New("approved Compose service is missing")
}

func ensureImmutableImage(ctx context.Context, image string, runner deploymentRunner) error {
	if !immutableImagePattern.MatchString(image) {
		return errors.New("image reference is not immutable")
	}
	if _, _, err := runner(ctx, "docker", "image", "inspect", "--", image); err == nil {
		return nil
	}
	if _, _, err := runner(ctx, "docker", "pull", "--quiet", image); err != nil {
		return err
	}
	_, _, err := runner(ctx, "docker", "image", "inspect", "--", image)
	return err
}

func discoverCurrentRelease(ctx context.Context, stack deploymentPolicyStack, runner deploymentRunner) (deploymentStateRelease, error) {
	containerID, err := composeContainerID(ctx, stack, "", runner)
	if err != nil {
		return deploymentStateRelease{}, err
	}
	stdout, _, err := runner(ctx, "docker", "inspect", "--format={{.Image}}", "--", containerID)
	if err != nil {
		return deploymentStateRelease{}, err
	}
	imageID := strings.TrimSpace(string(stdout))
	if !dockerImageIDPattern.MatchString(imageID) {
		return deploymentStateRelease{}, errors.New("current container image identity is invalid")
	}
	stdout, _, err = runner(ctx, "docker", "image", "inspect", "--format={{json .RepoDigests}}", "--", imageID)
	if err != nil {
		return deploymentStateRelease{}, err
	}
	var digests []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(stdout))), &digests); err != nil {
		return deploymentStateRelease{}, errors.New("current image digests are invalid")
	}
	sort.Strings(digests)
	for _, digest := range digests {
		if len(digest) <= 512 && immutableImagePattern.MatchString(digest) {
			return deploymentStateRelease{ID: "external", Label: "部署前版本", Image: digest}, nil
		}
	}
	return deploymentStateRelease{}, errors.New("current image has no immutable repository digest")
}

func applyDeploymentOverride(ctx context.Context, stack deploymentPolicyStack, overridePath string, runner deploymentRunner) error {
	_, _, err := runner(ctx, "docker", composeArguments(stack, overridePath, "up", "--detach", "--no-deps", stack.Service)...)
	return err
}

func verifyDeploymentHealth(ctx context.Context, stack deploymentPolicyStack, overridePath string, runner deploymentRunner, probe deploymentHTTPProber) error {
	deadline := time.Now().Add(time.Duration(stack.Health.TimeoutSeconds) * time.Second)
	for {
		containerID, err := composeContainerID(ctx, stack, overridePath, runner)
		if err == nil {
			running, _, inspectErr := runner(ctx, "docker", "inspect", "--format={{.State.Running}}", "--", containerID)
			if inspectErr == nil && strings.TrimSpace(string(running)) == "true" {
				switch stack.Health.Kind {
				case "docker":
					status, _, healthErr := runner(ctx, "docker", "inspect", "--format={{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}", "--", containerID)
					if healthErr == nil && strings.TrimSpace(string(status)) == "healthy" {
						return nil
					}
				case "http":
					status, probeErr := probe(ctx, stack.Health.URL)
					if probeErr == nil && status >= http.StatusOK && status < http.StatusMultipleChoices {
						return nil
					}
				}
			}
		}
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			return errors.New("deployment health verification timed out")
		}
		time.Sleep(deploymentProbeInterval)
	}
}

func composeContainerID(ctx context.Context, stack deploymentPolicyStack, overridePath string, runner deploymentRunner) (string, error) {
	stdout, _, err := runner(ctx, "docker", composeArguments(stack, overridePath, "ps", "--quiet", stack.Service)...)
	if err != nil {
		return "", err
	}
	containerID := strings.TrimSpace(string(stdout))
	if containerID == "" || strings.ContainsAny(containerID, " \t\r\n") {
		return "", errors.New("Compose service container is unavailable")
	}
	return containerID, nil
}

func composeArguments(stack deploymentPolicyStack, overridePath string, tail ...string) []string {
	arguments := []string{"compose", "--project-directory", stack.ProjectDirectory, "--file", stack.ComposeFile}
	if overridePath != "" {
		arguments = append(arguments, "--file", overridePath)
	}
	return append(arguments, tail...)
}

func runDeploymentCommand(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, nil, errors.New("deployment runtime is unavailable")
	}
	command := exec.CommandContext(ctx, path, args...)
	stdout := boundedBuffer{limit: deploymentCommandOutputMax}
	stderr := boundedBuffer{limit: deploymentCommandOutputMax}
	command.Stdout, command.Stderr = &stdout, &stderr
	runErr := command.Run()
	if ctx.Err() != nil {
		return stdout.Bytes(), stderr.Bytes(), errors.New("deployment command timed out")
	}
	if stdout.exceeded || stderr.exceeded {
		return stdout.Bytes(), stderr.Bytes(), errors.New("deployment command output exceeded limit")
	}
	if runErr != nil {
		return stdout.Bytes(), stderr.Bytes(), errors.New("deployment command failed")
	}
	return stdout.Bytes(), stderr.Bytes(), nil
}

func probeDeploymentHTTP(ctx context.Context, address string) (int, error) {
	transport := &http.Transport{
		Proxy:             nil,
		DialContext:       (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
		DisableKeepAlives: true,
	}
	client := &http.Client{
		Transport:     transport,
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return 0, err
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	return response.StatusCode, nil
}
