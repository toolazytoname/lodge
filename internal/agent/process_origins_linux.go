//go:build linux

package agent

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const maxProcessOrigins = 65536

// WriteProcessOrigins emits only redacted attribution metadata. It never reads
// command arguments or environment variables, and replaces the full working
// directory with a basename plus a one-way fingerprint.
func WriteProcessOrigins(writer io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("process origin collector must run as root through the exact sudoers rule")
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(writer)
	emitted := 0
	for _, entry := range entries {
		if emitted >= maxProcessOrigins {
			break
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 1 {
			continue
		}
		origin, ok := readProcessOrigin(pid)
		if !ok {
			continue
		}
		if err := encoder.Encode(origin); err != nil {
			return err
		}
		emitted++
	}
	return nil
}

func readProcessOrigin(pid int) (processOrigin, bool) {
	root := "/proc/" + strconv.Itoa(pid)
	uid, ok := readProcessUID(root + "/status")
	if !ok {
		return processOrigin{}, false
	}
	commBytes, _ := os.ReadFile(root + "/comm")
	comm := sanitizeProcessLabel(strings.TrimSpace(string(commBytes)))
	executable := ""
	if target, err := os.Readlink(root + "/exe"); err == nil {
		executable = sanitizeProcessLabel(filepath.Base(target))
	}
	cwdBase := ""
	cwdFingerprint := ""
	if target, err := os.Readlink(root + "/cwd"); err == nil {
		cleaned := filepath.Clean(target)
		if cleaned != "." && cleaned != string(filepath.Separator) {
			cwdBase = sanitizeProcessLabel(filepath.Base(cleaned))
			digest := sha256.Sum256([]byte(cleaned))
			cwdFingerprint = hex.EncodeToString(digest[:8])
		}
	}
	origin := processOrigin{
		PID: pid, UID: uid, Comm: comm, Executable: executable,
		CWDBase: cwdBase, CWDFingerprint: cwdFingerprint,
	}
	return origin, origin.strong()
}

func readProcessUID(path string) (int, bool) {
	file, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == "Uid:" {
			uid, err := strconv.Atoi(fields[1])
			return uid, err == nil
		}
	}
	return 0, false
}
