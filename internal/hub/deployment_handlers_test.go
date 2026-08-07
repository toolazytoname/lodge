package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/toolazytoname/lodge/internal/domain"
	"github.com/toolazytoname/lodge/internal/shared"
)

type stubAgentDeploymentClient struct {
	response   shared.DeploymentsResponse
	result     shared.DeploymentExecutionResult
	listErr    error
	executeErr error
	entered    chan struct{}
	release    chan struct{}
	executions atomic.Int32
}

func (client *stubAgentDeploymentClient) List(context.Context, AgentConfig) (shared.DeploymentsResponse, error) {
	return client.response, client.listErr
}

func (client *stubAgentDeploymentClient) Execute(ctx context.Context, _ AgentConfig, _ shared.DeploymentDefinition) (shared.DeploymentExecutionResult, error) {
	client.executions.Add(1)
	if client.entered != nil {
		select {
		case client.entered <- struct{}{}:
		default:
		}
	}
	if client.release != nil {
		select {
		case <-client.release:
		case <-ctx.Done():
			return shared.DeploymentExecutionResult{}, ctx.Err()
		}
	}
	return client.result, client.executeErr
}

func TestDeclarativeDeploymentAPIIsAcceptedAndAuditsAutomaticRollback(t *testing.T) {
	agents := []AgentConfig{{ID: "host-a", Name: "Host A", URL: "http://agent", Token: "runtime-only"}}
	store, databasePath := openTestSQLiteStore(t, agents)
	if err := store.Update(context.Background(), "host-a", true, "", shared.Ping{AgentVer: "0.7.0"}, &shared.Status{}, []shared.Service{}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	definition := hubDeploymentDefinitionFixture()
	client := &stubAgentDeploymentClient{
		response: shared.DeploymentsResponse{Deployments: []shared.DeploymentDefinition{definition}},
		result: shared.DeploymentExecutionResult{
			ActionID: definition.ID, StackKey: definition.StackKey, Kind: definition.Kind,
			ReleaseID: definition.ReleaseID, PreviousReleaseID: definition.CurrentReleaseID,
			Summary:           "部署未通过验证，已自动恢复到操作前版本",
			RollbackPerformed: true, ErrorKind: "health_verification_failed",
		},
	}
	server := newTestServer(t, store, "")
	server.deployments = client

	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/deployments?agent=host-a", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"agentVersion":"0.7.0"`) || strings.Contains(response.Body.String(), "runtime-only") || strings.Contains(response.Body.String(), "composeFile") {
		t.Fatalf("deployment list mismatch: HTTP %d %s", response.Code, response.Body.String())
	}

	request := newJSONRequest(http.MethodPost, "/api/deployments/execute", `{"agentId":"host-a","deploymentId":"deploy:gateway:v2","confirmation":"确认部署 Gateway 到 Version 2"}`)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"state":"running"`) || strings.Contains(response.Body.String(), "rollbackPerformed") {
		t.Fatalf("deployment acceptance mismatch: HTTP %d %s", response.Code, response.Body.String())
	}
	operation := waitForDeploymentOperation(t, store, domain.OperationRolledBack)
	if operation.Kind != domain.OperationDeploy || operation.WorkloadKey != "gateway" || operation.Error != "health_verification_failed" || operation.ResultSummary == "" || operation.RequestedBy != "tailnet-operator" {
		t.Fatalf("rolled-back audit mismatch: %+v", operation)
	}
	if client.executions.Load() != 1 {
		t.Fatalf("deployment execution count=%d", client.executions.Load())
	}
	encoded, _ := json.Marshal(operation)
	if strings.Contains(string(encoded), "runtime-only") {
		t.Fatalf("Agent credential entered deployment audit: %s", encoded)
	}
	assertSQLiteFilesExclude(t, databasePath, "runtime-only")
}

func TestDeclarativeDeploymentRequiresAuthenticationCSRFLivePolicyAndConfirmation(t *testing.T) {
	store := NewMemStore()
	if err := store.SetAgents(context.Background(), []AgentConfig{{ID: "host-a", Name: "Host A", URL: "http://agent"}}); err != nil {
		t.Fatal(err)
	}
	definition := hubDeploymentDefinitionFixture()
	client := &stubAgentDeploymentClient{
		response: shared.DeploymentsResponse{Deployments: []shared.DeploymentDefinition{definition}},
		result: shared.DeploymentExecutionResult{
			OK: true, ActionID: definition.ID, StackKey: definition.StackKey, Kind: definition.Kind,
			ReleaseID: definition.ReleaseID, PreviousReleaseID: definition.CurrentReleaseID,
			Summary: "Gateway 已部署 Version 2，健康验证通过",
		},
	}
	server := newTestServer(t, store, "pw")
	server.deployments = client
	body := `{"agentId":"host-a","deploymentId":"deploy:gateway:v2","confirmation":"确认部署 Gateway 到 Version 2"}`

	response := httptest.NewRecorder()
	server.ServeHTTP(response, newJSONRequest(http.MethodPost, "/api/deployments/execute", body))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated deployment returned HTTP %d", response.Code)
	}
	cookie, csrfToken := loginSession(t, server, "pw")
	request := newJSONRequest(http.MethodPost, "/api/deployments/execute", body)
	request.AddCookie(cookie)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("deployment without CSRF returned HTTP %d", response.Code)
	}

	for name, testCase := range map[string]struct {
		body string
		code int
	}{
		"wrong confirmation": {`{"agentId":"host-a","deploymentId":"deploy:gateway:v2","confirmation":"yes"}`, http.StatusUnprocessableEntity},
		"unknown deployment": {`{"agentId":"host-a","deploymentId":"deploy:gateway:v3","confirmation":"确认部署 Gateway 到 Version 3"}`, http.StatusNotFound},
		"caller image":       {`{"agentId":"host-a","deploymentId":"deploy:gateway:v2","confirmation":"确认部署 Gateway 到 Version 2","image":"evil:latest"}`, http.StatusBadRequest},
		"oversized body":     {`{"padding":"` + strings.Repeat("x", maximumDeploymentInputBody) + `"}`, http.StatusRequestEntityTooLarge},
	} {
		t.Run(name, func(t *testing.T) {
			request := newJSONRequest(http.MethodPost, "/api/deployments/execute", testCase.body)
			request.AddCookie(cookie)
			request.Header.Set("X-CSRF-Token", csrfToken)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != testCase.code {
				t.Fatalf("HTTP %d, want %d: %s", response.Code, testCase.code, response.Body.String())
			}
		})
	}
	if operations, err := store.Operations(context.Background(), "", 10); err != nil || len(operations) != 0 || client.executions.Load() != 0 {
		t.Fatalf("rejected deployments created work: operations=%+v executions=%d err=%v", operations, client.executions.Load(), err)
	}

	request = newJSONRequest(http.MethodPost, "/api/deployments/execute", body)
	request.AddCookie(cookie)
	request.Header.Set("X-CSRF-Token", csrfToken)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("approved deployment returned HTTP %d: %s", response.Code, response.Body.String())
	}
	operation := waitForDeploymentOperation(t, store, domain.OperationSucceeded)
	if !strings.HasPrefix(operation.RequestedBy, "session:") || len(operation.RequestedBy) != len("session:")+16 {
		t.Fatalf("pseudonymous deployment requester missing: %+v", operation)
	}
}

func TestDeploymentsAndActionsAreSerializedAtHub(t *testing.T) {
	store := NewMemStore()
	if err := store.SetAgents(context.Background(), []AgentConfig{{ID: "host-a", Name: "Host A", URL: "http://agent"}}); err != nil {
		t.Fatal(err)
	}
	definition := hubDeploymentDefinitionFixture()
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	deploymentClient := &stubAgentDeploymentClient{
		response: shared.DeploymentsResponse{Deployments: []shared.DeploymentDefinition{definition}},
		result: shared.DeploymentExecutionResult{
			OK: true, ActionID: definition.ID, StackKey: definition.StackKey, Kind: definition.Kind,
			ReleaseID: definition.ReleaseID, PreviousReleaseID: definition.CurrentReleaseID, Summary: "部署成功",
		},
		entered: make(chan struct{}, 1), release: release,
	}
	actionDefinition := hubActionDefinitionFixture()
	actionClient := &stubAgentActionClient{response: shared.ActionsResponse{Actions: []shared.ActionDefinition{actionDefinition}}}
	server := newTestServer(t, store, "")
	server.deployments, server.actions = deploymentClient, actionClient

	response := httptest.NewRecorder()
	server.ServeHTTP(response, newJSONRequest(http.MethodPost, "/api/deployments/execute", `{"agentId":"host-a","deploymentId":"deploy:gateway:v2","confirmation":"确认部署 Gateway 到 Version 2"}`))
	if response.Code != http.StatusAccepted {
		t.Fatalf("deployment was not accepted: HTTP %d %s", response.Code, response.Body.String())
	}
	<-deploymentClient.entered
	response = httptest.NewRecorder()
	server.ServeHTTP(response, newJSONRequest(http.MethodPost, "/api/actions/execute", `{"agentId":"host-a","actionId":"restart:systemd:caddy.service","confirmation":"确认重启 Caddy"}`))
	if response.Code != http.StatusConflict || actionClient.executions.Load() != 0 {
		t.Fatalf("action during deployment was not rejected: HTTP %d calls=%d", response.Code, actionClient.executions.Load())
	}
	close(release)
	_ = waitForDeploymentOperation(t, store, domain.OperationSucceeded)
}

func TestDeclarativeDeploymentTransportFailureIsCategorizedAsynchronously(t *testing.T) {
	store := NewMemStore()
	if err := store.SetAgents(context.Background(), []AgentConfig{{ID: "host-a", Name: "Host A", URL: "http://agent"}}); err != nil {
		t.Fatal(err)
	}
	definition := hubDeploymentDefinitionFixture()
	client := &stubAgentDeploymentClient{
		response:   shared.DeploymentsResponse{Deployments: []shared.DeploymentDefinition{definition}},
		executeErr: errors.New("raw password=must-not-leak"),
	}
	server := newTestServer(t, store, "")
	server.deployments = client
	response := httptest.NewRecorder()
	server.ServeHTTP(response, newJSONRequest(http.MethodPost, "/api/deployments/execute", `{"agentId":"host-a","deploymentId":"deploy:gateway:v2","confirmation":"确认部署 Gateway 到 Version 2"}`))
	if response.Code != http.StatusAccepted || strings.Contains(response.Body.String(), "must-not-leak") {
		t.Fatalf("transport failure leaked before finalization: HTTP %d %s", response.Code, response.Body.String())
	}
	operation := waitForDeploymentOperation(t, store, domain.OperationFailed)
	if operation.Error != "agent_unavailable" || strings.Contains(operation.ResultSummary, "must-not-leak") {
		t.Fatalf("transport failure audit mismatch: %+v", operation)
	}
}

func waitForDeploymentOperation(t *testing.T, store Store, state domain.OperationState) domain.Operation {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		operations, err := store.Operations(context.Background(), "host-a", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(operations) > 0 && operations[0].State == state {
			return operations[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	operations, _ := store.Operations(context.Background(), "host-a", 10)
	t.Fatalf("operation did not reach %s: %+v", state, operations)
	return domain.Operation{}
}
