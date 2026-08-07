//go:build !linux

package agent

import (
	"errors"
	"io"
)

func WriteSecurityPosture(io.Writer) error {
	return errors.New("security posture collection is supported only on Linux")
}
