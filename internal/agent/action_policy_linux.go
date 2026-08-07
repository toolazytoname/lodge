//go:build linux

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"

	"github.com/toolazytoname/lodge/internal/shared"
)

const (
	actionPolicyPath     = "/etc/lodge-agent/actions.json"
	actionLockPath       = "/run/lodge-agent-action.lock"
	maximumActionRequest = 4 << 10
	actionCommandTimeout = 15 * time.Second
	actionHealthTimeout  = 10 * time.Second
)

type actionRunner func(context.Context, string, ...string) ([]byte, []byte, error)

func WriteActionDefinitions(writer io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("action policy reader must run as root through the exact sudoers rule")
	}
	policy, err := loadActionPolicyFile(actionPolicyPath, 0)
	if err != nil {
		return err
	}
	return json.NewEncoder(writer).Encode(shared.ActionsResponse{Actions: actionDefinitions(policy)})
}

func ExecutePolicyAction(reader io.Reader, writer io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("action executor must run as root through the exact sudoers rule")
	}
	release, err := acquireActionLock(actionLockPath, 0)
	if err != nil {
		return err
	}
	defer release()
	request, err := decodeActionExecutionRequest(reader)
	if err != nil {
		return err
	}
	policy, err := loadActionPolicyFile(actionPolicyPath, 0)
	if err != nil {
		return err
	}
	target, definition, approved := approvedAction(policy, request.ID)
	if !approved {
		return errors.New("action is not approved by root policy")
	}
	result := executeApprovedAction(target, definition, runActionCommand)
	return json.NewEncoder(writer).Encode(result)
}

func loadActionPolicyFile(path string, expectedUID uint32) (actionPolicy, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return actionPolicy{Version: actionPolicyVersion, Targets: []actionPolicyTarget{}}, nil
	}
	if err != nil {
		return actionPolicy{}, errors.New("action policy cannot be opened safely")
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return actionPolicy{}, errors.New("action policy file handle is invalid")
	}
	defer file.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return actionPolicy{}, errors.New("action policy metadata is unavailable")
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != expectedUID || stat.Mode&0o777 != 0o600 {
		return actionPolicy{}, errors.New("action policy must be a root-owned regular file with mode 0600")
	}
	content, err := io.ReadAll(io.LimitReader(file, maximumActionPolicy+1))
	if err != nil {
		return actionPolicy{}, errors.New("action policy read failed")
	}
	return decodeActionPolicy(content)
}

func acquireActionLock(path string, expectedUID uint32) (func(), error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, errors.New("action lock cannot be opened safely")
	}
	closeFD := true
	defer func() {
		if closeFD {
			_ = unix.Close(fd)
		}
	}()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != expectedUID || stat.Mode&0o777 != 0o600 {
		return nil, errors.New("action lock has unsafe metadata")
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, errors.New("another action is already running")
		}
		return nil, errors.New("action lock failed")
	}
	closeFD = false
	return func() {
		_ = unix.Flock(fd, unix.LOCK_UN)
		_ = unix.Close(fd)
	}, nil
}

