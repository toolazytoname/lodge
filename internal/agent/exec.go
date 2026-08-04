package agent

import (
	"bytes"
	"errors"
	"os/exec"
)

// runPrivileged 执行一条已注册的白名单命令，经 sudo -n 以 root 运行。
//
// 安全要点：
//   - argv 来自 commands.go 的固定定义，不经任何 shell，无注入面；
//   - sudo -n（non-interactive）：sudoers 没配好就立即失败，绝不卡在密码提示；
//   - sudo -- 之后才是真正的命令，防止 argv 里的 - 参数被 sudo 当成自己的选项。
//
// 校验：传入的 argv 必须命中白名单，否则拒绝 —— 这是「agent 不支持任意命令」
// 这一硬约束的执行点。
func runPrivileged(argv []string) (stdout []byte, stderr []byte, err error) {
	if _, ok := commandByName(argv); !ok {
		return nil, nil, errors.New("命令不在 sudoers 白名单内，拒绝执行: " + joinArgv(argv))
	}
	cmd := exec.Command("sudo", append([]string{"-n", "--"}, argv...)...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if runErr := cmd.Run(); runErr != nil {
		return out.Bytes(), errb.Bytes(), &sudoError{argv: argv, err: runErr, stderr: errb.String()}
	}
	return out.Bytes(), errb.Bytes(), nil
}

// sudoError 带上 stderr，方便 hub 把「为什么失败」透传给用户，而不是只看到一个 exit code。
type sudoError struct {
	argv   []string
	err    error
	stderr string
}

func (e *sudoError) Error() string {
	if e.stderr != "" {
		return e.err.Error() + ": " + firstLine(e.stderr)
	}
	return e.err.Error()
}

func (e *sudoError) Unwrap() error { return e.err }

func joinArgv(argv []string) string {
	out := ""
	for i, a := range argv {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
