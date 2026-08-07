package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/toolazytoname/lodge/internal/shared"
)

func TestDecodeActionPolicyBuildsTypedDefinitions(t *testing.T) {
	policy, err := decodeActionPolicy([]byte(`{
		"version":1,
		"targets":[
			{"key":"systemd:caddy.service","label":"Caddy","kind":"systemd","resource":"caddy.service","actions":["restart","logs"]},
			{"key":"docker:api","label":"API","kind":"docker","resource":"api","actions":["start","stop"]}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	definitions := actionDefinitions(policy)
	if len(definitions) != 4 {
		t.Fatalf("got %d definitions, want 4", len(definitions))
	}
	logs, logsFound := findActionDefinition(definitions, "logs:systemd:caddy.service")
	restart, restartFound := findActionDefinition(definitions, "restart:systemd:caddy.service")
	if !logsFound || logs.Risk != shared.ActionRiskRead || !restartFound || restart.Confirmation != "确认重启 Caddy" {
		t.Fatalf("definitions should be stable and risk typed: %+v", definitions)
	}
	if definitions[2].ID != logs.ID || definitions[3].ID != restart.ID {
		t.Fatalf("actions for the same label should use safe stable ordering: %+v", definitions)
	}
	if target, definition, ok := approvedAction(policy, "stop:docker:api"); !ok || target.Resource != "api" || definition.Risk != shared.ActionRiskDisruptive {
		t.Fatalf("approved action lookup failed: %+v %+v %v", target, definition, ok)
	}
}

func TestDecodeActionPolicyAcceptsExplicitEmptyTargets(t *testing.T) {
	policy, err := decodeActionPolicy([]byte(`{"version":1,"targets":[]}`))
	if err != nil || len(actionDefinitions(policy)) != 0 {
		t.Fatalf("explicit empty policy should disable actions safely: policy=%+v err=%v", policy, err)
	}
}

func TestDecodeActionPolicyRejectsUnsafeOrAmbiguousInput(t *testing.T) {
	cases := map[string]string{
		"missing targets":       `{"version":1}`,
		"unknown field":         `{"version":1,"targets":[],"command":"id"}`,
		"trailing JSON":         `{"version":1,"targets":[]} {}`,
		"path-like systemd":     `{"version":1,"targets":[{"key":"systemd:../caddy.service","label":"Caddy","kind":"systemd","resource":"../caddy.service","actions":["restart"]}]}`,
		"flag-like docker":      `{"version":1,"targets":[{"key":"docker:--privileged","label":"bad","kind":"docker","resource":"--privileged","actions":["start"]}]}`,
		"identity mismatch":     `{"version":1,"targets":[{"key":"docker:other","label":"API","kind":"docker","resource":"api","actions":["start"]}]}`,
		"duplicate target":      `{"version":1,"targets":[{"key":"docker:api","label":"API","kind":"docker","resource":"api","actions":["start"]},{"key":"docker:api","label":"API 2","kind":"docker","resource":"api","actions":["stop"]}]}`,
		"duplicate action":      `{"version":1,"targets":[{"key":"docker:api","label":"API","kind":"docker","resource":"api","actions":["start","start"]}]}`,
		"control in label":      "{\"version\":1,\"targets\":[{\"key\":\"docker:api\",\"label\":\"API\\nservice\",\"kind\":\"docker\",\"resource\":\"api\",\"actions\":[\"start\"]}]}",
		"unsupported operation": `{"version":1,"targets":[{"key":"docker:api","label":"API","kind":"docker","resource":"api","actions":["exec"]}]}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeActionPolicy([]byte(content)); err == nil {
				t.Fatal("unsafe policy was accepted")
			}
		})
	}
}

func TestActionBoundaryDecodersRejectTampering(t *testing.T) {
	definition := actionResponseFixture().Actions[0]
	valid, err := json.Marshal(shared.ActionsResponse{Actions: []shared.ActionDefinition{definition}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeActionsResponse(valid); err != nil {
		t.Fatalf("valid definitions rejected: %v", err)
	}

	tampered := definition
	tampered.Confirmation = "确认执行任意命令"
	content, _ := json.Marshal(shared.ActionsResponse{Actions: []shared.ActionDefinition{tampered}})
	if _, err := decodeActionsResponse(content); err == nil {
		t.Fatal("tampered presentation was accepted")
	}
	if _, err := decodeActionsResponse(append(valid, []byte(` {}`)...)); err == nil {
		t.Fatal("trailing response JSON was accepted")
	}

	result := shared.ActionExecutionResult{
		OK: true, ActionID: definition.ID, TargetKey: definition.TargetKey,
		Kind: definition.Kind, Summary: "返回 1 行有界脱敏日志", Logs: []string{"safe"},
	}
	encodedResult, _ := json.Marshal(result)
	if _, err := decodeActionExecutionResult(encodedResult, definition); err != nil {
		t.Fatalf("valid result rejected: %v", err)
	}
	result.ActionID = "logs:docker:other"
	encodedResult, _ = json.Marshal(result)
	if _, err := decodeActionExecutionResult(encodedResult, definition); err == nil {
		t.Fatal("mismatched result identity was accepted")
	}
	result.ActionID = definition.ID
	result.Logs = []string{"line\nsmuggling"}
	encodedResult, _ = json.Marshal(result)
	if _, err := decodeActionExecutionResult(encodedResult, definition); err == nil {
		t.Fatal("multiline log item was accepted")
	}
}

func TestSanitizeActionLogLinesRedactsAndBounds(t *testing.T) {
	var input strings.Builder
	input.WriteString("password=\"secret with spaces\" Bearer abcdefghijkl https://user:pass@example.test/a\n")
	for index := 0; index < maximumActionLogLines+5; index++ {
		input.WriteString("safe line\n")
	}
	lines := sanitizeActionLogLines([]byte(input.String()))
	if len(lines) != maximumActionLogLines {
		t.Fatalf("got %d log lines, want %d", len(lines), maximumActionLogLines)
	}
	redacted := sanitizeActionLogLine("password='secret with spaces' Bearer abcdefghijkl https://user:pass@example.test/a")
	if strings.Contains(redacted, "secret with spaces") || strings.Contains(redacted, "abcdefghijkl") || strings.Contains(redacted, "user:pass") {
		t.Fatalf("secret-like values leaked: %q", redacted)
	}
	if !strings.Contains(redacted, "[REDACTED]") {
		t.Fatalf("redaction marker missing: %q", redacted)
	}
}
