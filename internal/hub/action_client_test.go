package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/toolazytoname/lodge/internal/shared"
)

func TestHTTPAgentActionClientUsesBearerAndNeverSendsCommands(t *testing.T) {
	definition := hubActionDefinitionFixture()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer agent-secret" {
			t.Fatal("missing Agent bearer authentication")
		}
		switch request.URL.Path {
		case "/v1/actions":
			if request.Method != http.MethodGet {
				t.Fatalf("action list method = %s", request.Method)
			}
			_ = json.NewEncoder(writer).Encode(shared.ActionsResponse{Actions: []shared.ActionDefinition{definition}})
		case "/v1/actions/restart:systemd:caddy.service":
			if request.Method != http.MethodPost || request.ContentLength > 0 || request.URL.RawQuery != "" {
				t.Fatalf("execution must be empty POST with no query: method=%s length=%d query=%q", request.Method, request.ContentLength, request.URL.RawQuery)
			}
			_ = json.NewEncoder(writer).Encode(shared.ActionExecutionResult{
				OK: true, ActionID: definition.ID, TargetKey: definition.TargetKey, Kind: definition.Kind,
				StateBefore: "running", StateAfter: "running", Summary: "Caddy：running → running",
			})
		default:
			t.Fatalf("unexpected Agent action path %q", request.URL.Path)
		}
	}))
	defer server.Close()
	client := newHTTPAgentActionClient()
	agent := AgentConfig{URL: server.URL, Token: "agent-secret"}
	response, err := client.List(context.Background(), agent)
	if err != nil || len(response.Actions) != 1 || response.Actions[0].ID != definition.ID {
		t.Fatalf("action list mismatch: %+v err=%v", response, err)
	}
	result, err := client.Execute(context.Background(), agent, definition)
	if err != nil || !result.OK || result.StateAfter != "running" {
		t.Fatalf("action result mismatch: %+v err=%v", result, err)
	}
}

func TestHTTPAgentActionClientDoesNotRetryPOST(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte(`{"error":"raw remote detail"}`))
	}))
	defer server.Close()
	client := newHTTPAgentActionClient()
	_, err := client.Execute(context.Background(), AgentConfig{URL: server.URL, Token: "secret"}, hubActionDefinitionFixture())
	if err == nil || calls.Load() != 1 || strings.Contains(err.Error(), "raw remote detail") {
		t.Fatalf("non-idempotent POST retry or detail leak: calls=%d err=%v", calls.Load(), err)
	}
}

func TestHTTPAgentActionClientRejectsRedirectAndTamperedPayload(t *testing.T) {
	definition := hubActionDefinitionFixture()
	for name, handler := range map[string]http.HandlerFunc{
		"redirect": func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Location", "http://example.test/")
			writer.WriteHeader(http.StatusFound)
		},
		"unknown field": func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte(`{"actions":[],"command":"id"}`))
		},
		"tampered identity": func(writer http.ResponseWriter, _ *http.Request) {
			tampered := definition
			tampered.TargetKey = "systemd:ssh.service"
			_ = json.NewEncoder(writer).Encode(shared.ActionsResponse{Actions: []shared.ActionDefinition{tampered}})
		},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(handler)
			defer server.Close()
			client := newHTTPAgentActionClient()
			if _, err := client.List(context.Background(), AgentConfig{URL: server.URL, Token: "secret"}); actionClientErrorCategory(err) == "" || err == nil {
				t.Fatalf("unsafe Agent response accepted: %v", err)
			}
		})
	}
}

func TestValidateAgentActionResultBoundsTransientLogs(t *testing.T) {
	definition := hubActionDefinitionFixture()
	definition.ID, definition.Kind, definition.Risk = "logs:systemd:caddy.service", shared.ActionLogs, shared.ActionRiskRead
	result := shared.ActionExecutionResult{
		OK: true, ActionID: definition.ID, TargetKey: definition.TargetKey, Kind: definition.Kind,
		Summary: "返回 1 行有界脱敏日志", Logs: []string{"safe line"},
	}
	if err := validateAgentActionResult(result, definition); err != nil {
		t.Fatalf("valid bounded logs rejected: %v", err)
	}
	result.Logs = []string{"line\nsmuggling"}
	if err := validateAgentActionResult(result, definition); err == nil {
		t.Fatal("multiline log item was accepted")
	}
	result.Logs = make([]string, 201)
	for index := range result.Logs {
		result.Logs[index] = "safe"
	}
	if err := validateAgentActionResult(result, definition); err == nil {
		t.Fatal("over-limit log result was accepted")
	}
}

func hubActionDefinitionFixture() shared.ActionDefinition {
	return shared.ActionDefinition{
		ID: "restart:systemd:caddy.service", TargetKey: "systemd:caddy.service", TargetLabel: "Caddy",
		TargetKind: shared.ActionTargetSystemd, Kind: shared.ActionRestart,
		Description: "重启并验证恢复运行：Caddy", Confirmation: "确认重启 Caddy", Risk: shared.ActionRiskDisruptive,
	}
}
