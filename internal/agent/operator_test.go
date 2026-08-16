package agent

import (
	"strconv"
	"strings"
	"testing"
)

func TestDecodeOperatorPolicyAcceptsExplicitEmptyOwners(t *testing.T) {
	policy, err := decodeOperatorPolicy([]byte(`{"version":1,"owners":[]}`))
	if err != nil || len(policy.Owners) != 0 {
		t.Fatalf("empty policy should fail closed: %+v %v", policy, err)
	}
}

func TestDecodeOperatorPolicyAcceptsOptedInOwners(t *testing.T) {
	policy, err := decodeOperatorPolicy([]byte(`{"version":1,"owners":["ecs-user","app-owner"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !ownerApproved(policy, "ecs-user") || ownerApproved(policy, "root") {
		t.Fatalf("owner approval is wrong: %+v", policy)
	}
}

func TestDecodeOperatorPolicyRejectsUnsafeOrAmbiguousInput(t *testing.T) {
	cases := map[string]string{
		"missing owners":  `{"version":1}`,
		"unknown field":   `{"version":1,"owners":[],"command":"id"}`,
		"trailing JSON":   `{"version":1,"owners":[]} {}`,
		"root":            `{"version":1,"owners":["root"]}`,
		"lodge":           `{"version":1,"owners":["lodge"]}`,
		"lodge-admin":     `{"version":1,"owners":["lodge-admin"]}`,
		"nobody":          `{"version":1,"owners":["nobody"]}`,
		"systemd prefix":  `{"version":1,"owners":["systemd-network"]}`,
		"underscore":      `{"version":1,"owners":["_apt"]}`,
		"uppercase":       `{"version":1,"owners":["ECS-User"]}`,
		"path-like":       `{"version":1,"owners":["../root"]}`,
		"duplicate":       `{"version":1,"owners":["ecs-user","ecs-user"]}`,
		"wrong version":   `{"version":2,"owners":["ecs-user"]}`,
		"too many owners": `{"version":1,"owners":[` + quotedOwners(maximumOperatorOwners+1) + `]}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeOperatorPolicy([]byte(content)); err == nil {
				t.Fatal("unsafe policy was accepted")
			}
		})
	}
}

func TestValidateRelativeOwnerPathRejectsEscapeAndCredentials(t *testing.T) {
	if cleaned, err := validateRelativeOwnerPath(".config/mihomo/config.yaml", false); err != nil || cleaned != ".config/mihomo/config.yaml" {
		t.Fatalf("safe service path rejected: %q %v", cleaned, err)
	}
	if cleaned, err := validateRelativeOwnerPath("", true); err != nil || cleaned != "" {
		t.Fatalf("list-dir home should be allowed: %q %v", cleaned, err)
	}
	if cleaned, err := validateRelativeOwnerPath("../etc/passwd", false); err != nil || cleaned != "etc/passwd" {
		t.Fatalf("leading .. should stay under home after clean: %q %v", cleaned, err)
	}
	for _, rel := range []string{
		"/etc/passwd",
		"..",
		".ssh/id_ed25519",
		".gnupg/trustdb.gpg",
		".aws/credentials",
		".config/gcloud/credentials.db",
		".netrc",
		"id_rsa",
		"foo/authorized_keys",
		`foo\bar`,
		"foo\nbar",
	} {
		if _, err := validateRelativeOwnerPath(rel, false); err == nil {
			t.Fatalf("unsafe path accepted: %q", rel)
		}
	}
	if _, err := validateRelativeOwnerPath("", false); err == nil {
		t.Fatal("empty path should be rejected for file operations")
	}
}

