//go:build !linux

package agent

import (
	"errors"
	"io"
)

func WriteDeploymentDefinitions(io.Writer) error {
	return errors.New("deployment policy is only available on Linux")
}

func ExecutePolicyDeployment(io.Reader, io.Writer) error {
	return errors.New("deployment execution is only available on Linux")
}
