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
		{"/usr/bin/docker", "system", "prune", "-f"},
	}

	out := GenerateSudoers(read, write)

	// 关键断言：docker format 的空格必须被转义，否则 sudo 会把参数拆成两个 token 匹配
	if !strings.Contains(out, `--format {{json\ .}}`) {
		t.Errorf("sudoers 未正确转义 docker format 空格:\n%s", out)
	}
	if !strings.Contains(out, "Cmnd_Alias LODGE_READ =") {
		t.Errorf("缺少 LODGE_READ 别名:\n%s", out)
	}
	if !strings.Contains(out, "Cmnd_Alias LODGE_WRITE =") {
		t.Errorf("缺少 LODGE_WRITE 别名:\n%s", out)
	}
	if !strings.Contains(out, "lodge ALL=(root) NOPASSWD: LODGE_READ, LODGE_WRITE") {
		t.Errorf("缺少授权行:\n%s", out)
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
}
