//go:build !linux

package agent

import (
	"errors"
	"io"
)

func WriteSSHAuthSummary(io.Writer) error {
	return errors.New("SSH auth collection is supported only on Linux")
}
