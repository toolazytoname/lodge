package agent

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"unicode"
)

type processOrigin struct {
	PID            int    `json:"pid"`
	UID            int    `json:"uid"`
	Comm           string `json:"comm,omitempty"`
	Executable     string `json:"executable,omitempty"`
	CWDBase        string `json:"cwdBase,omitempty"`
	CWDFingerprint string `json:"cwdFingerprint,omitempty"`
}

func parseProcessOrigins(content []byte) map[int]processOrigin {
	origins := make(map[int]processOrigin)
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		var origin processOrigin
		if err := json.Unmarshal(scanner.Bytes(), &origin); err != nil || !origin.strong() {
			continue
		}
		origins[origin.PID] = origin
	}
	return origins
}

func (origin processOrigin) strong() bool {
	if origin.PID <= 1 || origin.UID < 0 || origin.CWDBase == "" || len(origin.CWDFingerprint) != 16 {
		return false
	}
	if _, err := hex.DecodeString(origin.CWDFingerprint); err != nil {
		return false
	}
	return origin.Executable != "" || origin.Comm != ""
}

func (origin processOrigin) workloadKey() string {
	digest := sha256.Sum256([]byte(strconv.Itoa(origin.UID) + "\x00" + origin.CWDFingerprint + "\x00" + origin.Executable + "\x00" + origin.Comm))
	return "process:" + hex.EncodeToString(digest[:8])
}

func (origin processOrigin) workloadName(fallback string) string {
	runtimeName := origin.Executable
	if runtimeName == "" {
		runtimeName = origin.Comm
	}
	if runtimeName == "" {
		runtimeName = fallback
	}
	if origin.CWDBase == runtimeName || strings.TrimSpace(origin.CWDBase) == "" {
		return runtimeName
	}
	return origin.CWDBase + " · " + runtimeName
}

func sanitizeProcessLabel(value string) string {
	value = strings.ToValidUTF8(value, "�")
	runes := make([]rune, 0, len(value))
	for _, character := range value {
		if unicode.IsControl(character) {
			character = '�'
		}
		runes = append(runes, character)
		if len(runes) == 128 {
			break
		}
	}
	return strings.TrimSpace(string(runes))
}
