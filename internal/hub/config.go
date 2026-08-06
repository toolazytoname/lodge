package hub

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Config is the owner-private Hub configuration. Password is accepted only for
// a migration window; PasswordHash is the durable representation.
type Config struct {
	Comment       string        `json:"_comment,omitempty"`
	CommentAgents string        `json:"_comment_agents,omitempty"`
	Password      string        `json:"password,omitempty"`
	PasswordHash  string        `json:"passwordHash,omitempty"`
	Agents        []AgentConfig `json:"agents"`
}

func LoadConfig(path string) (Config, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Config{}, fmt.Errorf("检查 hub 配置 %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Config{}, fmt.Errorf("hub 配置必须是普通文件，不能是符号链接")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return Config{}, fmt.Errorf("hub 配置 %s 必须仅所有者可读写（建议 0600）", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("读取 hub 配置 %s: %w", path, err)
	}
	var config Config
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("解析 hub 配置: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Config{}, fmt.Errorf("解析 hub 配置: %w", err)
	}
	if config.Password != "" && config.PasswordHash != "" {
		return Config{}, fmt.Errorf("password 与 passwordHash 不能同时配置")
	}
	if config.PasswordHash != "" {
		if err := validatePasswordHash(config.PasswordHash); err != nil {
			return Config{}, fmt.Errorf("passwordHash: %w", err)
		}
	}
	knownIDs := make(map[string]struct{}, len(config.Agents))
	for i := range config.Agents {
		agent := &config.Agents[i]
		agent.ID = strings.TrimSpace(agent.ID)
		agent.Name = strings.TrimSpace(agent.Name)
		agent.URL = strings.TrimRight(strings.TrimSpace(agent.URL), "/")
		if agent.ID == "" || len(agent.ID) > maxAgentIDBytes {
			return Config{}, fmt.Errorf("第 %d 个 agent 缺 id", i+1)
		}
		if _, duplicate := knownIDs[agent.ID]; duplicate {
			return Config{}, fmt.Errorf("agent id 重复: %s", agent.ID)
		}
		knownIDs[agent.ID] = struct{}{}
		if agent.URL == "" {
			return Config{}, fmt.Errorf("agent %s 缺 url", agent.ID)
		}
		parsedURL, err := url.Parse(agent.URL)
		if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" || parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
			return Config{}, fmt.Errorf("agent %s 的 url 必须是无凭据、查询或片段的 http/https 地址", agent.ID)
		}
		if strings.TrimSpace(agent.Token) == "" {
			return Config{}, fmt.Errorf("agent %s 缺 token", agent.ID)
		}
		agent.Token = strings.TrimSpace(agent.Token)
		if agent.Name == "" {
			agent.Name = agent.ID
		}
	}
	return config, nil
}

// ResolvePasswordHash converts a legacy plaintext config only in memory. The
// caller must warn the operator and migrate the file; sessions intentionally
// expire on every restart while legacy mode remains.
func ResolvePasswordHash(config Config) (string, bool, error) {
	if config.PasswordHash != "" {
		return config.PasswordHash, false, nil
	}
	if config.Password == "" {
		return "", false, nil
	}
	hash, err := HashPassword(config.Password)
	return hash, true, err
}
