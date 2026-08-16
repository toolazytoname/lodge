//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/toolazytoname/lodge/internal/shared"
)

const securityPostureTimeout = 3 * time.Second

var errSecurityCommandUnavailable = errors.New("security posture command is unavailable")

// WriteSecurityPosture runs no caller-controlled command or query. It converts
// fixed local control output to a small enum-only document while still root, so
// keys, users, rules, paths, addresses and raw daemon output never leave root.
func WriteSecurityPosture(writer io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("security posture collector must run as root through the exact sudoers rule")
	}
	posture := shared.SecurityPosture{
		SSHListener:                shared.SecurityUnknown,
		SSHPasswordAuthentication:  shared.SecurityUnknown,
		SSHRootLogin:               shared.SecurityUnknown,
		SSHPublicKeyAuthentication: shared.SecurityUnknown,
		Firewall:                   shared.SecurityUnknown,
		Fail2Ban:                   shared.SecurityUnknown,
		Tailscale:                  shared.SecurityUnknown,
	}
	results := collectSecurityCommands()
	if output, err := results["sshd"].output, results["sshd"].err; err == nil {
		posture.SSHPasswordAuthentication, posture.SSHRootLogin, posture.SSHPublicKeyAuthentication = parseSSHDPosture(string(output))
	}
	if output, err := results["ss"].output, results["ss"].err; err == nil {
		posture.SSHListener = parseSSHListenerPosture(string(output))
	}
	posture.Firewall = parseUFWStatus(results["ufw"].output, results["ufw"].err)
	posture.Fail2Ban = parseFail2BanStatus(results["fail2ban"].output, results["fail2ban"].err)
	posture.Tailscale = parseTailscaleStatus(results["tailscale"].output, results["tailscale"].err)
	return json.NewEncoder(writer).Encode(posture)
}

type securityCommandResult struct {
	name   string
	output []byte
	err    error
}

// Every fixed probe has a three-second deadline and all run concurrently. A
// broken local utility therefore cannot serially consume the Hub's ten-second
// Agent HTTP budget.
func collectSecurityCommands() map[string]securityCommandResult {
	commands := []struct {
		name   string
		binary string
		args   []string
	}{
		{name: "sshd", binary: "sshd", args: []string{"-T"}},
		{name: "ss", binary: "ss", args: []string{"-ltnH", "sport = :22"}},
		{name: "ufw", binary: "ufw", args: []string{"status"}},
		{name: "fail2ban", binary: "fail2ban-client", args: []string{"ping"}},
		{name: "tailscale", binary: "tailscale", args: []string{"status", "--json"}},
	}
	results := make(chan securityCommandResult, len(commands))
	for _, command := range commands {
		command := command
		go func() {
			output, err := runSecurityCommand(command.binary, command.args...)
			results <- securityCommandResult{name: command.name, output: output, err: err}
		}()
	}
	collected := make(map[string]securityCommandResult, len(commands))
	for range commands {
		result := <-results
		collected[result.name] = result
	}
	return collected
}

func runSecurityCommand(name string, args ...string) ([]byte, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, errSecurityCommandUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), securityPostureTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, path, args...)
	outputBuffer := boundedBuffer{limit: 64 << 10}
	command.Stdout = &outputBuffer
	command.Stderr = io.Discard
	err = command.Run()
	if ctx.Err() != nil {
		return nil, errors.New("security posture command timed out")
	}
	if err != nil || outputBuffer.exceeded {
		return nil, errors.New("security posture command failed")
	}
	return outputBuffer.Bytes(), nil
}

func parseSSHDPosture(output string) (password, root, publicKey shared.SecuritySetting) {
	password, root, publicKey = shared.SecurityUnknown, shared.SecurityUnknown, shared.SecurityUnknown
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		switch fields[0] {
		case "passwordauthentication", "kbdinteractiveauthentication":
			next := parseSSHDBool(fields[1])
			if password == shared.SecurityUnknown || next == shared.SecurityEnabled {
				password = next
			}
		case "permitrootlogin":
			switch strings.ToLower(fields[1]) {
			case "yes":
				root = shared.SecurityEnabled
			case "no":
				root = shared.SecurityDisabled
			case "prohibit-password", "without-password", "forced-commands-only":
				root = shared.SecurityRestricted
			}
		case "pubkeyauthentication":
			publicKey = parseSSHDBool(fields[1])
		}
	}
	return password, root, publicKey
}

func parseSSHDBool(value string) shared.SecuritySetting {
	switch strings.ToLower(value) {
	case "yes":
		return shared.SecurityEnabled
	case "no":
		return shared.SecurityDisabled
	default:
		return shared.SecurityUnknown
	}
}

func parseSSHListenerPosture(output string) shared.SecuritySetting {
	found := false
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		found = true
		local := fields[3]
		if strings.HasPrefix(local, "0.0.0.0:") || strings.HasPrefix(local, "*:") || strings.HasPrefix(local, "[::]:") {
			return shared.SecurityEnabled // enabled here means wildcard exposure
		}
	}
	if found {
		return shared.SecurityRestricted
	}
	return shared.SecurityDisabled
}

func parseUFWStatus(output []byte, err error) shared.SecuritySetting {
	if errors.Is(err, errSecurityCommandUnavailable) {
		return shared.SecurityUnavailable
	}
	if err != nil {
		return shared.SecurityUnknown
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.EqualFold(strings.TrimSpace(line), "Status: active") {
			return shared.SecurityEnabled
		}
		if strings.EqualFold(strings.TrimSpace(line), "Status: inactive") {
			return shared.SecurityDisabled
		}
	}
	return shared.SecurityUnknown
}

func parseFail2BanStatus(output []byte, err error) shared.SecuritySetting {
	if errors.Is(err, errSecurityCommandUnavailable) {
		return shared.SecurityUnavailable
	}
	if err != nil {
		return shared.SecurityDisabled
	}
	if strings.Contains(strings.ToLower(string(output)), "pong") {
		return shared.SecurityEnabled
	}
	return shared.SecurityUnknown
}

func parseTailscaleStatus(output []byte, err error) shared.SecuritySetting {
	if errors.Is(err, errSecurityCommandUnavailable) {
		return shared.SecurityUnavailable
	}
	if err != nil {
		return shared.SecurityUnknown
	}
	var result struct {
		BackendState string `json:"BackendState"`
	}
	if json.Unmarshal(output, &result) != nil {
		return shared.SecurityUnknown
	}
	if result.BackendState == "Running" {
		return shared.SecurityEnabled
	}
	if result.BackendState == "Stopped" || result.BackendState == "NeedsLogin" || result.BackendState == "NoState" {
		return shared.SecurityDisabled
	}
	return shared.SecurityUnknown
}
