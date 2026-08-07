package agent

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// AgentVersion 是 lodge-agent 的语义版本。hub 据此判断兼容性。
const AgentVersion = "0.5.1"

var processOriginsCommand = []string{"/usr/local/bin/lodge-agent", "--collect-process-origins"}
var composeMetadataCommand = []string{"/usr/local/bin/lodge-agent", "--collect-compose-metadata"}
var proxyRoutesCommand = []string{"/usr/local/bin/lodge-agent", "--collect-proxy-routes"}
var sshAuthCommand = []string{"/usr/local/bin/lodge-agent", "--collect-ssh-auth"}
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
	// ID 是动作的稳定标识（仅 Write 命令需要），用于 POST /v1/actions/{id}。
	// 不参与 sudoers 生成，纯 API 路由用。
	ID string
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
	{Argv: []string{"ss", "-tlnpH"}, Desc: "监听套接字（含 PID，需 root 才能跨用户）"},
	{Argv: systemdUnitsCommand, Desc: "读取 systemd service 状态与 unit 来源分类"},
	{Argv: processOriginsCommand, Desc: "输出脱敏进程来源（不含参数、环境变量或完整路径）"},
}

// privilegedWrite 是有副作用的白名单动作。
//
// 原则：宁可少，不可多。每一条都应是「运维高频且安全边界清晰」的操作。
// 当前清单对应三台目标机器的实际需求：清理 docker 垃圾、回收日志、重启 caddy。
var privilegedWrite = []PrivCommand{
	{Write: true, ID: "docker-prune", Argv: []string{"docker", "system", "prune", "-f"}, Desc: "清理悬空镜像/停止容器/无用网络"},
	{Write: true, ID: "journalctl-vacuum", Argv: []string{"journalctl", "--vacuum-time=7d"}, Desc: "清理 7 天前的 journal 日志"},
	{Write: true, ID: "restart-caddy", Argv: []string{"systemctl", "restart", "caddy"}, Desc: "重启 caddy"},
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

// commandByID 按 ID 查找一条写命令，用于 POST /v1/actions/{id}。
func commandByID(id string) (PrivCommand, bool) {
	for _, c := range privilegedWrite {
		if c.ID == id {
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
