package agent

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// AgentVersion 是 lodge-agent 的语义版本。hub 据此判断兼容性。
const AgentVersion = "0.10.0"

var processOriginsCommand = []string{"/usr/local/bin/lodge-agent", "--collect-process-origins"}
var composeMetadataCommand = []string{"/usr/local/bin/lodge-agent", "--collect-compose-metadata"}
var proxyRoutesCommand = []string{"/usr/local/bin/lodge-agent", "--collect-proxy-routes"}
var sshAuthCommand = []string{"/usr/local/bin/lodge-agent", "--collect-ssh-auth"}
var securityPostureCommand = []string{"/usr/local/bin/lodge-agent", "--collect-security-posture"}
var listActionsCommand = []string{"/usr/local/bin/lodge-agent", "--list-actions"}
var executeActionCommand = []string{"/usr/local/bin/lodge-agent", "--execute-action"}
var listDeploymentsCommand = []string{"/usr/local/bin/lodge-agent", "--list-deployments"}
var executeDeploymentCommand = []string{"/usr/local/bin/lodge-agent", "--execute-deployment"}
var systemdUnitsCommand = []string{
	"systemctl", "show", "--type=service", "--all",
	"--property=Id,LoadState,ActiveState,SubState,FragmentPath",
}

// PrivCommand 描述一条需要经 sudo 以 root 执行的命令。
//
// 这是 sudoers 白名单的单一真相来源：install-agent.sh 调用
// `lodge-agent --print-sudoers`，从这份定义生成 /etc/sudoers.d/lodge-agent。
// 改这里必须重新部署 agent，二者绝不手写分叉。
type PrivCommand struct {
	// Write 为 true 表示有副作用（动作），UI 上对应一个需二次确认的按钮；
	// false 表示纯只读采集。
	Write bool
	// Argv 是固定命令行。注意：
	//   - 第一项是命令短名（docker/ss/...），部署时解析为绝对路径写进 sudoers
	//   - 必须「逐字」与 sudoers 对应 —— agent 内部也是用这同一份 argv 去跑，
	//     所以多一个空格都不行
	Argv []string
	Desc string
}

// privilegedRead 是只读采集命令。安全：只读取，不改变任何状态。
var privilegedRead = []PrivCommand{
	{Argv: []string{"docker", "ps", "--all", "--no-trunc", "--format", "{{json .}}"}, Desc: "列出所有容器（含完整 ID）"},
	{Argv: []string{"docker", "system", "df", "--format", "{{json .}}"}, Desc: "docker 磁盘占用"},
	{Argv: composeMetadataCommand, Desc: "输出校验后的 Compose project/service 身份"},
	{Argv: proxyRoutesCommand, Desc: "输出脱敏的 Caddy/Nginx Web 路由"},
	{Argv: sshAuthCommand, Desc: "输出最近 SSH 认证失败的来源 IP 聚合"},
	{Argv: securityPostureCommand, Desc: "输出脱敏 SSH 与本机防护姿态"},
	{Argv: listActionsCommand, Desc: "列出 root 策略批准的受控动作"},
	{Argv: listDeploymentsCommand, Desc: "列出 root 策略批准的声明式部署"},
	{Argv: []string{"ss", "-tlnpH"}, Desc: "监听套接字（含 PID，需 root 才能跨用户）"},
	{Argv: systemdUnitsCommand, Desc: "读取 systemd service 状态与 unit 来源分类"},
	{Argv: processOriginsCommand, Desc: "输出脱敏进程来源（不含参数、环境变量或完整路径）"},
}

// privilegedWrite 只授权两个固定的 root 策略执行入口。具体目标、动作和不可变
// 发布版本由 root 拥有的策略文件决定；Hub 和 Agent HTTP 请求都不能提交命令、
// 参数、路径、Compose 内容或环境变量。
var privilegedWrite = []PrivCommand{
	{Write: true, Argv: executeActionCommand, Desc: "执行 root 策略批准的单个受控动作"},
	{Write: true, Argv: executeDeploymentCommand, Desc: "执行 root 策略批准的声明式部署"},
}

// AllPrivileged 返回只读 + 写命令的合集，按声明顺序。
func AllPrivileged() []PrivCommand {
	out := make([]PrivCommand, 0, len(privilegedRead)+len(privilegedWrite))
	out = append(out, privilegedRead...)
	out = append(out, privilegedWrite...)
	return out
}

// commandByName 用 argv 前 N 项查找一条已注册命令。
// 用于把运行期 argv 映射回命令定义（动作执行时的校验）。
func commandByName(argv []string) (PrivCommand, bool) {
	for _, c := range AllPrivileged() {
		if argvEqual(c.Argv, argv) {
			return c, true
		}
	}
	return PrivCommand{}, false
}

func argvEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// resolveAbsolute 把每条命令的首项（短名）解析为绝对路径。
// sudoers 要求绝对路径，且必须等于 sudo 经 secure_path 实际解析到的路径，
// 否则匹配失败。找不到的命令报错 —— 宁可安装失败，也不在 sudoers 里留坏路径。
func resolveAbsolute(cmds []PrivCommand) ([][]string, error) {
	resolved := make([][]string, 0, len(cmds))
	for _, c := range cmds {
		if len(c.Argv) == 0 {
			return nil, errors.New("空命令")
		}
		path, err := exec.LookPath(c.Argv[0])
		if err != nil {
			return nil, fmt.Errorf("找不到命令 %q：%w", c.Argv[0], err)
		}
		argv := make([]string, len(c.Argv))
		argv[0] = path
		copy(argv[1:], c.Argv[1:])
		resolved = append(resolved, argv)
	}
	return resolved, nil
}

// escapeSudoersArg 转义单个 sudoers 参数 token。
//
// sudoers 的特殊字符是：反斜杠、逗号（Cmnd_Alias 分隔符）、冒号、井号、空格。
// 其中空格最易踩坑 —— docker 的 `--format {{json .}}` 里 `{{json .}}` 是含空格的
// 单个 argv 元素，若不转义，sudo 会把它拆成 `{{json` 和 `.}}` 两个 token 去匹配，
// 结果永远匹配不上，sudo 静默拒绝。
func escapeSudoersArg(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`,`, `\,`,
		`:`, `\:`,
		`#`, `\#`,
		` `, `\ `,
	)
	return r.Replace(s)
}

// formatSudoersLine 把一条绝对路径 argv 渲染成 sudoers 里的一条命令描述。
func formatSudoersLine(argv []string) string {
	parts := make([]string, len(argv))
	parts[0] = argv[0] // 绝对路径本身不含需转义的字符
	for i := 1; i < len(argv); i++ {
		parts[i] = escapeSudoersArg(argv[i])
	}
	return strings.Join(parts, " ")
}

// GenerateSudoers 把已解析为绝对路径的读写命令渲染成完整的 sudoers 文件内容。
// 直接写 user specification，不定义全局 Alias：这样即使某台旧机器错误地
// 重复 includedir，也不会发生 Alias 重定义并污染每次 sudo 调用。
func GenerateSudoers(read, write [][]string) string {
	var b strings.Builder
	b.WriteString("# /etc/sudoers.d/lodge-agent\n")
	b.WriteString("# 由 `lodge-agent --print-sudoers` 生成，请勿手改。\n")
	b.WriteString("# 与 agent 二进制内部的命令逐字对应，手改会导致 sudo 拒绝执行。\n")
	writeSudoersSpec(&b, "只读采集", read)
	writeSudoersSpec(&b, "受控写操作", write)
	return b.String()
}

func writeSudoersSpec(b *strings.Builder, description string, commands [][]string) {
	if len(commands) == 0 {
		return
	}
	b.WriteString("\n# " + description + "\n")
	b.WriteString("lodge ALL=(root) NOPASSWD: \\\n")
	for index, argv := range commands {
		b.WriteString("    " + formatSudoersLine(argv))
		if index < len(commands)-1 {
			b.WriteString(", \\\n")
		} else {
			b.WriteByte('\n')
		}
	}
}

// GenerateAdminSudoers 给运维账号 lodge-admin 两条精确 helper。
// 不得写入 lodge 的 AllPrivileged，也不得授予 NOPASSWD: ALL。
func GenerateAdminSudoers() string {
	var b strings.Builder
	b.WriteString("# /etc/sudoers.d/lodge-admin-operator\n")
	b.WriteString("# 由 `lodge-agent --print-admin-sudoers` 生成，请勿手改。\n")
	b.WriteString("# lodge 服务账号不得使用此文件。\n")
	b.WriteString("\n# 非 root 用户服务维护\n")
	b.WriteString("lodge-admin ALL=(root) NOPASSWD: \\\n")
	b.WriteString("    " + formatSudoersLine(listOperatorCommand) + ", \\\n")
	b.WriteString("    " + formatSudoersLine(executeOperatorCommand) + "\n")
	return b.String()
}

// PrintAdminSudoers 生成 lodge-admin 的免密 sudoers。
func PrintAdminSudoers() string {
	return GenerateAdminSudoers()
}

// PrintSudoers 解析当前机器上的命令路径并生成 sudoers 内容。
// 安装时由 install-agent.sh 调用：`sudo lodge-agent --print-sudoers > /etc/sudoers.d/lodge-agent`。
func PrintSudoers() (string, error) {
	read, err := resolveAbsolute(privilegedRead)
	if err != nil {
		return "", err
	}
	write, err := resolveAbsolute(privilegedWrite)
	if err != nil {
		return "", err
	}
	return GenerateSudoers(read, write), nil
}
