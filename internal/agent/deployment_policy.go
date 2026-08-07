package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/toolazytoname/lodge/internal/shared"
)

const (
	deploymentPolicyVersion   = 1
	deploymentStateVersion    = 1
	maximumDeploymentPolicy   = 256 << 10
	maximumDeploymentResponse = 256 << 10
	maximumDeploymentStacks   = 32
	maximumDeploymentReleases = 32
)

var (
	deploymentKeyPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)
	deploymentReleasePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	immutableImagePattern    = regexp.MustCompile(`^(?:[a-z0-9]+(?:[._-][a-z0-9]+)*(?::[0-9]{1,5})?/)?(?:[a-z0-9]+(?:[._-][a-z0-9]+)*/)*[a-z0-9]+(?:[._-][a-z0-9]+)*@sha256:[a-f0-9]{64}$`)
	dockerImageIDPattern     = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

type deploymentPolicy struct {
	Version int                     `json:"version"`
	Stacks  []deploymentPolicyStack `json:"stacks"`
}

type deploymentPolicyStack struct {
	Key              string                    `json:"key"`
	Label            string                    `json:"label"`
	ProjectDirectory string                    `json:"projectDirectory"`
	ComposeFile      string                    `json:"composeFile"`
	Service          string                    `json:"service"`
	Stateless        bool                      `json:"stateless"`
	Health           deploymentHealthPolicy    `json:"health"`
	Releases         []deploymentPolicyRelease `json:"releases"`
}

