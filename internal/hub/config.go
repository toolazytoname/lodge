package hub

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
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

// MigrateConfigPassword replaces the legacy plaintext password with an
// Argon2id verifier in one owner-only atomic rename. It never prints or returns
// either credential and refuses to overwrite a concurrently changed file.
func MigrateConfigPassword(path string) (bool, error) {
	original, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	config, err := LoadConfig(path)
	if err != nil {
		return false, err
	}
	if config.Password == "" {
		return false, nil
	}
	passwordHash, err := HashPassword(config.Password)
	if err != nil {
		return false, err
	}
	config.Password = ""
	config.PasswordHash = passwordHash
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return false, err
	}
	encoded = append(encoded, '\n')
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".config-migration-*")
	if err != nil {
		return false, fmt.Errorf("create config migration file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return false, err
	}
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return false, err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return false, err
	}
	if err := temporary.Close(); err != nil {
		return false, err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	if !os.SameFile(original, current) || original.Size() != current.Size() || !original.ModTime().Equal(current.ModTime()) {
		return false, errors.New("hub config changed during password migration")
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return false, err
	}
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return false, err
	}
	defer directoryHandle.Close()
	if err := directoryHandle.Sync(); err != nil {
		return false, err
	}
	return true, nil
}
