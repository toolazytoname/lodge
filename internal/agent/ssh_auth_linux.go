//go:build linux

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/toolazytoname/lodge/internal/shared"
)

const maximumSSHAuthLogBytes int64 = 8 << 20

var sshAuthLogPaths = []string{"/var/log/auth.log", "/var/log/secure"}

// WriteSSHAuthSummary reads a bounded fixed-path authentication-log tail, or a
// bounded journald window when no log file exists, and emits only source-IP
// counts. Raw messages and usernames never leave this helper.
func WriteSSHAuthSummary(writer io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("SSH auth collector must run as root through the exact sudoers rule")
	}
	end := time.Now().UTC().Truncate(time.Second)
	start := end.Add(-sshAuthWindow)
	for _, path := range sshAuthLogPaths {
		summary, found, err := summarizeSSHAuthFile(path, start, end)
		if err != nil {
			return err
		}
		if found {
			return json.NewEncoder(writer).Encode(summary)
		}
	}
	return writeSSHAuthJournalSummary(writer, start, end)
}

func summarizeSSHAuthFile(path string, start, end time.Time) (shared.SSHAuthSummary, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return shared.SSHAuthSummary{}, false, nil
	}
	if err != nil {
		return shared.SSHAuthSummary{}, true, errors.New("SSH authentication log metadata is unavailable")
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return shared.SSHAuthSummary{}, true, errors.New("SSH authentication log must be a regular non-symlink file")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return shared.SSHAuthSummary{}, true, errors.New("SSH authentication log must not be group- or world-writable")
	}
	file, err := os.Open(path)
	if err != nil {
		return shared.SSHAuthSummary{}, true, errors.New("SSH authentication log is unavailable")
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return shared.SSHAuthSummary{}, true, errors.New("SSH authentication log changed during open")
	}
	size := openedInfo.Size()
	offset := int64(0)
	truncated := false
	if size > maximumSSHAuthLogBytes {
		offset = size - maximumSSHAuthLogBytes
		truncated = true
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return shared.SSHAuthSummary{}, true, errors.New("SSH authentication log seek failed")
	}
	content, err := io.ReadAll(io.LimitReader(file, size-offset))
	if err != nil {
		return shared.SSHAuthSummary{}, true, errors.New("SSH authentication log read failed")
	}
	if int64(len(content)) != size-offset {
		return shared.SSHAuthSummary{}, true, errors.New("SSH authentication log changed during read")
	}
	if truncated {
		newline := bytes.IndexByte(content, '\n')
		if newline < 0 {
			return shared.SSHAuthSummary{}, true, errors.New("SSH authentication log tail is incomplete")
		}
		content = content[newline+1:]
	}
	summary, err := summarizeSSHTextLog(content, start, end)
	if err != nil {
		return shared.SSHAuthSummary{}, true, err
	}
	return summary, true, nil
}

func writeSSHAuthJournalSummary(writer io.Writer, start, end time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	journalctl, err := exec.LookPath("journalctl")
	if err != nil {
		return errors.New("journal reader is unavailable")
	}
	command := exec.CommandContext(ctx, journalctl,
		"--quiet", "--no-pager", "--output=json", "--since=-10min",
		"_COMM=sshd", "+", "SYSLOG_IDENTIFIER=sshd")
	stdout := boundedBuffer{limit: maxPrivilegedStdout}
	stderr := boundedBuffer{limit: maxPrivilegedStderr}
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return errors.New("SSH journal query timed out")
		}
		return errors.New("SSH journal query failed")
	}
	if stdout.exceeded || stderr.exceeded {
		return errors.New("SSH journal query exceeded its output limit")
	}
	summary, err := summarizeSSHJournal(stdout.Bytes(), start, end)
	if err != nil {
		return err
	}
	return json.NewEncoder(writer).Encode(summary)
}
