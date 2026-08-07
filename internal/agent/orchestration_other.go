//go:build !linux

package agent

import (
	"errors"
	"io"
)

func WriteComposeMetadata(io.Writer) error {
	return errors.New("Compose metadata collection is supported only on Linux")
}
