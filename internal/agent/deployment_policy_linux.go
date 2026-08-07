//go:build linux

package agent

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"

	"github.com/toolazytoname/lodge/internal/shared"
)

const (
	deploymentPolicyPath     = "/etc/lodge-agent/deployments.json"
	deploymentStateDir       = "/var/lib/lodge-agent/deployments"
	maximumDeploymentRequest = 4 << 10
	maximumDeploymentState   = 16 << 10
)

func WriteDeploymentDefinitions(writer io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("deployment policy reader must run as root through the exact sudoers rule")
	}
	policy, err := loadDeploymentPolicyFile(deploymentPolicyPath, 0)
	if err != nil {
		return err
	}
	for _, stack := range policy.Stacks {
		if err := validateDeploymentStackFiles(stack, 0); err != nil {
			return errors.New("deployment stack files cannot be opened safely")
		}
	}
	states, err := loadDeploymentStates(deploymentStateDir, policy, 0)
	if err != nil {
		return err
	}
	return json.NewEncoder(writer).Encode(shared.DeploymentsResponse{Deployments: deploymentDefinitions(policy, states)})
}

func ExecutePolicyDeployment(reader io.Reader, writer io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("deployment executor must run as root through the exact sudoers rule")
	}
	release, err := acquireActionLock(actionLockPath, 0)
	if err != nil {
		return err
	}
	defer release()
	request, err := decodeDeploymentExecutionRequest(reader)
	if err != nil {
		return err
	}
	policy, err := loadDeploymentPolicyFile(deploymentPolicyPath, 0)
	if err != nil {
		return err
	}
	states, err := loadDeploymentStates(deploymentStateDir, policy, 0)
	if err != nil {
		return err
	}
	definition, found := findDeploymentDefinition(deploymentDefinitions(policy, states), request.ID)
	if !found {
		return errors.New("deployment is not approved by root policy")
	}
	stack, found := findDeploymentStack(policy.Stacks, definition.StackKey)
	if !found {
		return errors.New("deployment stack is unavailable")
	}
	if err := validateDeploymentStackFiles(stack, 0); err != nil {
		result := failedDeploymentResult(definition, "preflight_failed", false, definition.CurrentReleaseID)
		return json.NewEncoder(writer).Encode(result)
	}
	result := executeApprovedDeployment(stack, definition, states[stack.Key], deploymentStateDir, 0, runDeploymentCommand, probeDeploymentHTTP)
	return json.NewEncoder(writer).Encode(result)
}

func loadDeploymentPolicyFile(path string, expectedUID uint32) (deploymentPolicy, error) {
	content, found, err := readRootOwnedFile(path, expectedUID, maximumDeploymentPolicy, 0o600)
	if err != nil {
		return deploymentPolicy{}, errors.New("deployment policy cannot be opened safely")
	}
	if !found {
		return deploymentPolicy{Version: deploymentPolicyVersion, Stacks: []deploymentPolicyStack{}}, nil
	}
	return decodeDeploymentPolicy(content)
}

func readRootOwnedFile(path string, expectedUID uint32, limit int, mode uint32) ([]byte, bool, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, false, errors.New("file handle is invalid")
	}
	defer file.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != expectedUID || stat.Mode&0o777 != mode {
		return nil, false, errors.New("file metadata is unsafe")
	}
	content, err := io.ReadAll(io.LimitReader(file, int64(limit+1)))
	if err != nil || len(content) > limit {
		return nil, false, errors.New("file exceeds limit")
	}
	return content, true, nil
}

func loadDeploymentStates(directory string, policy deploymentPolicy, expectedUID uint32) (map[string]deploymentState, error) {
	states := make(map[string]deploymentState, len(policy.Stacks))
	for _, stack := range policy.Stacks {
		path := deploymentOverridePath(directory, stack.Key)
		content, found, err := readRootOwnedFile(path, expectedUID, maximumDeploymentState, 0o600)
		if err != nil {
			return nil, fmt.Errorf("deployment state %q is unsafe", stack.Key)
		}
		if !found {
			continue
		}
		state, err := decodeDeploymentOverride(content, stack)
		if err != nil {
			return nil, fmt.Errorf("deployment state %q is invalid: %w", stack.Key, err)
		}
		states[stack.Key] = state
	}
	return states, nil
}

