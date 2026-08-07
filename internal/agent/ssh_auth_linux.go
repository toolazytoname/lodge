//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"
)

// WriteSSHAuthSummary reads a fixed journald window as root and emits only
// source-IP counts. Raw messages and usernames never leave this helper.
func WriteSSHAuthSummary(writer io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("SSH auth collector must run as root through the exact sudoers rule")
	}
	end := time.Now().UTC().Truncate(time.Second)
	start := end.Add(-sshAuthWindow)
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
