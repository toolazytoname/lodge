package agent

import (
	"strings"
	"testing"
)

func TestEscapeSudoersArg(t *testing.T) {
	cases := map[string]string{
		"{{json .}}": `{{json\ .}}`, // 含空格的 docker format —— 最易踩的坑
		"plain":      "plain",
		"--format":   "--format",
		"a,b":        `a\,b`,
		`c:\path`:    `c\:\\path`,
		"with#hash":  `with\#hash`,
		`back\slash`: `back\\slash`,
	}
	for in, want := range cases {
		if got := escapeSudoersArg(in); got != want {
			t.Errorf("escapeSudoersArg(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

func TestGenerateSudoers(t *testing.T) {
	read := [][]string{
		{"/usr/bin/docker", "ps", "--all", "--no-trunc", "--format", "{{json .}}"},
		{"/usr/bin/ss", "-tlnpH"},
	}
	write := [][]string{
		{"/usr/local/bin/lodge-agent", "--execute-action"},
	}

	out := GenerateSudoers(read, write)

	// 关键断言：docker format 的空格必须被转义，否则 sudo 会把参数拆成两个 token 匹配
	if !strings.Contains(out, `--format {{json\ .}}`) {
		t.Errorf("sudoers 未正确转义 docker format 空格:\n%s", out)
	}
	if strings.Contains(out, "Cmnd_Alias") {
		t.Errorf("不应定义可能与重复 includedir 冲突的全局别名:\n%s", out)
	}
	if strings.Count(out, "lodge ALL=(root) NOPASSWD:") != 2 {
		t.Errorf("读写命令应分别生成直接授权行:\n%s", out)
	}
	if !strings.Contains(out, "# 只读采集") || !strings.Contains(out, "# 受控写操作") {
		t.Errorf("缺少读写边界注释:\n%s", out)
	}
	// 多条命令之间应有逗号分隔
	if strings.Count(out, "/usr/bin/docker ps") != 1 {
		t.Errorf("docker ps 应只出现一次:\n%s", out)
	}
	// 不能有尾随的续行符（\ 后直接换行再 EOF）
	if strings.HasSuffix(strings.TrimSpace(out), "\\") {
		t.Errorf("文件以续行符结尾:\n%s", out)
	}
}

func TestGenerateSudoersOmitsEmptyCommandClass(t *testing.T) {
	out := GenerateSudoers([][]string{{"/usr/bin/ss", "-tlnpH"}}, nil)
	if strings.Count(out, "lodge ALL=(root) NOPASSWD:") != 1 || strings.Contains(out, "# 受控写操作") {
		t.Fatalf("empty command class generated an authorization spec:\n%s", out)
	}
}

func TestCommandByName(t *testing.T) {
	// 已注册的 docker ps 应命中
	c, ok := commandByName([]string{"docker", "ps", "--all", "--no-trunc", "--format", "{{json .}}"})
	if !ok {
		t.Fatal("已注册命令应命中白名单")
	}
	if c.Write {
		t.Error("docker ps 应是只读命令")
	}

	// 未注册的命令应被拒（任意命令执行防护的核心）
	_, ok = commandByName([]string{"docker", "run", "--privileged", "-v", "/:/host", "alpine"})
	if ok {
		t.Fatal("危险命令 docker run 竟然命中白名单 —— 这是严重的安全漏洞")
	}

	// 多一个参数也不行（argv 必须逐字相等）
	_, ok = commandByName([]string{"docker", "ps", "--all", "--no-trunc", "--format", "{{json .}}", "EXTRA"})
	if ok {
		t.Fatal("命令被追加参数后不应命中白名单")
	}

	// 进程来源 helper 只能按完整 argv 调用；追加任意 flag 都必须被拒绝。
	c, ok = commandByName(processOriginsCommand)
	if !ok || c.Write {
		t.Fatal("脱敏进程来源 helper 应命中只读白名单")
	}
	_, ok = commandByName(append(append([]string{}, processOriginsCommand...), "--token-file", "/tmp/evil"))
	if ok {
		t.Fatal("进程来源 helper 被追加参数后不应命中白名单")
	}
	for _, command := range [][]string{composeMetadataCommand, proxyRoutesCommand, sshAuthCommand, listActionsCommand, listDeploymentsCommand, systemdUnitsCommand} {
		definition, found := commandByName(command)
		if !found || definition.Write {
			t.Fatalf("orchestration metadata command should be read-only: %v", command)
		}
		_, found = commandByName(append(append([]string{}, command...), "EXTRA"))
		if found {
			t.Fatalf("orchestration command with extra args matched allowlist: %v", command)
		}
	}
	for _, command := range [][]string{executeActionCommand, executeDeploymentCommand} {
		definition, found := commandByName(command)
		if !found || !definition.Write {
			t.Fatalf("fixed policy executor should be a write command: %v", command)
		}
		_, found = commandByName(append(append([]string{}, command...), "caller-controlled-id"))
		if found {
			t.Fatalf("policy executor with an argv ID must be rejected: %v", command)
		}
	}
	for _, removed := range [][]string{
		{"docker", "system", "prune", "-f"},
		{"journalctl", "--vacuum-time=7d"},
		{"systemctl", "restart", "caddy"},
	} {
		if _, found := commandByName(removed); found {
			t.Fatalf("legacy direct write command still allowlisted: %v", removed)
		}
	}
}
