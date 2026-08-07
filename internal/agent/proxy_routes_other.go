//go:build !linux

package agent

import (
	"errors"
	"io"
)

func WriteProxyRoutes(io.Writer) error {
	return errors.New("proxy route collector is only supported on Linux")
}
