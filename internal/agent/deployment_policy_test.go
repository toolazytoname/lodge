package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/toolazytoname/lodge/internal/shared"
)

func TestDecodeDeploymentPolicyRequiresStatelessImmutableReleases(t *testing.T) {
	policy := deploymentPolicyFixture()
	encoded, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeDeploymentPolicy(encoded)
	if err != nil || len(decoded.Stacks) != 1 || len(decoded.Stacks[0].Releases) != 2 {
		t.Fatalf("valid deployment policy rejected: %+v err=%v", decoded, err)
	}

	for name, mutate := range map[string]func(*deploymentPolicyStack){
		"stateful":      func(stack *deploymentPolicyStack) { stack.Stateless = false },
		"mutable image": func(stack *deploymentPolicyStack) { stack.Releases[0].Image = "example.test/gateway:latest" },
		"duplicate digest": func(stack *deploymentPolicyStack) {
			stack.Releases[1].Image = "mirror.example.test/gateway@sha256:" + strings.Repeat("1", 64)
		},
		"path escape": func(stack *deploymentPolicyStack) { stack.ComposeFile = "/srv/other/compose.yaml" },
		"public health": func(stack *deploymentPolicyStack) {
			stack.Health = deploymentHealthPolicy{Kind: "http", URL: "https://example.test/health", TimeoutSeconds: 30}
		},
		"invalid port": func(stack *deploymentPolicyStack) {
			stack.Health = deploymentHealthPolicy{Kind: "http", URL: "http://127.0.0.1:99999/health", TimeoutSeconds: 30}
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := deploymentPolicyFixture()
			mutate(&candidate.Stacks[0])
			content, _ := json.Marshal(candidate)
			if _, err := decodeDeploymentPolicy(content); err == nil {
				t.Fatal("unsafe deployment policy was accepted")
			}
		})
	}
	if _, err := decodeDeploymentPolicy(append(encoded, []byte(` {}`)...)); err == nil {
		t.Fatal("trailing deployment JSON was accepted")
	}
	if _, err := decodeDeploymentPolicy([]byte(`{"version":1,"stacks":[],"command":"id"}`)); err == nil {
		t.Fatal("command field was accepted in deployment policy")
	}
}

func TestDeploymentDefinitionsExposeOnlyReleaseIdentity(t *testing.T) {
	policy := deploymentPolicyFixture()
	states := map[string]deploymentState{
		"gateway": {
			Version: deploymentStateVersion, StackKey: "gateway",
			Current:  deploymentStateRelease{ID: "v1", Label: "Version 1", Image: policy.Stacks[0].Releases[0].Image},
			Previous: deploymentStateRelease{ID: "external", Label: "Deployment pre-state", Image: immutableImage("0")},
		},
	}
	definitions := deploymentDefinitions(policy, states)
	if len(definitions) != 2 {
		t.Fatalf("definitions=%+v", definitions)
	}
	if definitions[0].Kind != shared.DeploymentDeploy || definitions[0].ReleaseID != "v2" || definitions[0].ID != "deploy:gateway:v2" {
		t.Fatalf("deploy definition mismatch: %+v", definitions[0])
	}
	if definitions[1].Kind != shared.DeploymentRollback || definitions[1].ReleaseID != "external" || definitions[1].ID != "rollback:gateway" {
		t.Fatalf("rollback definition mismatch: %+v", definitions[1])
	}
	encoded, _ := json.Marshal(definitions)
	for _, forbidden := range []string{"projectDirectory", "composeFile", "/srv/gateway", "command", "environment"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("deployment definition leaked host implementation %q: %s", forbidden, encoded)
		}
	}
}

func TestDeploymentWireContractRejectsTampering(t *testing.T) {
	definition := shared.DeploymentDefinition{
		ID: "deploy:gateway:v2", StackKey: "gateway", StackLabel: "Gateway", Kind: shared.DeploymentDeploy,
		ReleaseID: "v2", ReleaseLabel: "Version 2", Image: immutableImage("2"), CurrentReleaseID: "v1",
		Description:  "部署不可变镜像并验证；失败自动回滚：Gateway / Version 2",
		Confirmation: "确认部署 Gateway 到 Version 2", Risk: shared.ActionRiskDisruptive,
	}
	encoded, _ := json.Marshal(shared.DeploymentsResponse{Deployments: []shared.DeploymentDefinition{definition}})
	if _, err := decodeDeploymentsResponse(encoded); err != nil {
		t.Fatalf("valid deployment response rejected: %v", err)
	}
	for name, mutate := range map[string]func(*shared.DeploymentDefinition){
		"identity":      func(item *shared.DeploymentDefinition) { item.ID = "deploy:gateway:other" },
		"mutable image": func(item *shared.DeploymentDefinition) { item.Image = "example.test/gateway:latest" },
		"presentation":  func(item *shared.DeploymentDefinition) { item.Confirmation = "运行调用者命令" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := definition
			mutate(&candidate)
			content, _ := json.Marshal(shared.DeploymentsResponse{Deployments: []shared.DeploymentDefinition{candidate}})
			if _, err := decodeDeploymentsResponse(content); err == nil {
				t.Fatal("tampered deployment definition was accepted")
			}
		})
	}

	result := shared.DeploymentExecutionResult{
		OK: false, ActionID: definition.ID, StackKey: definition.StackKey, Kind: definition.Kind,
		ReleaseID: definition.ReleaseID, PreviousReleaseID: definition.CurrentReleaseID,
		Summary: "部署未通过验证，已自动恢复到操作前版本", RollbackPerformed: true, ErrorKind: "health_verification_failed",
	}
	resultJSON, _ := json.Marshal(result)
	if _, err := decodeDeploymentExecutionResult(resultJSON, definition); err != nil {
		t.Fatalf("valid rolled-back result rejected: %v", err)
	}
	result.ErrorKind = "raw: docker stderr"
	resultJSON, _ = json.Marshal(result)
	if _, err := decodeDeploymentExecutionResult(resultJSON, definition); err == nil {
		t.Fatal("untyped deployment error was accepted")
	}
}

func deploymentPolicyFixture() deploymentPolicy {
	return deploymentPolicy{Version: deploymentPolicyVersion, Stacks: []deploymentPolicyStack{{
		Key: "gateway", Label: "Gateway", ProjectDirectory: "/srv/gateway", ComposeFile: "/srv/gateway/compose.yaml",
		Service: "gateway", Stateless: true,
		Health: deploymentHealthPolicy{Kind: "http", URL: "http://127.0.0.1:8080/health", TimeoutSeconds: 30},
		Releases: []deploymentPolicyRelease{
			{ID: "v1", Label: "Version 1", Image: immutableImage("1")},
			{ID: "v2", Label: "Version 2", Image: immutableImage("2")},
		},
	}}}
}

func immutableImage(digit string) string {
	return "registry.example.test/lodge/gateway@sha256:" + strings.Repeat(digit, 64)
}
