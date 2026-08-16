//go:build !linux

package agent

import (
	"errors"
	"io"
)

func WriteOperatorOwners(io.Writer) error {
	return errors.New("owner-service operator is supported only on Linux")
}

func ExecuteOperator(io.Reader, io.Writer) error {
	return errors.New("owner-service operator is supported only on Linux")
}
