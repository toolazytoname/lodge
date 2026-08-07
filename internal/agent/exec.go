package agent

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
)

const (
	maxPrivilegedStdin  = 4 << 10
	maxPrivilegedStdout = 4 << 20
	maxPrivilegedStderr = 64 << 10
)

// runPrivileged 执行一条已注册的白名单命令，经 sudo -n 以 root 运行。
//
// 安全要点：
//   - argv 来自 commands.go 的固定定义，不经任何 shell，无注入面；
//   - sudo -n（non-interactive）：sudoers 没配好就立即失败，绝不卡在密码提示；
//   - sudo -- 之后才是真正的命令，防止 argv 里的 - 参数被 sudo 当成自己的选项。
//   - stdout/stderr 分别限制为 4 MiB/64 KiB，超限时采集失败但持续排空管道，
//     避免大主机或异常命令造成 Agent 内存无界增长。
//
// 校验：传入的 argv 必须命中白名单，否则拒绝 —— 这是「agent 不支持任意命令」
// 这一硬约束的执行点。
func runPrivileged(argv []string) (stdout []byte, stderr []byte, err error) {
	return runPrivilegedInput(argv, nil)
}

// runPrivilegedInput is reserved for fixed policy write helpers. Keeping stdin
// bounded and refusing it for read commands prevents this channel from becoming
// an argument-smuggling escape hatch.
func runPrivilegedInput(argv []string, input []byte) (stdout []byte, stderr []byte, err error) {
	definition, ok := commandByName(argv)
	if !ok {
		return nil, nil, errors.New("命令不在 sudoers 白名单内，拒绝执行: " + joinArgv(argv))
	}
	if len(input) > maxPrivilegedStdin {
		return nil, nil, errors.New("特权命令输入超过限制")
	}
	if len(input) > 0 && !definition.Write {
		return nil, nil, errors.New("只读特权命令不接受标准输入")
	}
	cmd := exec.Command("sudo", append([]string{"-n", "--"}, argv...)...)
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	out := boundedBuffer{limit: maxPrivilegedStdout}
	errb := boundedBuffer{limit: maxPrivilegedStderr}
	cmd.Stdout = &out
	cmd.Stderr = &errb
	runErr := cmd.Run()
	var limitErr error
	if out.exceeded || errb.exceeded {
		limitErr = fmt.Errorf("特权命令输出超过限制（stdout=%d bytes, stderr=%d bytes）", maxPrivilegedStdout, maxPrivilegedStderr)
	}
	if runErr != nil || limitErr != nil {
		return out.Bytes(), errb.Bytes(), &sudoError{argv: argv, err: errors.Join(runErr, limitErr), stderr: errb.String()}
	}
	return out.Bytes(), errb.Bytes(), nil
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (buffer *boundedBuffer) Write(content []byte) (int, error) {
	written := len(content)
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining > 0 {
		if remaining > len(content) {
			remaining = len(content)
		}
		_, _ = buffer.buffer.Write(content[:remaining])
	}
	if remaining < len(content) {
		buffer.exceeded = true
	}
	return written, nil
}

func (buffer *boundedBuffer) Bytes() []byte  { return buffer.buffer.Bytes() }
func (buffer *boundedBuffer) String() string { return buffer.buffer.String() }

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
