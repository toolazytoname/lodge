//go:build !linux

package agent

import (
	"errors"
	"io"
)

func WriteProcessOrigins(io.Writer) error {
	return errors.New("process origin collection is supported only on Linux")
}
