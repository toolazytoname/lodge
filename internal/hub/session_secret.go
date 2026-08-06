package hub

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const sessionKeyBytes = 32

// LoadOrCreateSessionKey keeps session signing independent from the login
// password verifier. The file must remain readable only by the Hub account.
func LoadOrCreateSessionKey(path string) ([]byte, error) {
	key, err := readSessionKey(path)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create session secret directory: %w", err)
	}
	raw := make([]byte, sessionKeyBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate session secret: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return readSessionKey(path)
	}
	if err != nil {
		return nil, fmt.Errorf("create session secret: %w", err)
	}
	encoded := base64.RawStdEncoding.EncodeToString(raw) + "\n"
	if _, err := file.WriteString(encoded); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("write session secret: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close session secret: %w", err)
	}
	return raw, nil
}

func readSessionKey(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("session secret must be a regular file, not a symlink")
	}
	if info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("session secret %s must have mode 0600", path)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read session secret: %w", err)
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil || len(key) != sessionKeyBytes {
		return nil, errors.New("session secret must contain exactly 32 base64-encoded random bytes")
	}
	return key, nil
}
