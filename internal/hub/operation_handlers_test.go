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

type stubAgentActionClient struct {
	response   shared.ActionsResponse
	result     shared.ActionExecutionResult
	listErr    error
	executeErr error
	onList     func()
	entered    chan struct{}
	release    chan struct{}
	executions atomic.Int32
}

func (client *stubAgentActionClient) List(context.Context, AgentConfig) (shared.ActionsResponse, error) {
	if client.onList != nil {
		client.onList()
	}
	return client.response, client.listErr
}

func (client *stubAgentActionClient) Execute(ctx context.Context, _ AgentConfig, _ shared.ActionDefinition) (shared.ActionExecutionResult, error) {
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
			return shared.ActionExecutionResult{}, ctx.Err()
		}
	}
	return client.result, client.executeErr
}

func TestControlledActionAPIExecutesAndAuditsWithoutPersistingLogs(t *testing.T) {
	const transientSecret = "workload-log-secret-must-not-reach-sqlite-28f4"
	agents := []AgentConfig{{ID: "host-a", Name: "Host A", URL: "http://agent", Token: "runtime-only"}}
	store, databasePath := openTestSQLiteStore(t, agents)
	if err := store.Update(context.Background(), "host-a", true, "", shared.Ping{AgentVer: "0.6.0"}, &shared.Status{}, []shared.Service{}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	definition := hubActionDefinitionFixture()
	definition.ID, definition.Kind, definition.Risk = "logs:systemd:caddy.service", shared.ActionLogs, shared.ActionRiskRead
	definition.Description, definition.Confirmation = "读取最多 200 行脱敏日志：Caddy", "确认读取日志 Caddy"
	client := &stubAgentActionClient{
		response: shared.ActionsResponse{Actions: []shared.ActionDefinition{definition}},
		result: shared.ActionExecutionResult{
			OK: true, ActionID: definition.ID, TargetKey: definition.TargetKey, Kind: definition.Kind,
			Summary: "返回 2 行有界脱敏日志", Logs: []string{"safe line", transientSecret},
		},
	}
	server := newTestServer(t, store, "")
	server.actions = client
	fixedNow := time.Date(2026, 8, 8, 2, 3, 4, 0, time.UTC)
	server.now = func() time.Time { return fixedNow }

	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/actions?agent=host-a", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"agentVersion":"0.6.0"`) || strings.Contains(response.Body.String(), "runtime-only") {
		t.Fatalf("action list mismatch: HTTP %d %s", response.Code, response.Body.String())
	}

	request := newJSONRequest(http.MethodPost, "/api/actions/execute", `{"agentId":"host-a","actionId":"logs:systemd:caddy.service","confirmation":"确认读取日志 Caddy"}`)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), transientSecret) {
		t.Fatalf("action execution response mismatch: HTTP %d %s", response.Code, response.Body.String())
	}
	var executed ActionExecutionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &executed); err != nil {
		t.Fatal(err)
	}
	if executed.Operation.State != domain.OperationSucceeded || executed.Operation.Kind != domain.OperationLogs || executed.Operation.RequestedBy != "tailnet-operator" || executed.Result == nil || len(executed.Result.Logs) != 2 {
		t.Fatalf("typed action response mismatch: %+v", executed)
	}

	operations, err := store.Operations(context.Background(), "host-a", 10)
	if err != nil || len(operations) != 1 || operations[0].State != domain.OperationSucceeded || operations[0].ResultSummary != "返回 2 行有界脱敏日志" {
		t.Fatalf("durable operation mismatch: %+v err=%v", operations, err)
	}
	encodedAudit, _ := json.Marshal(operations)
	if strings.Contains(string(encodedAudit), transientSecret) || strings.Contains(string(encodedAudit), "runtime-only") {
		t.Fatalf("transient log or Agent credential entered operation audit: %s", encodedAudit)
	}
	assertSQLiteFilesExclude(t, databasePath, transientSecret)

	response = httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/operations?agent=host-a&limit=10", nil))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), transientSecret) || !strings.Contains(response.Body.String(), `"state":"succeeded"`) {
		t.Fatalf("operation timeline mismatch: HTTP %d %s", response.Code, response.Body.String())
	}
}

