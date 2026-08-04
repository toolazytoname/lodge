package agent

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// loadToken 读取 token 文件；不存在则生成一个并写回（0600）。
//
// 由 agent 自己生成 token 是为了让 `lodge-agent serve` 开箱即用；
// install-agent.sh 也可以选择预生成。两者都保证 token 只在本地文件里，
// 随日志/stderr 提示一次，绝不外传。
func LoadToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		t := strings.TrimSpace(string(b))
		if t != "" {
			return t, nil
		}
		// 文件存在但为空 —— 视为未初始化，重新生成。
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("读取 token 文件 %s 失败: %w", path, err)
	}

	// 生成新 token：32 字节随机 → 64 hex 字符（256 bit）。
	tok, gerr := generateToken()
	if gerr != nil {
		return "", gerr
	}
	if werr := writeToken(path, tok); werr != nil {
		// 写不进去（只读环境）—— 退回内存 token，但警告：重启会变。
		fmt.Fprintf(os.Stderr, "警告：无法写入 token 文件 %s（%v）；本次使用临时 token，重启后失效。\n", path, werr)
		return tok, nil
	}
	fmt.Fprintf(os.Stderr, "已生成新 token 并写入 %s\n首次部署请妥善保存（仅本次提示）。\n", path)
	return tok, nil
}

func generateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成随机 token 失败: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func writeToken(path, token string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// 先写临时文件再 rename，避免半写状态被读到。
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(token+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
