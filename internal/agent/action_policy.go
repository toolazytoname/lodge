package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/toolazytoname/lodge/internal/shared"
)

const (
	actionPolicyVersion    = 1
	maximumActionPolicy    = 64 << 10
	maximumActionResponse  = 128 << 10
	maximumActionTargets   = 64
	maximumActionLogLines  = 200
	maximumActionLogBytes  = 64 << 10
	maximumActionLineBytes = 512
)

var (
	systemdActionResourcePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@:-]{0,246}\.service$`)
	dockerActionResourcePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	actionBearerPattern          = regexp.MustCompile(`(?i)\bBearer[ \t]+[A-Za-z0-9._~+/=-]{8,}`)
	actionKeyValuePattern        = regexp.MustCompile(`(?i)\b(password|passwd|token|secret|api[_-]?key|authorization)([ \t]*[:=][ \t]*)("[^"\r\n]*"|'[^'\r\n]*'|[^ \t,;]+)`)
	actionURLUserinfoPattern     = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)([^/@[:space:]]+)@`)
)

type actionPolicy struct {
	Version int                  `json:"version"`
	Targets []actionPolicyTarget `json:"targets"`
}

type actionPolicyTarget struct {
	Key      string                  `json:"key"`
	Label    string                  `json:"label"`
	Kind     shared.ActionTargetKind `json:"kind"`
	Resource string                  `json:"resource"`
	Actions  []shared.ActionKind     `json:"actions"`
}

func decodeActionPolicy(content []byte) (actionPolicy, error) {
	if len(content) > maximumActionPolicy {
		return actionPolicy{}, errors.New("action policy exceeds 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var policy actionPolicy
	if err := decoder.Decode(&policy); err != nil {
		return actionPolicy{}, errors.New("action policy JSON is invalid")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return actionPolicy{}, errors.New("action policy contains trailing JSON")
	}
	if err := validateActionPolicy(policy); err != nil {
		return actionPolicy{}, err
	}
	return policy, nil
}

func validateActionPolicy(policy actionPolicy) error {
	if policy.Version != actionPolicyVersion {
		return fmt.Errorf("action policy version must be %d", actionPolicyVersion)
	}
	if policy.Targets == nil || len(policy.Targets) > maximumActionTargets {
		return errors.New("action policy targets are missing or exceed limit")
	}
	seenTargets := make(map[string]struct{}, len(policy.Targets))
	seenActions := make(map[string]struct{})
	for _, target := range policy.Targets {
		if err := validateActionTarget(target); err != nil {
			return err
		}
		if _, duplicate := seenTargets[target.Key]; duplicate {
			return fmt.Errorf("duplicate action target %q", target.Key)
		}
		seenTargets[target.Key] = struct{}{}
		for _, action := range target.Actions {
			id := actionID(target.Key, action)
			if _, duplicate := seenActions[id]; duplicate {
				return fmt.Errorf("duplicate action %q", id)
			}
			seenActions[id] = struct{}{}
		}
	}
	return nil
}

func validateActionTarget(target actionPolicyTarget) error {
	if target.Key == "" || len(target.Key) > 256 || !utf8.ValidString(target.Key) {
		return errors.New("action target key is invalid")
	}
	if strings.TrimSpace(target.Label) == "" || utf8.RuneCountInString(target.Label) > 80 || hasActionControl(target.Label) {
		return fmt.Errorf("action target %q label is invalid", target.Key)
	}
	if len(target.Actions) < 1 || len(target.Actions) > 4 {
		return fmt.Errorf("action target %q must approve 1..4 actions", target.Key)
	}
	switch target.Kind {
	case shared.ActionTargetSystemd:
		if !systemdActionResourcePattern.MatchString(target.Resource) || target.Key != "systemd:"+target.Resource {
			return fmt.Errorf("action target %q has invalid systemd identity", target.Key)
		}
	case shared.ActionTargetDocker:
		if !dockerActionResourcePattern.MatchString(target.Resource) || target.Key != "docker:"+target.Resource {
			return fmt.Errorf("action target %q has invalid Docker identity", target.Key)
		}
	default:
		return fmt.Errorf("action target %q kind is invalid", target.Key)
	}
	seen := make(map[shared.ActionKind]struct{}, len(target.Actions))
	for _, action := range target.Actions {
		switch action {
		case shared.ActionStart, shared.ActionStop, shared.ActionRestart, shared.ActionLogs:
		default:
			return fmt.Errorf("action target %q action %q is invalid", target.Key, action)
		}
		if _, duplicate := seen[action]; duplicate {
			return fmt.Errorf("action target %q repeats action %q", target.Key, action)
		}
		seen[action] = struct{}{}
	}
	return nil
}

func hasActionControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func actionID(targetKey string, action shared.ActionKind) string {
	return string(action) + ":" + targetKey
}

func actionDefinitions(policy actionPolicy) []shared.ActionDefinition {
	definitions := make([]shared.ActionDefinition, 0, len(policy.Targets)*2)
	for _, target := range policy.Targets {
		for _, action := range target.Actions {
			verb, description, risk := actionPresentation(action)
			definitions = append(definitions, shared.ActionDefinition{
				ID: actionID(target.Key, action), TargetKey: target.Key, TargetLabel: target.Label,
				TargetKind: target.Kind, Kind: action, Description: description + target.Label,
				Confirmation: "确认" + verb + " " + target.Label, Risk: risk,
			})
		}
	}
	sort.Slice(definitions, func(left, right int) bool {
		if definitions[left].TargetLabel != definitions[right].TargetLabel {
			return definitions[left].TargetLabel < definitions[right].TargetLabel
		}
		return actionOrder(definitions[left].Kind) < actionOrder(definitions[right].Kind)
	})
	return definitions
}

func actionPresentation(action shared.ActionKind) (verb, description string, risk shared.ActionRisk) {
	switch action {
	case shared.ActionStart:
		return "启动", "启动并验证运行状态：", shared.ActionRiskChange
	case shared.ActionStop:
		return "停止", "停止并验证退出状态：", shared.ActionRiskDisruptive
	case shared.ActionRestart:
		return "重启", "重启并验证恢复运行：", shared.ActionRiskDisruptive
	case shared.ActionLogs:
		return "读取日志", "读取最多 200 行脱敏日志：", shared.ActionRiskRead
	default:
		return "执行", "执行：", shared.ActionRiskDisruptive
	}
}

func actionOrder(action shared.ActionKind) int {
	switch action {
	case shared.ActionLogs:
		return 0
	case shared.ActionStart:
		return 1
	case shared.ActionRestart:
		return 2
	case shared.ActionStop:
		return 3
	default:
		return 4
	}
}

func approvedAction(policy actionPolicy, id string) (actionPolicyTarget, shared.ActionDefinition, bool) {
	for _, target := range policy.Targets {
		for _, definition := range actionDefinitions(actionPolicy{Version: actionPolicyVersion, Targets: []actionPolicyTarget{target}}) {
			if definition.ID == id {
				return target, definition, true
			}
		}
	}
	return actionPolicyTarget{}, shared.ActionDefinition{}, false
}

func decodeActionsResponse(content []byte) (shared.ActionsResponse, error) {
	if len(content) > maximumActionResponse {
		return shared.ActionsResponse{}, errors.New("action response exceeds limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var response shared.ActionsResponse
	if err := decoder.Decode(&response); err != nil {
		return shared.ActionsResponse{}, errors.New("action response JSON is invalid")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return shared.ActionsResponse{}, errors.New("action response contains trailing JSON")
	}
	if response.Actions == nil || len(response.Actions) > maximumActionTargets*4 {
		return shared.ActionsResponse{}, errors.New("action response count is invalid")
	}
	seen := make(map[string]struct{}, len(response.Actions))
	for _, definition := range response.Actions {
		if err := validateActionDefinition(definition); err != nil {
			return shared.ActionsResponse{}, err
		}
		if _, duplicate := seen[definition.ID]; duplicate {
			return shared.ActionsResponse{}, errors.New("action response contains duplicate IDs")
		}
		seen[definition.ID] = struct{}{}
	}
	return response, nil
}

func validateActionDefinition(definition shared.ActionDefinition) error {
	if definition.ID != actionID(definition.TargetKey, definition.Kind) {
		return errors.New("action definition identity is invalid")
	}
	if strings.TrimSpace(definition.TargetLabel) == "" || utf8.RuneCountInString(definition.TargetLabel) > 80 || hasActionControl(definition.TargetLabel) {
		return errors.New("action definition label is invalid")
	}
	resource := strings.TrimPrefix(definition.TargetKey, string(definition.TargetKind)+":")
	target := actionPolicyTarget{
		Key: definition.TargetKey, Label: definition.TargetLabel, Kind: definition.TargetKind,
		Resource: resource, Actions: []shared.ActionKind{definition.Kind},
	}
	if err := validateActionTarget(target); err != nil {
		return errors.New("action definition target is invalid")
	}
	verb, description, risk := actionPresentation(definition.Kind)
	if definition.Description != description+definition.TargetLabel ||
		definition.Confirmation != "确认"+verb+" "+definition.TargetLabel || definition.Risk != risk {
		return errors.New("action definition presentation is invalid")
	}
	return nil
}

func decodeActionExecutionResult(content []byte, expected shared.ActionDefinition) (shared.ActionExecutionResult, error) {
	if len(content) > maximumActionResponse {
		return shared.ActionExecutionResult{}, errors.New("action result exceeds limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var result shared.ActionExecutionResult
	if err := decoder.Decode(&result); err != nil {
		return shared.ActionExecutionResult{}, errors.New("action result JSON is invalid")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return shared.ActionExecutionResult{}, errors.New("action result contains trailing JSON")
	}
	if result.ActionID != expected.ID || result.TargetKey != expected.TargetKey || result.Kind != expected.Kind {
		return shared.ActionExecutionResult{}, errors.New("action result identity is invalid")
	}
	if result.ErrorKind != "" && !validActionErrorKind(result.ErrorKind) {
		return shared.ActionExecutionResult{}, errors.New("action result error kind is invalid")
	}
	if result.OK == (result.ErrorKind != "") || len(result.Logs) > maximumActionLogLines {
		return shared.ActionExecutionResult{}, errors.New("action result state is invalid")
	}
	if result.StateBefore != "" && result.StateBefore != "running" && result.StateBefore != "stopped" {
		return shared.ActionExecutionResult{}, errors.New("action result before state is invalid")
	}
	if result.StateAfter != "" && result.StateAfter != "running" && result.StateAfter != "stopped" {
		return shared.ActionExecutionResult{}, errors.New("action result after state is invalid")
	}
	if hasActionControl(result.Summary) || utf8.RuneCountInString(result.Summary) > 200 {
		return shared.ActionExecutionResult{}, errors.New("action result summary is invalid")
	}
	total := 0
	for _, line := range result.Logs {
		if hasActionControlExceptTab(line) || len(line) > maximumActionLineBytes {
			return shared.ActionExecutionResult{}, errors.New("action result log line is invalid")
		}
		total += len(line)
	}
	if total > maximumActionLogBytes {
		return shared.ActionExecutionResult{}, errors.New("action result logs exceed limit")
	}
	return result, nil
}

func validActionErrorKind(value string) bool {
	switch value {
	case "log_read_failed", "state_read_failed", "command_failed", "health_verification_failed":
		return true
	default:
		return false
	}
}

func hasActionControlExceptTab(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) && character != '\t' {
			return true
		}
	}
	return false
}

func sanitizeActionLogLines(content []byte) []string {
	content = bytes.ToValidUTF8(content, []byte("�"))
	rawLines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(rawLines) > maximumActionLogLines {
		rawLines = rawLines[len(rawLines)-maximumActionLogLines:]
	}
	lines := make([]string, 0, len(rawLines))
	total := 0
	for _, raw := range rawLines {
		line := sanitizeActionLogLine(raw)
		if line == "" {
			continue
		}
		if total+len(line) > maximumActionLogBytes {
			break
		}
		lines = append(lines, line)
		total += len(line)
	}
	return lines
}

func sanitizeActionLogLine(value string) string {
	value = actionBearerPattern.ReplaceAllString(value, "Bearer [REDACTED]")
	value = actionKeyValuePattern.ReplaceAllString(value, "$1$2[REDACTED]")
	value = actionURLUserinfoPattern.ReplaceAllString(value, "$1[REDACTED]@")
	var cleaned strings.Builder
	for _, character := range value {
		if character == '\t' || character >= ' ' && character != 0x7f {
			if cleaned.Len()+utf8.RuneLen(character) > maximumActionLineBytes {
				break
			}
			cleaned.WriteRune(character)
		}
	}
	return strings.TrimSpace(cleaned.String())
}
