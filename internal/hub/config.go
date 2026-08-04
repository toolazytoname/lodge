package hub

import (
	"encoding/json"
	"fmt"
	"os"
)

// LoadConfig 从 JSON 文件加载 agents 配置与登录密码。
// 文件格式：{"password":"...","agents":[{"id","name","url","token","publicHost"}, ...]}
// password 为空表示不启用登录认证 —— 仅在 tailnet 内访问时可接受，
// 一旦公网暴露必须设置密码（公网部署时由脚本强制）。
func LoadConfig(path string) (agents []AgentConfig, password string, err error) {
	b, rerr := os.ReadFile(path)
	if rerr != nil {
		return nil, "", fmt.Errorf("读取 hub 配置 %s: %w", path, rerr)
	}
	var f struct {
		Password string        `json:"password"`
		Agents   []AgentConfig `json:"agents"`
	}
	if uerr := json.Unmarshal(b, &f); uerr != nil {
		return nil, "", fmt.Errorf("解析 hub 配置: %w", uerr)
	}
	for i := range f.Agents {
		a := &f.Agents[i]
		if a.ID == "" {
			return nil, "", fmt.Errorf("第 %d 个 agent 缺 id", i+1)
		}
		if a.URL == "" {
			return nil, "", fmt.Errorf("agent %s 缺 url", a.ID)
		}
		if a.Name == "" {
			a.Name = a.ID
		}
	}
	return f.Agents, f.Password, nil
}
