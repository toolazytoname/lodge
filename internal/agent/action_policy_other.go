//go:build !linux

package agent

import (
	"errors"
	"io"
)

func WriteActionDefinitions(io.Writer) error {
	return errors.New("controlled actions are supported only on Linux")
}

func ExecutePolicyAction(io.Reader, io.Writer) error {
	return errors.New("controlled actions are supported only on Linux")
}
