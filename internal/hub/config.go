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
	"syscall"
	"unicode"
	"unicode/utf8"
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
	if err := normalizeConfig(&config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func normalizeConfig(config *Config) error {
	if config.Password != "" && config.PasswordHash != "" {
		return fmt.Errorf("password 与 passwordHash 不能同时配置")
	}
	if config.PasswordHash != "" {
		if err := validatePasswordHash(config.PasswordHash); err != nil {
			return fmt.Errorf("passwordHash: %w", err)
		}
	}
	knownIDs := make(map[string]struct{}, len(config.Agents))
	for i := range config.Agents {
		agent := &config.Agents[i]
		if !utf8.ValidString(agent.ID) || !utf8.ValidString(agent.Name) || !utf8.ValidString(agent.URL) || !utf8.ValidString(agent.Token) || !utf8.ValidString(agent.PublicHost) {
			return fmt.Errorf("第 %d 个 agent 配置不是有效 UTF-8", i+1)
		}
		if containsControl(agent.ID) || containsControl(agent.Name) || containsControl(agent.URL) || containsControl(agent.Token) || containsControl(agent.PublicHost) {
			return fmt.Errorf("第 %d 个 agent 配置包含控制字符", i+1)
		}
		agent.ID = strings.TrimSpace(agent.ID)
		agent.Name = strings.TrimSpace(agent.Name)
		agent.URL = strings.TrimSpace(agent.URL)
		agent.PublicHost = strings.TrimSpace(agent.PublicHost)
		if agent.ID == "" || len(agent.ID) > maxAgentIDBytes || !utf8.ValidString(agent.ID) {
			return fmt.Errorf("第 %d 个 agent 的 id 无效", i+1)
		}
		if _, duplicate := knownIDs[agent.ID]; duplicate {
			return fmt.Errorf("agent id 重复: %s", agent.ID)
		}
		knownIDs[agent.ID] = struct{}{}
		if agent.URL == "" {
			return fmt.Errorf("agent %s 缺 url", agent.ID)
		}
		parsedURL, err := url.Parse(agent.URL)
		if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" || parsedURL.User != nil || (parsedURL.Path != "" && parsedURL.Path != "/") || parsedURL.RawPath != "" || parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
			return fmt.Errorf("agent %s 的 url 必须是无凭据、路径、查询或片段的 http/https base URL", agent.ID)
		}
		parsedURL.Path = ""
		agent.URL = parsedURL.String()
		if agent.Token == "" || len(agent.Token) > 4096 || !utf8.ValidString(agent.Token) {
			return fmt.Errorf("agent %s 的 token 无效", agent.ID)
		}
		if agent.Name == "" {
			agent.Name = agent.ID
		}
		if len(agent.Name) > 256 || !utf8.ValidString(agent.Name) {
			return fmt.Errorf("agent %s 的 name 无效", agent.ID)
		}
		if agent.PublicHost != "" {
			parsedHost, err := url.Parse("//" + agent.PublicHost)
			if err != nil || parsedHost.Host != agent.PublicHost || parsedHost.Hostname() == "" || parsedHost.User != nil || parsedHost.Port() != "" || parsedHost.Path != "" || parsedHost.RawQuery != "" || parsedHost.Fragment != "" {
				return fmt.Errorf("agent %s 的 publicHost 必须是无端口的域名或 IP", agent.ID)
			}
		}
	}
	return nil
}

func containsControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
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
	if err := writeConfigAtomically(path, original, config); err != nil {
		return false, err
	}
	return true, nil
}

// UpsertAgentConfig adds or replaces one Agent without exposing its bearer
// token through command-line arguments or process output.
func UpsertAgentConfig(path string, agent AgentConfig) (bool, error) {
	original, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	config, err := LoadConfig(path)
	if err != nil {
		return false, err
	}
	single := Config{Agents: []AgentConfig{agent}}
	if err := normalizeConfig(&single); err != nil {
		return false, err
	}
	agent = single.Agents[0]
	config.Agents = append([]AgentConfig(nil), config.Agents...)
	for index := range config.Agents {
		if config.Agents[index].ID != agent.ID {
			continue
		}
		if config.Agents[index] == agent {
			return false, nil
		}
		config.Agents[index] = agent
		if err := writeConfigAtomically(path, original, config); err != nil {
			return false, err
		}
		return true, nil
	}
	config.Agents = append(config.Agents, agent)
	if err := writeConfigAtomically(path, original, config); err != nil {
		return false, err
	}
	return true, nil
}

func writeConfigAtomically(path string, original os.FileInfo, config Config) error {
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".config-migration-*")
	if err != nil {
		return fmt.Errorf("create config migration file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if stat, ok := original.Sys().(*syscall.Stat_t); ok {
		if err := temporary.Chown(int(stat.Uid), int(stat.Gid)); err != nil {
			_ = temporary.Close()
			return fmt.Errorf("preserve config ownership: %w", err)
		}
	}
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(original, current) || original.Size() != current.Size() || !original.ModTime().Equal(current.ModTime()) {
		return errors.New("hub config changed during atomic update")
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer directoryHandle.Close()
	if err := directoryHandle.Sync(); err != nil {
		return err
	}
	return nil
}