func decodeDeploymentOverride(content []byte, stack deploymentPolicyStack) (deploymentState, error) {
	lineEnd := bytes.IndexByte(content, '\n')
	if lineEnd < 0 || !bytes.HasPrefix(content[:lineEnd], []byte("# lodge-state ")) {
		return deploymentState{}, errors.New("state header is missing")
	}
	encoded := strings.TrimSpace(string(bytes.TrimPrefix(content[:lineEnd], []byte("# lodge-state "))))
	stateJSON, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return deploymentState{}, errors.New("state header is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(stateJSON))
	decoder.DisallowUnknownFields()
	var state deploymentState
	if err := decoder.Decode(&state); err != nil || ensureDeploymentEOF(decoder) != nil {
		return deploymentState{}, errors.New("state JSON is invalid")
	}
	if err := validateDeploymentState(state, stack); err != nil {
		return deploymentState{}, err
	}
	expected, err := encodeDeploymentOverride(state, stack)
	if err != nil || !bytes.Equal(content, expected) {
		return deploymentState{}, errors.New("state and Compose override differ")
	}
	return state, nil
}

func validateDeploymentState(state deploymentState, stack deploymentPolicyStack) error {
	if state.Version != deploymentStateVersion || state.StackKey != stack.Key {
		return errors.New("state identity is invalid")
	}
	if err := validateDeploymentStateRelease(state.Current, false); err != nil {
		return errors.New("current state release is invalid")
	}
	if err := validateDeploymentStateRelease(state.Previous, true); err != nil {
		return errors.New("previous state release is invalid")
	}
	if state.Previous.Image != "" && immutableImageDigest(state.Current.Image) == immutableImageDigest(state.Previous.Image) {
		return errors.New("current and previous state images are equal")
	}
	updated, err := time.Parse(time.RFC3339Nano, state.UpdatedAt)
	if err != nil || updated.Location() != time.UTC {
		return errors.New("state update time is invalid")
	}
	return nil
}

func validateDeploymentStateRelease(release deploymentStateRelease, optional bool) error {
	if optional && release == (deploymentStateRelease{}) {
		return nil
	}
	if !deploymentReleasePattern.MatchString(release.ID) || strings.TrimSpace(release.Label) == "" || utf8.RuneCountInString(release.Label) > 80 || deploymentTextHasControl(release.Label) || !immutableImagePattern.MatchString(release.Image) || len(release.Image) > 512 {
		return errors.New("state release fields are invalid")
	}
	return nil
}

func encodeDeploymentOverride(state deploymentState, stack deploymentPolicyStack) ([]byte, error) {
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	encoded := base64.RawURLEncoding.EncodeToString(stateJSON)
	content := fmt.Sprintf("# lodge-state %s\nservices:\n  %s:\n    image: %s\n", encoded, stack.Service, state.Current.Image)
	if len(content) > maximumDeploymentState {
		return nil, errors.New("deployment state exceeds limit")
	}
	return []byte(content), nil
}

func writeDeploymentOverrideCandidate(directory string, stack deploymentPolicyStack, state deploymentState, expectedUID uint32) (string, func() error, func(), error) {
	content, err := encodeDeploymentOverride(state, stack)
	if err != nil {
		return "", nil, nil, err
	}
	if err := ensureDeploymentStateDirectory(directory, expectedUID); err != nil {
		return "", nil, nil, err
	}
	file, err := os.CreateTemp(directory, "."+stack.Key+"-candidate-")
	if err != nil {
		return "", nil, nil, err
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, nil, err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, nil, err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, nil, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", nil, nil, err
	}
	commit := func() error {
		if err := os.Rename(path, deploymentOverridePath(directory, stack.Key)); err != nil {
			return err
		}
		directoryHandle, err := os.Open(directory)
		if err != nil {
			return err
		}
		defer directoryHandle.Close()
		return directoryHandle.Sync()
	}
	return path, commit, cleanup, nil
}

func ensureDeploymentStateDirectory(directory string, expectedUID uint32) error {
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return err
		}
		info, err = os.Lstat(directory)
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return errors.New("deployment state directory is unsafe")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != expectedUID {
		return errors.New("deployment state directory has an unexpected owner")
	}
	return nil
}

func deploymentOverridePath(directory, stackKey string) string {
	return filepath.Join(directory, stackKey+".override.yaml")
}

func decodeDeploymentExecutionRequest(reader io.Reader) (shared.DeploymentExecutionRequest, error) {
	content, err := io.ReadAll(io.LimitReader(reader, maximumDeploymentRequest+1))
	if err != nil || len(content) > maximumDeploymentRequest {
		return shared.DeploymentExecutionRequest{}, errors.New("deployment request exceeds limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var request shared.DeploymentExecutionRequest
	if err := decoder.Decode(&request); err != nil || ensureDeploymentEOF(decoder) != nil || strings.TrimSpace(request.ID) == "" || len(request.ID) > 256 || !utf8.ValidString(request.ID) {
		return shared.DeploymentExecutionRequest{}, errors.New("deployment request JSON is invalid")
	}
	return request, nil
}

func ensureDeploymentEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}

func findDeploymentStack(stacks []deploymentPolicyStack, key string) (deploymentPolicyStack, bool) {
	for _, stack := range stacks {
		if stack.Key == key {
			return stack, true
		}
	}
	return deploymentPolicyStack{}, false
}

func validateDeploymentStackFiles(stack deploymentPolicyStack, expectedUID uint32) error {
	for _, path := range []string{stack.ProjectDirectory, stack.ComposeFile} {
		if err := validateRootOwnedPathChain(path, expectedUID); err != nil {
			return err
		}
	}
	composeInfo, err := os.Lstat(stack.ComposeFile)
	if err != nil || !composeInfo.Mode().IsRegular() {
		return errors.New("Compose file is not a regular file")
	}
	envPath := filepath.Join(stack.ProjectDirectory, ".env")
	if _, err := os.Lstat(envPath); err == nil {
		if err := validateRootOwnedPathChain(envPath, expectedUID); err != nil {
			return err
		}
		info, err := os.Lstat(envPath)
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("Compose environment file is unsafe")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func validateRootOwnedPathChain(path string, expectedUID uint32) error {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) || clean != path {
		return errors.New("path is not clean and absolute")
	}
	current := string(filepath.Separator)
	parts := strings.Split(strings.TrimPrefix(clean, string(filepath.Separator)), string(filepath.Separator))
	for _, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
			return errors.New("deployment path chain is missing, writable, or symlinked")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != expectedUID {
			return errors.New("deployment path chain is not root-owned")
		}
	}
	return nil
}