func TestControlledActionRequiresAuthenticationCSRFLivePolicyAndConfirmation(t *testing.T) {
	store := NewMemStore()
	if err := store.SetAgents(context.Background(), []AgentConfig{{ID: "host-a", Name: "Host A", URL: "http://agent", Token: "secret"}}); err != nil {
		t.Fatal(err)
	}
	definition := hubActionDefinitionFixture()
	client := &stubAgentActionClient{
		response: shared.ActionsResponse{Actions: []shared.ActionDefinition{definition}},
		result: shared.ActionExecutionResult{
			OK: true, ActionID: definition.ID, TargetKey: definition.TargetKey, Kind: definition.Kind,
			StateBefore: "running", StateAfter: "running", Summary: "Caddy：running → running",
		},
	}
	server := newTestServer(t, store, "pw")
	server.actions = client
	body := `{"agentId":"host-a","actionId":"restart:systemd:caddy.service","confirmation":"确认重启 Caddy"}`

	response := httptest.NewRecorder()
	server.ServeHTTP(response, newJSONRequest(http.MethodPost, "/api/actions/execute", body))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated action returned HTTP %d", response.Code)
	}
	cookie, csrfToken := loginSession(t, server, "pw")
	request := httptest.NewRequest(http.MethodGet, "/api/actions?agent=host-a&agent=host-a", nil)
	request.AddCookie(cookie)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("duplicate Agent query returned HTTP %d", response.Code)
	}
	request = newJSONRequest(http.MethodPost, "/api/actions/execute", body)
	request.AddCookie(cookie)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("action without CSRF returned HTTP %d", response.Code)
	}

	for name, testCase := range map[string]struct {
		body string
		code int
	}{
		"wrong confirmation": {`{"agentId":"host-a","actionId":"restart:systemd:caddy.service","confirmation":"yes"}`, http.StatusUnprocessableEntity},
		"unknown action":     {`{"agentId":"host-a","actionId":"stop:systemd:caddy.service","confirmation":"确认停止 Caddy"}`, http.StatusNotFound},
		"unknown JSON field": {`{"agentId":"host-a","actionId":"restart:systemd:caddy.service","confirmation":"确认重启 Caddy","command":"id"}`, http.StatusBadRequest},
		"oversized body":     {`{"padding":"` + strings.Repeat("x", maximumActionInputBody) + `"}`, http.StatusRequestEntityTooLarge},
	} {
		t.Run(name, func(t *testing.T) {
			request := newJSONRequest(http.MethodPost, "/api/actions/execute", testCase.body)
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
		t.Fatalf("rejected requests created audit or execution: operations=%+v calls=%d err=%v", operations, client.executions.Load(), err)
	}

	request = newJSONRequest(http.MethodPost, "/api/actions/execute", body)
	request.AddCookie(cookie)
	request.Header.Set("X-CSRF-Token", csrfToken)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK || client.executions.Load() != 1 {
		t.Fatalf("approved action failed: HTTP %d calls=%d body=%s", response.Code, client.executions.Load(), response.Body.String())
	}
	var result ActionExecutionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || !strings.HasPrefix(result.Operation.RequestedBy, "session:") || len(result.Operation.RequestedBy) != len("session:")+16 {
		t.Fatalf("pseudonymous requester missing: %+v err=%v", result, err)
	}
}

func TestControlledActionFailureIsCategorizedAndAudited(t *testing.T) {
	store := NewMemStore()
	if err := store.SetAgents(context.Background(), []AgentConfig{{ID: "host-a", Name: "Host A", URL: "http://agent"}}); err != nil {
		t.Fatal(err)
	}
	definition := hubActionDefinitionFixture()
	client := &stubAgentActionClient{
		response:   shared.ActionsResponse{Actions: []shared.ActionDefinition{definition}},
		executeErr: errors.New("raw transport password=must-not-leak"),
	}
	server := newTestServer(t, store, "")
	server.actions = client
	request := newJSONRequest(http.MethodPost, "/api/actions/execute", `{"agentId":"host-a","actionId":"restart:systemd:caddy.service","confirmation":"确认重启 Caddy"}`)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "must-not-leak") || !strings.Contains(response.Body.String(), "agent_unavailable") {
		t.Fatalf("transport failure leaked or misclassified: HTTP %d %s", response.Code, response.Body.String())
	}
	operations, err := store.Operations(context.Background(), "", 10)
	if err != nil || len(operations) != 1 || operations[0].State != domain.OperationFailed || operations[0].Error != "agent_unavailable" {
		t.Fatalf("failed operation audit mismatch: %+v err=%v", operations, err)
	}
}

