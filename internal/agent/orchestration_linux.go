//go:build linux

package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// WriteComposeMetadata executes one fixed Docker read inside the root-owned
// Agent helper, then emits only validated project/service identity tuples.
func WriteComposeMetadata(writer io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("Compose metadata collector must run as root through the exact sudoers rule")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, dockerComposeQuery[0], dockerComposeQuery[1:]...)
	stdout := boundedBuffer{limit: maxPrivilegedStdout}
	stderr := boundedBuffer{limit: maxPrivilegedStderr}
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return errors.New("Docker Compose identity query timed out")
		}
		return fmt.Errorf("Docker Compose identity query failed: %w: %s", err, firstLine(stderr.String()))
	}
	if stdout.exceeded || stderr.exceeded {
		return errors.New("Docker Compose identity query exceeded its output limit")
	}
	return writeComposeMetadata(stdout.Bytes(), writer)
}