type deploymentHealthPolicy struct {
	Kind           string `json:"kind"`
	URL            string `json:"url,omitempty"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}

type deploymentPolicyRelease struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Image string `json:"image"`
}

type deploymentState struct {
	Version   int                    `json:"version"`
	StackKey  string                 `json:"stackKey"`
	Current   deploymentStateRelease `json:"current"`
	Previous  deploymentStateRelease `json:"previous"`
	UpdatedAt string                 `json:"updatedAt"`
}

type deploymentStateRelease struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Image string `json:"image"`
}

func decodeDeploymentPolicy(content []byte) (deploymentPolicy, error) {
	if len(content) > maximumDeploymentPolicy {
		return deploymentPolicy{}, errors.New("deployment policy exceeds 256 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var policy deploymentPolicy
	if err := decoder.Decode(&policy); err != nil {
		return deploymentPolicy{}, errors.New("deployment policy JSON is invalid")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return deploymentPolicy{}, errors.New("deployment policy contains trailing JSON")
	}
	if err := validateDeploymentPolicy(policy); err != nil {
		return deploymentPolicy{}, err
	}
	return policy, nil
}

func validateDeploymentPolicy(policy deploymentPolicy) error {
	if policy.Version != deploymentPolicyVersion {
		return fmt.Errorf("deployment policy version must be %d", deploymentPolicyVersion)
	}
	if policy.Stacks == nil || len(policy.Stacks) > maximumDeploymentStacks {
		return errors.New("deployment policy stacks are missing or exceed limit")
	}
	seen := make(map[string]struct{}, len(policy.Stacks))
	for _, stack := range policy.Stacks {
		if err := validateDeploymentStack(stack); err != nil {
			return err
		}
		if _, duplicate := seen[stack.Key]; duplicate {
			return fmt.Errorf("duplicate deployment stack %q", stack.Key)
		}
		seen[stack.Key] = struct{}{}
	}
	return nil
}

func validateDeploymentStack(stack deploymentPolicyStack) error {
	if !deploymentKeyPattern.MatchString(stack.Key) {
		return errors.New("deployment stack key is invalid")
	}
	if strings.TrimSpace(stack.Label) == "" || utf8.RuneCountInString(stack.Label) > 80 || deploymentTextHasControl(stack.Label) {
		return fmt.Errorf("deployment stack %q label is invalid", stack.Key)
	}
	if !stack.Stateless {
		return fmt.Errorf("deployment stack %q must explicitly be stateless; stateful backup adapters are not implemented", stack.Key)
	}
	if err := validateDeploymentPath(stack.ProjectDirectory); err != nil {
		return fmt.Errorf("deployment stack %q project directory is invalid", stack.Key)
	}
	if err := validateDeploymentPath(stack.ComposeFile); err != nil {
		return fmt.Errorf("deployment stack %q compose file is invalid", stack.Key)
	}
	relative, err := filepath.Rel(stack.ProjectDirectory, stack.ComposeFile)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("deployment stack %q compose file escapes project directory", stack.Key)
	}
	if !dockerActionResourcePattern.MatchString(stack.Service) {
		return fmt.Errorf("deployment stack %q service is invalid", stack.Key)
	}
	if err := validateDeploymentHealth(stack.Health); err != nil {
		return fmt.Errorf("deployment stack %q health policy is invalid: %w", stack.Key, err)
	}
	if len(stack.Releases) < 1 || len(stack.Releases) > maximumDeploymentReleases {
		return fmt.Errorf("deployment stack %q must have 1..%d releases", stack.Key, maximumDeploymentReleases)
	}
	seenRelease, seenDigest := make(map[string]struct{}), make(map[string]struct{})
	for _, release := range stack.Releases {
		if !deploymentReleasePattern.MatchString(release.ID) || strings.EqualFold(release.ID, "external") {
			return fmt.Errorf("deployment stack %q release ID is invalid", stack.Key)
		}
		if strings.TrimSpace(release.Label) == "" || utf8.RuneCountInString(release.Label) > 80 || deploymentTextHasControl(release.Label) {
			return fmt.Errorf("deployment stack %q release label is invalid", stack.Key)
		}
		if !immutableImagePattern.MatchString(release.Image) || len(release.Image) > 512 {
			return fmt.Errorf("deployment stack %q release image must be an immutable sha256 reference", stack.Key)
		}
		if _, duplicate := seenRelease[release.ID]; duplicate {
			return fmt.Errorf("deployment stack %q repeats release %q", stack.Key, release.ID)
		}
		digest := immutableImageDigest(release.Image)
		if _, duplicate := seenDigest[digest]; duplicate {
			return fmt.Errorf("deployment stack %q repeats an immutable image digest", stack.Key)
		}
		seenRelease[release.ID], seenDigest[digest] = struct{}{}, struct{}{}
	}
	return nil
}

func validateDeploymentPath(value string) error {
	if value == "" || len(value) > 512 || !utf8.ValidString(value) || deploymentTextHasControl(value) || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return errors.New("path must be a clean absolute UTF-8 path")
	}
	return nil
}

func validateDeploymentHealth(health deploymentHealthPolicy) error {
	if health.TimeoutSeconds < 10 || health.TimeoutSeconds > 300 {
		return errors.New("timeout must be 10..300 seconds")
	}
	switch health.Kind {
	case "docker":
		if health.URL != "" {
			return errors.New("Docker health must not contain a URL")
		}
	case "http":
		parsed, err := url.Parse(health.URL)
		if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Port() == "" {
			return errors.New("HTTP health must be an explicit loopback URL without credentials, query, or fragment")
		}
		port, err := strconv.Atoi(parsed.Port())
		if err != nil || port < 1 || port > 65535 {
			return errors.New("HTTP health port is invalid")
		}
		if parsed.Path == "" {
			return errors.New("HTTP health URL must contain a path")
		}
	default:
		return errors.New("health kind must be docker or http")
	}
	return nil
}

func deploymentTextHasControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func immutableImageDigest(image string) string {
	_, digest, found := strings.Cut(image, "@sha256:")
	if !found {
		return ""
	}
	return digest
}

func deploymentDefinitions(policy deploymentPolicy, states map[string]deploymentState) []shared.DeploymentDefinition {
	definitions := make([]shared.DeploymentDefinition, 0)
	for _, stack := range policy.Stacks {
		state := states[stack.Key]
		for _, release := range stack.Releases {
			if state.Current.Image == release.Image {
				continue
			}
			definitions = append(definitions, shared.DeploymentDefinition{
				ID:       deploymentActionID(shared.DeploymentDeploy, stack.Key, release.ID),
				StackKey: stack.Key, StackLabel: stack.Label, Kind: shared.DeploymentDeploy,
				ReleaseID: release.ID, ReleaseLabel: release.Label, Image: release.Image,
				CurrentReleaseID: state.Current.ID, PreviousReleaseID: state.Previous.ID,
				Description:  "部署不可变镜像并验证；失败自动回滚：" + stack.Label + " / " + release.Label,
				Confirmation: "确认部署 " + stack.Label + " 到 " + release.Label,
				Risk:         shared.ActionRiskDisruptive,
			})
		}
		if state.Previous.Image != "" {
			definitions = append(definitions, shared.DeploymentDefinition{
				ID:       deploymentActionID(shared.DeploymentRollback, stack.Key, ""),
				StackKey: stack.Key, StackLabel: stack.Label, Kind: shared.DeploymentRollback,
				ReleaseID: state.Previous.ID, ReleaseLabel: state.Previous.Label, Image: state.Previous.Image,
				CurrentReleaseID: state.Current.ID, PreviousReleaseID: state.Previous.ID,
				Description:  "回滚到上一个已验证版本：" + stack.Label + " / " + state.Previous.Label,
				Confirmation: "确认回滚 " + stack.Label + " 到 " + state.Previous.Label,
				Risk:         shared.ActionRiskDisruptive,
			})
		}
	}
	sort.Slice(definitions, func(left, right int) bool {
		if definitions[left].StackLabel != definitions[right].StackLabel {
			return definitions[left].StackLabel < definitions[right].StackLabel
		}
		if definitions[left].Kind != definitions[right].Kind {
			return definitions[left].Kind < definitions[right].Kind
		}
		return definitions[left].ReleaseLabel < definitions[right].ReleaseLabel
	})
	return definitions
}

func deploymentActionID(kind shared.DeploymentKind, stackKey, releaseID string) string {
	if kind == shared.DeploymentRollback {
		return "rollback:" + stackKey
	}
	return "deploy:" + stackKey + ":" + releaseID
}

func findDeploymentDefinition(definitions []shared.DeploymentDefinition, id string) (shared.DeploymentDefinition, bool) {
	for _, definition := range definitions {
		if definition.ID == id {
			return definition, true
		}
	}
	return shared.DeploymentDefinition{}, false
}

func decodeDeploymentsResponse(content []byte) (shared.DeploymentsResponse, error) {
	if len(content) > maximumDeploymentResponse {
		return shared.DeploymentsResponse{}, errors.New("deployment response exceeds limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var response shared.DeploymentsResponse
	if err := decoder.Decode(&response); err != nil || ensureDeploymentResponseEOF(decoder) != nil {
		return shared.DeploymentsResponse{}, errors.New("deployment response JSON is invalid")
	}
	if response.Deployments == nil || len(response.Deployments) > maximumDeploymentStacks*(maximumDeploymentReleases+1) {
		return shared.DeploymentsResponse{}, errors.New("deployment response count is invalid")
	}
	seen := make(map[string]struct{}, len(response.Deployments))
	for _, definition := range response.Deployments {
		if err := validateDeploymentDefinition(definition); err != nil {
			return shared.DeploymentsResponse{}, err
		}
		if _, duplicate := seen[definition.ID]; duplicate {
			return shared.DeploymentsResponse{}, errors.New("deployment response contains duplicate IDs")
		}
		seen[definition.ID] = struct{}{}
	}
	return response, nil
}

func validateDeploymentDefinition(definition shared.DeploymentDefinition) error {
	if !deploymentKeyPattern.MatchString(definition.StackKey) ||
		strings.TrimSpace(definition.StackLabel) == "" || utf8.RuneCountInString(definition.StackLabel) > 80 || deploymentTextHasControl(definition.StackLabel) ||
		!deploymentReleasePattern.MatchString(definition.ReleaseID) ||
		strings.TrimSpace(definition.ReleaseLabel) == "" || utf8.RuneCountInString(definition.ReleaseLabel) > 80 || deploymentTextHasControl(definition.ReleaseLabel) ||
		!immutableImagePattern.MatchString(definition.Image) || len(definition.Image) > 512 {
		return errors.New("deployment definition fields are invalid")
	}
	for _, releaseID := range []string{definition.CurrentReleaseID, definition.PreviousReleaseID} {
		if releaseID != "" && !deploymentReleasePattern.MatchString(releaseID) {
			return errors.New("deployment definition state identity is invalid")
		}
	}
	if definition.Risk != shared.ActionRiskDisruptive {
		return errors.New("deployment definition risk is invalid")
	}
	switch definition.Kind {
	case shared.DeploymentDeploy:
		if strings.EqualFold(definition.ReleaseID, "external") || definition.ID != deploymentActionID(definition.Kind, definition.StackKey, definition.ReleaseID) ||
			definition.Description != "部署不可变镜像并验证；失败自动回滚："+definition.StackLabel+" / "+definition.ReleaseLabel ||
			definition.Confirmation != "确认部署 "+definition.StackLabel+" 到 "+definition.ReleaseLabel {
			return errors.New("deployment definition deploy presentation is invalid")
		}
	case shared.DeploymentRollback:
		if definition.ID != deploymentActionID(definition.Kind, definition.StackKey, "") ||
			definition.Description != "回滚到上一个已验证版本："+definition.StackLabel+" / "+definition.ReleaseLabel ||
			definition.Confirmation != "确认回滚 "+definition.StackLabel+" 到 "+definition.ReleaseLabel {
			return errors.New("deployment definition rollback presentation is invalid")
		}
	default:
		return errors.New("deployment definition kind is invalid")
	}
	return nil
}

func decodeDeploymentExecutionResult(content []byte, expected shared.DeploymentDefinition) (shared.DeploymentExecutionResult, error) {
	if len(content) > maximumDeploymentResponse {
		return shared.DeploymentExecutionResult{}, errors.New("deployment result exceeds limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var result shared.DeploymentExecutionResult
	if err := decoder.Decode(&result); err != nil || ensureDeploymentResponseEOF(decoder) != nil {
		return shared.DeploymentExecutionResult{}, errors.New("deployment result JSON is invalid")
	}
	if result.ActionID != expected.ID || result.StackKey != expected.StackKey || result.Kind != expected.Kind || result.ReleaseID != expected.ReleaseID ||
		expected.CurrentReleaseID != "" && result.PreviousReleaseID != expected.CurrentReleaseID ||
		result.PreviousReleaseID != "" && !deploymentReleasePattern.MatchString(result.PreviousReleaseID) {
		return shared.DeploymentExecutionResult{}, errors.New("deployment result identity is invalid")
	}
	if result.OK == (result.ErrorKind != "") || !validDeploymentErrorKind(result.ErrorKind, result.OK) {
		return shared.DeploymentExecutionResult{}, errors.New("deployment result state is invalid")
	}
	if result.OK && result.RollbackPerformed || result.ErrorKind == "rollback_failed" && result.RollbackPerformed {
		return shared.DeploymentExecutionResult{}, errors.New("deployment result rollback state is invalid")
	}
	if deploymentTextHasControl(result.Summary) || utf8.RuneCountInString(result.Summary) > 200 {
		return shared.DeploymentExecutionResult{}, errors.New("deployment result summary is invalid")
	}
	return result, nil
}

func validDeploymentErrorKind(value string, ok bool) bool {
	if ok {
		return value == ""
	}
	switch value {
	case "preflight_failed", "image_prepare_failed", "current_release_unknown", "state_prepare_failed", "compose_apply_failed", "health_verification_failed", "state_commit_failed", "rollback_failed":
		return true
	default:
		return false
	}
}

func ensureDeploymentResponseEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}