func TestControlledActionFinalizesAfterBrowserCancellation(t *testing.T) {
	store := NewMemStore()
	if err := store.SetAgents(context.Background(), []AgentConfig{{ID: "host-a", Name: "Host A", URL: "http://agent"}}); err != nil {
		t.Fatal(err)
	}
	definition := hubActionDefinitionFixture()
	requestContext, cancelRequest := context.WithCancel(context.Background())
	client := &stubAgentActionClient{
		response: shared.ActionsResponse{Actions: []shared.ActionDefinition{definition}},
		result: shared.ActionExecutionResult{
			OK: true, ActionID: definition.ID, TargetKey: definition.TargetKey, Kind: definition.Kind,
			StateBefore: "running", StateAfter: "running", Summary: "Caddy：running → running",
		},
		onList: cancelRequest,
	}
	server := newTestServer(t, store, "")
	server.actions = client
	request := newJSONRequest(http.MethodPost, "/api/actions/execute", `{"agentId":"host-a","actionId":"restart:systemd:caddy.service","confirmation":"确认重启 Caddy"}`).WithContext(requestContext)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK || client.executions.Load() != 1 {
		t.Fatalf("cancelled browser abandoned admitted action: HTTP %d calls=%d body=%s", response.Code, client.executions.Load(), response.Body.String())
	}
	operations, err := store.Operations(context.Background(), "host-a", 10)
	if err != nil || len(operations) != 1 || operations[0].State != domain.OperationSucceeded {
		t.Fatalf("cancelled browser left incomplete audit: operations=%+v err=%v", operations, err)
	}
}

func TestControlledActionsAreSerializedAtHub(t *testing.T) {
	store := NewMemStore()
	if err := store.SetAgents(context.Background(), []AgentConfig{{ID: "host-a", Name: "Host A", URL: "http://agent"}}); err != nil {
		t.Fatal(err)
	}
	definition := hubActionDefinitionFixture()
	client := &stubAgentActionClient{
		response: shared.ActionsResponse{Actions: []shared.ActionDefinition{definition}},
		result: shared.ActionExecutionResult{
			OK: true, ActionID: definition.ID, TargetKey: definition.TargetKey, Kind: definition.Kind,
			StateBefore: "running", StateAfter: "running", Summary: "Caddy：running → running",
		},
		entered: make(chan struct{}, 1), release: make(chan struct{}),
	}
	server := newTestServer(t, store, "")
	server.actions = client
	newRequest := func() *http.Request {
		return newJSONRequest(http.MethodPost, "/api/actions/execute", `{"agentId":"host-a","actionId":"restart:systemd:caddy.service","confirmation":"确认重启 Caddy"}`)
	}
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, newRequest())
		firstDone <- response
	}()
	<-client.entered
	second := httptest.NewRecorder()
	server.ServeHTTP(second, newRequest())
	if second.Code != http.StatusConflict || client.executions.Load() != 1 {
		t.Fatalf("concurrent action was not rejected: HTTP %d calls=%d", second.Code, client.executions.Load())
	}
	close(client.release)
	if first := <-firstDone; first.Code != http.StatusOK {
		t.Fatalf("first action failed: HTTP %d %s", first.Code, first.Body.String())
	}
}
