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
	maximumAgentDeploymentBody = 256 << 10
	maximumAgentDeployments    = 32 * 33
	agentDeploymentTimeout     = 18 * time.Minute
)

var (
	hubDeploymentKeyPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)
	hubDeploymentReleasePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	hubImmutableImagePattern    = regexp.MustCompile(`^(?:[a-z0-9]+(?:[._-][a-z0-9]+)*(?::[0-9]{1,5})?/)?(?:[a-z0-9]+(?:[._-][a-z0-9]+)*/)*[a-z0-9]+(?:[._-][a-z0-9]+)*@sha256:[a-f0-9]{64}$`)
)

type agentDeploymentClient interface {
	List(context.Context, AgentConfig) (shared.DeploymentsResponse, error)
	Execute(context.Context, AgentConfig, shared.DeploymentDefinition) (shared.DeploymentExecutionResult, error)
}

type httpAgentDeploymentClient struct {
	client *http.Client
}

func newHTTPAgentDeploymentClient() *httpAgentDeploymentClient {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &httpAgentDeploymentClient{client: &http.Client{
		Transport: transport,
		Timeout:   agentDeploymentTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

func (client *httpAgentDeploymentClient) List(ctx context.Context, agent AgentConfig) (shared.DeploymentsResponse, error) {
	request, err := newAgentDeploymentRequest(ctx, http.MethodGet, agent, "/v1/deployments")
	if err != nil {
		return shared.DeploymentsResponse{}, actionClientError("agent_invalid_config")
	}
	response, err := client.client.Do(request)
	if err != nil {
		return shared.DeploymentsResponse{}, classifyAgentActionNetworkError(ctx, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return shared.DeploymentsResponse{}, classifyAgentActionStatus(response.StatusCode)
	}
	var deployments shared.DeploymentsResponse
	if err := decodeAgentDeploymentJSON(response.Body, &deployments); err != nil || validateAgentDeployments(deployments) != nil {
		return shared.DeploymentsResponse{}, actionClientError("agent_invalid_response")
	}
	return deployments, nil
}

// Execute performs one non-idempotent request. Host-local rollback is part of
// that single request; the Hub never retries a timeout or lost response.
func (client *httpAgentDeploymentClient) Execute(ctx context.Context, agent AgentConfig, definition shared.DeploymentDefinition) (shared.DeploymentExecutionResult, error) {
	if err := validateAgentDeploymentDefinition(definition); err != nil {
		return shared.DeploymentExecutionResult{}, actionClientError("agent_invalid_action")
	}
	request, err := newAgentDeploymentRequest(ctx, http.MethodPost, agent, "/v1/deployments/"+url.PathEscape(definition.ID))
	if err != nil {
		return shared.DeploymentExecutionResult{}, actionClientError("agent_invalid_config")
	}
	response, err := client.client.Do(request)
	if err != nil {
		return shared.DeploymentExecutionResult{}, classifyAgentActionNetworkError(ctx, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusBadGateway {
		return shared.DeploymentExecutionResult{}, classifyAgentActionStatus(response.StatusCode)
	}
	var result shared.DeploymentExecutionResult
	if err := decodeAgentDeploymentJSON(response.Body, &result); err != nil || validateAgentDeploymentResult(result, definition) != nil {
		return shared.DeploymentExecutionResult{}, actionClientError("agent_invalid_response")
	}
	if response.StatusCode == http.StatusOK && !result.OK || response.StatusCode == http.StatusBadGateway && result.OK {
		return shared.DeploymentExecutionResult{}, actionClientError("agent_invalid_response")
	}
	return result, nil
}

func newAgentDeploymentRequest(ctx context.Context, method string, agent AgentConfig, path string) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(agent.URL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+agent.Token)
	return request, nil
}

func decodeAgentDeploymentJSON(reader io.Reader, destination any) error {
	content, err := io.ReadAll(io.LimitReader(reader, maximumAgentDeploymentBody+1))
	if err != nil || len(content) > maximumAgentDeploymentBody {
		return errors.New("agent deployment response exceeds limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("agent deployment response has trailing JSON")
	}
	return nil
}

func validateAgentDeployments(response shared.DeploymentsResponse) error {
	if response.Deployments == nil || len(response.Deployments) > maximumAgentDeployments {
		return errors.New("agent deployment count is invalid")
	}
	seen := make(map[string]struct{}, len(response.Deployments))
	for _, definition := range response.Deployments {
		if err := validateAgentDeploymentDefinition(definition); err != nil {
			return err
		}
		if _, duplicate := seen[definition.ID]; duplicate {
			return errors.New("agent deployment IDs are duplicated")
		}
		seen[definition.ID] = struct{}{}
	}
	return nil
}

func validateAgentDeploymentDefinition(definition shared.DeploymentDefinition) error {
	if !hubDeploymentKeyPattern.MatchString(definition.StackKey) || strings.Contains(definition.ID, "/") ||
		!validDeploymentText(definition.StackLabel, 80) || !hubDeploymentReleasePattern.MatchString(definition.ReleaseID) ||
		!validDeploymentText(definition.ReleaseLabel, 80) || !hubImmutableImagePattern.MatchString(definition.Image) || len(definition.Image) > 512 {
		return errors.New("agent deployment fields are invalid")
	}
	for _, releaseID := range []string{definition.CurrentReleaseID, definition.PreviousReleaseID} {
		if releaseID != "" && !hubDeploymentReleasePattern.MatchString(releaseID) {
			return errors.New("agent deployment state identity is invalid")
		}
	}
	if definition.Risk != shared.ActionRiskDisruptive {
		return errors.New("agent deployment risk is invalid")
	}
	switch definition.Kind {
	case shared.DeploymentDeploy:
		if strings.EqualFold(definition.ReleaseID, "external") || definition.ID != "deploy:"+definition.StackKey+":"+definition.ReleaseID ||
			definition.Description != "部署不可变镜像并验证；失败自动回滚："+definition.StackLabel+" / "+definition.ReleaseLabel ||
			definition.Confirmation != "确认部署 "+definition.StackLabel+" 到 "+definition.ReleaseLabel {
			return errors.New("agent deployment presentation is invalid")
		}
	case shared.DeploymentRollback:
		if definition.ID != "rollback:"+definition.StackKey ||
			definition.Description != "回滚到上一个已验证版本："+definition.StackLabel+" / "+definition.ReleaseLabel ||
			definition.Confirmation != "确认回滚 "+definition.StackLabel+" 到 "+definition.ReleaseLabel {
			return errors.New("agent rollback presentation is invalid")
		}
	default:
		return errors.New("agent deployment kind is invalid")
	}
	return nil
}

func validateAgentDeploymentResult(result shared.DeploymentExecutionResult, expected shared.DeploymentDefinition) error {
	if result.ActionID != expected.ID || result.StackKey != expected.StackKey || result.Kind != expected.Kind || result.ReleaseID != expected.ReleaseID ||
		expected.CurrentReleaseID != "" && result.PreviousReleaseID != expected.CurrentReleaseID ||
		result.PreviousReleaseID != "" && !hubDeploymentReleasePattern.MatchString(result.PreviousReleaseID) {
		return errors.New("agent deployment result identity is invalid")
	}
	if result.OK == (result.ErrorKind != "") || !validAgentDeploymentErrorKind(result.ErrorKind, result.OK) {
		return errors.New("agent deployment result state is invalid")
	}
	if result.OK && result.RollbackPerformed || result.ErrorKind == "rollback_failed" && result.RollbackPerformed {
		return errors.New("agent deployment rollback state is invalid")
	}
	if result.OK || result.RollbackPerformed {
		if !validDeploymentText(result.Summary, 200) {
			return errors.New("agent deployment result summary is missing")
		}
	} else if result.Summary != "" && !validDeploymentText(result.Summary, 200) {
		return errors.New("agent deployment result summary is invalid")
	}
	return nil
}

func validAgentDeploymentErrorKind(value string, ok bool) bool {
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

func validDeploymentText(value string, maximumRunes int) bool {
	if strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > maximumRunes || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
