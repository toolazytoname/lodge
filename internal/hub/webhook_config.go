package hub

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultWebhookCooldownSeconds = 15 * 60
	maximumWebhookSecretBytes     = 4096
)

type WebhookConfig struct {
	URL             string `json:"url"`
	SecretFile      string `json:"secretFile,omitempty"`
	CooldownSeconds int    `json:"cooldownSeconds,omitempty"`
}

func normalizeWebhookConfig(config *WebhookConfig) error {
	if config == nil {
		return errors.New("configuration is missing")
	}
	if !utf8.ValidString(config.URL) || containsControl(config.URL) {
		return errors.New("url is invalid")
	}
	config.URL = strings.TrimSpace(config.URL)
	if config.URL == "" || len(config.URL) > maxURLBytes {
		return fmt.Errorf("url must contain 1..%d bytes", maxURLBytes)
	}
	parsed, err := url.Parse(config.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("url must be an absolute HTTPS URL without credentials or fragment")
	}
	if parsed.RawPath != "" {
		return errors.New("url must not contain an ambiguous encoded path")
	}
	config.URL = parsed.String()

	if !utf8.ValidString(config.SecretFile) || containsControl(config.SecretFile) {
		return errors.New("secretFile is invalid")
	}
	config.SecretFile = strings.TrimSpace(config.SecretFile)
	if config.SecretFile != "" {
		if len(config.SecretFile) > 4096 || !filepath.IsAbs(config.SecretFile) || filepath.Clean(config.SecretFile) != config.SecretFile {
			return errors.New("secretFile must be a clean absolute path")
		}
	}
	if config.CooldownSeconds == 0 {
		config.CooldownSeconds = defaultWebhookCooldownSeconds
	}
	if config.CooldownSeconds < 30 || config.CooldownSeconds > int((24*time.Hour)/time.Second) {
		return errors.New("cooldownSeconds must be between 30 and 86400")
	}
	return nil
}

func (config WebhookConfig) Cooldown() time.Duration {
	return time.Duration(config.CooldownSeconds) * time.Second
}

// LoadWebhookSecret reads an optional owner-only bearer secret without
// following symlinks or accepting unbounded content.
func LoadWebhookSecret(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect webhook secret: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("webhook secret must be a regular file, not a symlink")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("webhook secret must be owner-only (mode 0600)")
	}
	if info.Size() > maximumWebhookSecretBytes+2 {
		return "", fmt.Errorf("webhook secret exceeds %d bytes", maximumWebhookSecretBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open webhook secret: %w", err)
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, maximumWebhookSecretBytes+3))
	if err != nil {
		return "", fmt.Errorf("read webhook secret: %w", err)
	}
	contents = []byte(strings.TrimSuffix(strings.TrimSuffix(string(contents), "\n"), "\r"))
	secret := string(contents)
	if secret == "" || len(secret) > maximumWebhookSecretBytes || !utf8.ValidString(secret) || !isVisibleASCII(secret) {
		return "", fmt.Errorf("webhook secret must contain 1..%d visible ASCII bytes without whitespace", maximumWebhookSecretBytes)
	}
	return secret, nil
}

func isVisibleASCII(value string) bool {
	for _, character := range []byte(value) {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}
