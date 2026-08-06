package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadAgentTokenFromOwnerPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("opaque-secret\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	token, err := readAgentToken(file)
	if err != nil {
		t.Fatal(err)
	}
	if token != "opaque-secret" {
		t.Fatalf("token = %q, want opaque-secret", token)
	}
}

func TestReadAgentTokenRejectsEmptyAndOversizedInput(t *testing.T) {
	for _, contents := range []string{"\n", strings.Repeat("x", 4097)} {
		path := filepath.Join(t.TempDir(), "token")
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		_, readErr := readAgentToken(file)
		_ = file.Close()
		if readErr == nil {
			t.Fatalf("unsafe token length %d was accepted", len(contents))
		}
	}
}
