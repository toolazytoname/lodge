package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/toolazytoname/lodge/internal/shared"
)

const (
	maximumAgentActionBody = 128 << 10
	maximumAgentActions    = 256
	agentActionTimeout     = 20 * time.Second
)

var (
	hubSystemdActionResource = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@:-]{0,246}\.service$`)
	hubDockerActionResource  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
)

type agentActionClient interface {
	List(context.Context, AgentConfig) (shared.ActionsResponse, error)
	Execute(context.Context, AgentConfig, shared.ActionDefinition) (shared.ActionExecutionResult, error)
}

type httpAgentActionClient struct {
	client *http.Client
}

func newHTTPAgentActionClient() *httpAgentActionClient {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &httpAgentActionClient{client: &http.Client{
		Transport: transport,
		Timeout:   agentActionTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

func (client *httpAgentActionClient) List(ctx context.Context, agent AgentConfig) (shared.ActionsResponse, error) {
	request, err := newAgentActionRequest(ctx, http.MethodGet, agent, "/v1/actions")
	if err != nil {
		return shared.ActionsResponse{}, actionClientError("agent_invalid_config")
	}
	response, err := client.client.Do(request)
	if err != nil {
		return shared.ActionsResponse{}, classifyAgentActionNetworkError(ctx, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return shared.ActionsResponse{}, classifyAgentActionStatus(response.StatusCode)
	}
	var actions shared.ActionsResponse
	if err := decodeAgentActionJSON(response.Body, &actions); err != nil {
		return shared.ActionsResponse{}, actionClientError("agent_invalid_response")
	}
	if err := validateAgentActions(actions); err != nil {
		return shared.ActionsResponse{}, actionClientError("agent_invalid_response")
	}
	return actions, nil
}

// Execute sends one non-idempotent POST and deliberately never retries it. A
// lost response cannot prove whether the remote state change happened.
func (client *httpAgentActionClient) Execute(ctx context.Context, agent AgentConfig, definition shared.ActionDefinition) (shared.ActionExecutionResult, error) {
	if err := validateAgentActionDefinition(definition); err != nil {
		return shared.ActionExecutionResult{}, actionClientError("agent_invalid_action")
	}
	request, err := newAgentActionRequest(ctx, http.MethodPost, agent, "/v1/actions/"+url.PathEscape(definition.ID))
	if err != nil {
		return shared.ActionExecutionResult{}, actionClientError("agent_invalid_config")
	}
	response, err := client.client.Do(request)
	if err != nil {
		return shared.ActionExecutionResult{}, classifyAgentActionNetworkError(ctx, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusBadGateway {
		return shared.ActionExecutionResult{}, classifyAgentActionStatus(response.StatusCode)
	}
	var result shared.ActionExecutionResult
	if err := decodeAgentActionJSON(response.Body, &result); err != nil || validateAgentActionResult(result, definition) != nil {
		return shared.ActionExecutionResult{}, actionClientError("agent_invalid_response")
	}
	if (response.StatusCode == http.StatusOK && !result.OK) ||
		(response.StatusCode == http.StatusBadGateway && result.OK) {
		return shared.ActionExecutionResult{}, actionClientError("agent_invalid_response")
	}
	return result, nil
}

func newAgentActionRequest(ctx context.Context, method string, agent AgentConfig, path string) (*http.Request, error) {
	endpoint := strings.TrimRight(agent.URL, "/") + path
	request, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+agent.Token)
	return request, nil
}

func decodeAgentActionJSON(reader io.Reader, destination any) error {
	content, err := io.ReadAll(io.LimitReader(reader, maximumAgentActionBody+1))
	if err != nil || len(content) > maximumAgentActionBody {
		return errors.New("agent action response exceeds limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("agent action response has trailing JSON")
	}
	return nil
}

func validateAgentActions(response shared.ActionsResponse) error {
	if response.Actions == nil || len(response.Actions) > maximumAgentActions {
		return errors.New("agent action count is invalid")
	}
	seen := make(map[string]struct{}, len(response.Actions))
	for _, definition := range response.Actions {
		if err := validateAgentActionDefinition(definition); err != nil {
			return err
		}
		if _, duplicate := seen[definition.ID]; duplicate {
			return errors.New("agent action IDs are duplicated")
		}
		seen[definition.ID] = struct{}{}
	}
	return nil
}

func validateAgentActionDefinition(definition shared.ActionDefinition) error {
	if definition.ID != string(definition.Kind)+":"+definition.TargetKey || len(definition.ID) > 320 || strings.Contains(definition.ID, "/") {
		return errors.New("agent action identity is invalid")
	}
	if strings.TrimSpace(definition.TargetLabel) == "" || utf8.RuneCountInString(definition.TargetLabel) > 80 || actionTextHasControl(definition.TargetLabel) {
		return errors.New("agent action label is invalid")
	}
	if strings.TrimSpace(definition.Description) == "" || utf8.RuneCountInString(definition.Description) > 240 || actionTextHasControl(definition.Description) {
		return errors.New("agent action description is invalid")
	}
	if strings.TrimSpace(definition.Confirmation) == "" || utf8.RuneCountInString(definition.Confirmation) > 160 || actionTextHasControl(definition.Confirmation) {
		return errors.New("agent action confirmation is invalid")
	}
	var validTarget bool
	switch definition.TargetKind {
	case shared.ActionTargetSystemd:
		resource := strings.TrimPrefix(definition.TargetKey, "systemd:")
		validTarget = resource != definition.TargetKey && hubSystemdActionResource.MatchString(resource)
	case shared.ActionTargetDocker:
		resource := strings.TrimPrefix(definition.TargetKey, "docker:")
		validTarget = resource != definition.TargetKey && hubDockerActionResource.MatchString(resource)
	}
	if !validTarget {
		return errors.New("agent action target is invalid")
	}
	expectedRisk, ok := actionRisk(definition.Kind)
	if !ok || definition.Risk != expectedRisk {
		return errors.New("agent action kind or risk is invalid")
	}
	return nil
}

func actionRisk(kind shared.ActionKind) (shared.ActionRisk, bool) {
	switch kind {
	case shared.ActionLogs:
		return shared.ActionRiskRead, true
	case shared.ActionStart:
		return shared.ActionRiskChange, true
	case shared.ActionStop, shared.ActionRestart:
		return shared.ActionRiskDisruptive, true
	default:
		return "", false
	}
}

func validateAgentActionResult(result shared.ActionExecutionResult, expected shared.ActionDefinition) error {
	if result.ActionID != expected.ID || result.TargetKey != expected.TargetKey || result.Kind != expected.Kind {
		return errors.New("agent action result identity is invalid")
	}
	if result.OK == (result.ErrorKind != "") || !validAgentActionErrorKind(result.ErrorKind, result.OK) {
		return errors.New("agent action result state is invalid")
	}
	if (strings.TrimSpace(result.Summary) == "" && result.OK) ||
		utf8.RuneCountInString(result.Summary) > 240 || actionTextHasControl(result.Summary) {
		return errors.New("agent action result summary is invalid")
	}
	for _, state := range []string{result.StateBefore, result.StateAfter} {
		if state != "" && state != "running" && state != "stopped" {
			return errors.New("agent action target state is invalid")
		}
	}
	if expected.Kind == shared.ActionLogs {
		if result.StateBefore != "" || result.StateAfter != "" {
			return errors.New("log action returned state mutation fields")
		}
	} else {
		if len(result.Logs) != 0 || result.OK && (result.StateBefore == "" || result.StateAfter == "") {
			return errors.New("state action result is incomplete")
		}
		if (result.OK && expected.Kind == shared.ActionStop && result.StateAfter != "stopped") ||
			(result.OK && expected.Kind != shared.ActionStop && result.StateAfter != "running") {
			return errors.New("state action health result is inconsistent")
		}
	}
	if len(result.Logs) > 200 || !result.OK && len(result.Logs) != 0 {
		return errors.New("agent action logs are invalid")
	}
	total := 0
	for _, line := range result.Logs {
		if len(line) > 512 || actionLogHasControl(line) {
			return errors.New("agent action log line is invalid")
		}
		total += len(line)
	}
	if total > 64<<10 {
		return errors.New("agent action logs exceed limit")
	}
	return nil
}

func validAgentActionErrorKind(value string, ok bool) bool {
	if ok {
		return value == ""
	}
	switch value {
	case "log_read_failed", "state_read_failed", "command_failed", "health_verification_failed":
		return true
	default:
		return false
	}
}

func actionTextHasControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func actionLogHasControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) && character != '\t' {
			return true
		}
	}
	return false
}

type actionClientError string

func (err actionClientError) Error() string { return string(err) }

func classifyAgentActionNetworkError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return actionClientError("agent_timeout")
	}
	return actionClientError("agent_unavailable")
}

func classifyAgentActionStatus(status int) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return actionClientError("agent_auth_failed")
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return actionClientError("agent_incompatible")
	default:
		return actionClientError("agent_http_error")
	}
}

func actionClientErrorCategory(err error) string {
	var categorized actionClientError
	if errors.As(err, &categorized) {
		return string(categorized)
	}
	return "agent_unavailable"
}