func TestValidateOperatorRequestAcceptsBoundedOps(t *testing.T) {
	request, err := decodeOperatorRequest(strings.NewReader(`{"owner":"ecs-user","op":"read-file","path":".config/mihomo/config.yaml"}`))
	if err != nil || request.Path != ".config/mihomo/config.yaml" {
		t.Fatalf("valid read rejected: %+v %v", request, err)
	}
	request, err = decodeOperatorRequest(strings.NewReader(`{"owner":"ecs-user","op":"list-dir","path":".config/mihomo"}`))
	if err != nil || request.Path != ".config/mihomo" {
		t.Fatalf("valid list rejected: %+v %v", request, err)
	}
	request, err = decodeOperatorRequest(strings.NewReader(`{"owner":"ecs-user","op":"unit-restart","unit":"mihomo.service"}`))
	if err != nil || request.Unit != "mihomo.service" {
		t.Fatalf("valid unit restart rejected: %+v %v", request, err)
	}
}

func TestValidateOperatorRequestRejectsTampering(t *testing.T) {
	cases := []string{
		`{"owner":"ecs-user","op":"exec","path":".config/mihomo/config.yaml"}`,
		`{"owner":"ecs-user","op":"read-file","path":".config/mihomo/config.yaml","command":"id"}`,
		`{"owner":"root","op":"read-file","path":".config/mihomo/config.yaml"}`,
		`{"owner":"ecs-user","op":"read-file","path":".ssh/config"}`,
		`{"owner":"ecs-user","op":"write-file","path":".config/mihomo/config.yaml","unit":"mihomo.service"}`,
		`{"owner":"ecs-user","op":"unit-restart","unit":"sshd.service"}`,
		`{"owner":"ecs-user","op":"unit-restart","unit":"lodge-agent.service"}`,
		`{"owner":"ecs-user","op":"unit-restart","unit":"systemd-logind.service"}`,
		`{"owner":"ecs-user","op":"unit-restart","unit":"../sshd.service"}`,
		`{"owner":"ecs-user","op":"unit-status","unit":"mihomo.service","path":".config"}`,
		`{"owner":"ecs-user","op":"write-file","path":".config/mihomo/config.yaml","sha256":"ZZ"}`,
		`{"owner":"ecs-user","op":"read-file"}`,
		`{"owner":"ecs-user","op":"read-file","path":".config/mihomo/config.yaml"} {}`,
	}
	for _, content := range cases {
		if _, err := decodeOperatorRequest(strings.NewReader(content)); err == nil {
			t.Fatalf("unsafe request accepted: %s", content)
		}
	}
}

func TestDeniedSystemUnitCoversCriticalServices(t *testing.T) {
	for _, unit := range []string{
		"sshd.service", "docker.service", "lodge-agent.service",
		"user@1000.service", "systemd-networkd.service", "getty@tty1.service",
	} {
		if !deniedSystemUnit(unit) {
			t.Fatalf("critical unit was not denied: %s", unit)
		}
	}
	if deniedSystemUnit("mihomo.service") || deniedSystemUnit("happy.service") {
		t.Fatal("owner service units should not be denied by name")
	}
}

func TestApprovedOwnerHomeRejectsSystemPaths(t *testing.T) {
	if !approvedOwnerHome("/home/ecs-user") || !approvedOwnerHome("/export/home/app") {
		t.Fatal("normal login homes should be allowed")
	}
	for _, home := range []string{"/", "/root", "/nonexistent", "/tmp/x", "/var/lib/lodge", "/etc/lodge-agent", "relative"} {
		if approvedOwnerHome(home) {
			t.Fatalf("system home accepted: %s", home)
		}
	}
}

func TestOperatorCommandsStayOffLodgeAllowlist(t *testing.T) {
	for _, command := range [][]string{listOperatorCommand, executeOperatorCommand} {
		if _, found := commandByName(command); found {
			t.Fatalf("operator helper leaked into lodge privileged commands: %v", command)
		}
	}
}

func quotedOwners(count int) string {
	parts := make([]string, 0, count)
	for index := 0; index < count; index++ {
		parts = append(parts, `"owner`+strconv.Itoa(index)+`"`)
	}
	return strings.Join(parts, ",")
}