func decodeActionExecutionRequest(reader io.Reader) (shared.ActionExecutionRequest, error) {
	content, err := io.ReadAll(io.LimitReader(reader, maximumActionRequest+1))
	if err != nil || len(content) > maximumActionRequest {
		return shared.ActionExecutionRequest{}, errors.New("action request exceeds limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var request shared.ActionExecutionRequest
	if err := decoder.Decode(&request); err != nil {
		return shared.ActionExecutionRequest{}, errors.New("action request JSON is invalid")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return shared.ActionExecutionRequest{}, errors.New("action request contains trailing JSON")
	}
	if strings.TrimSpace(request.ID) == "" || len(request.ID) > 320 || !utf8.ValidString(request.ID) {
		return shared.ActionExecutionRequest{}, errors.New("action request ID is invalid")
	}
	return request, nil
}

func executeApprovedAction(target actionPolicyTarget, definition shared.ActionDefinition, runner actionRunner) shared.ActionExecutionResult {
	result := shared.ActionExecutionResult{
		ActionID: definition.ID, TargetKey: target.Key, Kind: definition.Kind,
	}
	ctx, cancel := context.WithTimeout(context.Background(), actionCommandTimeout)
	defer cancel()
	if definition.Kind == shared.ActionLogs {
		lines, err := readApprovedLogs(ctx, target, runner)
		if err != nil {
			result.ErrorKind = "log_read_failed"
			return result
		}
		result.OK = true
		result.Logs = lines
		result.Summary = fmt.Sprintf("返回 %d 行有界脱敏日志", len(lines))
		return result
	}
	before, err := approvedTargetState(ctx, target, runner)
	if err != nil {
		result.ErrorKind = "state_read_failed"
		return result
	}
	result.StateBefore = before
	if err := changeApprovedTarget(ctx, target, definition.Kind, runner); err != nil {
		result.ErrorKind = "command_failed"
		return result
	}
	expected := "running"
	if definition.Kind == shared.ActionStop {
		expected = "stopped"
	}
	deadline := time.Now().Add(actionHealthTimeout)
	for {
		after, stateErr := approvedTargetState(ctx, target, runner)
		if stateErr == nil {
			result.StateAfter = after
			if after == expected {
				result.OK = true
				result.Summary = fmt.Sprintf("%s：%s → %s", target.Label, before, after)
				return result
			}
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			result.ErrorKind = "health_verification_failed"
			return result
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func approvedTargetState(ctx context.Context, target actionPolicyTarget, runner actionRunner) (string, error) {
	switch target.Kind {
	case shared.ActionTargetSystemd:
		stdout, _, err := runner(ctx, "systemctl", "is-active", "--", target.Resource)
		state := strings.TrimSpace(string(stdout))
		if state == "active" {
			return "running", nil
		}
		if state == "inactive" || state == "failed" || state == "deactivating" {
			return "stopped", nil
		}
		if err != nil {
			return "", err
		}
		return "", errors.New("systemd state is unsupported")
	case shared.ActionTargetDocker:
		stdout, _, err := runner(ctx, "docker", "inspect", "--format={{.State.Running}}", "--", target.Resource)
		if err != nil {
			return "", err
		}
		switch strings.TrimSpace(string(stdout)) {
		case "true":
			return "running", nil
		case "false":
			return "stopped", nil
		default:
			return "", errors.New("Docker state is unsupported")
		}
	default:
		return "", errors.New("target kind is unsupported")
	}
}

func changeApprovedTarget(ctx context.Context, target actionPolicyTarget, action shared.ActionKind, runner actionRunner) error {
	if action != shared.ActionStart && action != shared.ActionStop && action != shared.ActionRestart {
		return errors.New("state action is unsupported")
	}
	switch target.Kind {
	case shared.ActionTargetSystemd:
		_, _, err := runner(ctx, "systemctl", string(action), "--", target.Resource)
		return err
	case shared.ActionTargetDocker:
		_, _, err := runner(ctx, "docker", string(action), target.Resource)
		return err
	default:
		return errors.New("target kind is unsupported")
	}
}

func readApprovedLogs(ctx context.Context, target actionPolicyTarget, runner actionRunner) ([]string, error) {
	var stdout, stderr []byte
	var err error
	switch target.Kind {
	case shared.ActionTargetSystemd:
		stdout, stderr, err = runner(ctx, "journalctl", "--quiet", "--no-pager", "--output=short-iso-precise", "--lines=200", "--unit", target.Resource)
	case shared.ActionTargetDocker:
		stdout, stderr, err = runner(ctx, "docker", "logs", "--tail=200", "--timestamps", target.Resource)
	default:
		return nil, errors.New("target kind is unsupported")
	}
	if err != nil {
		return nil, err
	}
	combined := append(append([]byte(nil), stdout...), '\n')
	combined = append(combined, stderr...)
	return sanitizeActionLogLines(combined), nil
}

func runActionCommand(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, nil, errors.New("approved action runtime is unavailable")
	}
	command := exec.CommandContext(ctx, path, args...)
	stdout := boundedBuffer{limit: maximumActionLogBytes}
	stderr := boundedBuffer{limit: maximumActionLogBytes}
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	if ctx.Err() != nil {
		return stdout.Bytes(), stderr.Bytes(), errors.New("approved action timed out")
	}
	if stdout.exceeded || stderr.exceeded {
		return stdout.Bytes(), stderr.Bytes(), errors.New("approved action output exceeded limit")
	}
	if runErr != nil {
		return stdout.Bytes(), stderr.Bytes(), errors.New("approved action command failed")
	}
	return stdout.Bytes(), stderr.Bytes(), nil
}
