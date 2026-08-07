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

func TestAgentDeploymentClientListsAndExecutesWithoutRetry(t *testing.T) {
	definition := hubDeploymentDefinitionFixture()
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer secret" || request.Header.Get("Accept") != "application/json" {
			t.Fatal("Agent deployment request headers are missing")
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v1/deployments":
			_ = json.NewEncoder(writer).Encode(shared.DeploymentsResponse{Deployments: []shared.DeploymentDefinition{definition}})
		case request.Method == http.MethodPost && request.URL.Path == "/v1/deployments/"+definition.ID:
			posts.Add(1)
			writer.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(writer).Encode(shared.DeploymentExecutionResult{
				ActionID: definition.ID, StackKey: definition.StackKey, Kind: definition.Kind,
				ReleaseID: definition.ReleaseID, PreviousReleaseID: definition.CurrentReleaseID,
				Summary:           "部署未通过验证，已自动恢复到操作前版本",
				RollbackPerformed: true, ErrorKind: "health_verification_failed",
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := newHTTPAgentDeploymentClient()
	agent := AgentConfig{URL: server.URL, Token: "secret"}
	listed, err := client.List(context.Background(), agent)
	if err != nil || len(listed.Deployments) != 1 || listed.Deployments[0].ID != definition.ID {
		t.Fatalf("deployment list=%+v err=%v", listed, err)
	}
	result, err := client.Execute(context.Background(), agent, definition)
	if err != nil || result.OK || !result.RollbackPerformed || result.ErrorKind != "health_verification_failed" || posts.Load() != 1 {
		t.Fatalf("deployment result=%+v posts=%d err=%v", result, posts.Load(), err)
	}
}

func TestAgentDeploymentClientRejectsTamperedAuthorityAndResult(t *testing.T) {
	definition := hubDeploymentDefinitionFixture()
	for name, handler := range map[string]http.HandlerFunc{
		"caller command": func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte(`{"deployments":[{"id":"deploy:gateway:v2","stackKey":"gateway","stackLabel":"Gateway","kind":"deploy","releaseId":"v2","releaseLabel":"Version 2","image":"registry.example.test/lodge/gateway@sha256:` + strings.Repeat("2", 64) + `","description":"x","confirmation":"x","risk":"disruptive","command":"id"}]}`))
		},
		"mutable image": func(writer http.ResponseWriter, _ *http.Request) {
			candidate := definition
			candidate.Image = "registry.example.test/lodge/gateway:latest"
			_ = json.NewEncoder(writer).Encode(shared.DeploymentsResponse{Deployments: []shared.DeploymentDefinition{candidate}})
		},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(handler)
			defer server.Close()
			if _, err := newHTTPAgentDeploymentClient().List(context.Background(), AgentConfig{URL: server.URL}); err == nil || actionClientErrorCategory(err) != "agent_invalid_response" {
				t.Fatalf("tampered authority error=%v", err)
			}
		})
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(writer).Encode(shared.DeploymentExecutionResult{
			ActionID: definition.ID, StackKey: definition.StackKey, Kind: definition.Kind,
			ReleaseID: definition.ReleaseID, PreviousReleaseID: definition.CurrentReleaseID,
			RollbackPerformed: true, ErrorKind: "raw_docker_stderr",
		})
	}))
	defer server.Close()
	if _, err := newHTTPAgentDeploymentClient().Execute(context.Background(), AgentConfig{URL: server.URL}, definition); err == nil || actionClientErrorCategory(err) != "agent_invalid_response" {
		t.Fatalf("tampered deployment result error=%v", err)
	}
}

func hubDeploymentDefinitionFixture() shared.DeploymentDefinition {
	return shared.DeploymentDefinition{
		ID: "deploy:gateway:v2", StackKey: "gateway", StackLabel: "Gateway", Kind: shared.DeploymentDeploy,
		ReleaseID: "v2", ReleaseLabel: "Version 2",
		Image:            "registry.example.test/lodge/gateway@sha256:" + strings.Repeat("2", 64),
		CurrentReleaseID: "v1", PreviousReleaseID: "external",
		Description:  "部署不可变镜像并验证；失败自动回滚：Gateway / Version 2",
		Confirmation: "确认部署 Gateway 到 Version 2", Risk: shared.ActionRiskDisruptive,
	}
}
