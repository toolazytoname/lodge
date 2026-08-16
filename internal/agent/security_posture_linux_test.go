//go:build linux

package agent

import (
	"errors"
	"testing"

	"github.com/toolazytoname/lodge/internal/shared"
)

func TestParseSSHDPosture(t *testing.T) {
	password, root, publicKey := parseSSHDPosture("passwordauthentication no\npermitrootlogin prohibit-password\npubkeyauthentication yes\n")
	if password != shared.SecurityDisabled || root != shared.SecurityRestricted || publicKey != shared.SecurityEnabled {
		t.Fatalf("unexpected SSHD posture: %q %q %q", password, root, publicKey)
	}
	password, root, publicKey = parseSSHDPosture("passwordauthentication maybe\npermitrootlogin no\npubkeyauthentication no\n")
	if password != shared.SecurityUnknown || root != shared.SecurityDisabled || publicKey != shared.SecurityDisabled {
		t.Fatalf("unexpected conservative SSHD fallback: %q %q %q", password, root, publicKey)
	}
	password, _, _ = parseSSHDPosture("passwordauthentication no\nkbdinteractiveauthentication yes\npermitrootlogin no\npubkeyauthentication yes\n")
	if password != shared.SecurityEnabled {
		t.Fatalf("keyboard-interactive must count as password authentication: %q", password)
	}
}

func TestParseSSHListenerPosture(t *testing.T) {
	if got := parseSSHListenerPosture("LISTEN 0 128 0.0.0.0:22 0.0.0.0:*\n"); got != shared.SecurityEnabled {
		t.Fatalf("wildcard listener = %q", got)
	}
	if got := parseSSHListenerPosture("LISTEN 0 128 100.64.0.8:22 0.0.0.0:*\n"); got != shared.SecurityRestricted {
		t.Fatalf("specific listener = %q", got)
	}
	if got := parseSSHListenerPosture(""); got != shared.SecurityDisabled {
		t.Fatalf("absent listener = %q", got)
	}
}

func TestParseControlStates(t *testing.T) {
	if got := parseSSHDBool("yes"); got != shared.SecurityEnabled {
		t.Fatalf("yes = %q", got)
	}
	if got := parseSSHDBool("no"); got != shared.SecurityDisabled {
		t.Fatalf("no = %q", got)
	}
}

func TestParseSecurityControlCommandResults(t *testing.T) {
	if got := parseUFWStatus([]byte("Status: active\n"), nil); got != shared.SecurityEnabled {
		t.Fatalf("active UFW = %q", got)
	}
	if got := parseUFWStatus(nil, errSecurityCommandUnavailable); got != shared.SecurityUnavailable {
		t.Fatalf("missing UFW = %q", got)
	}
	if got := parseFail2BanStatus([]byte("Server replied: pong\n"), nil); got != shared.SecurityEnabled {
		t.Fatalf("running Fail2Ban = %q", got)
	}
	if got := parseFail2BanStatus(nil, errors.New("socket missing")); got != shared.SecurityDisabled {
		t.Fatalf("stopped Fail2Ban = %q", got)
	}
	if got := parseTailscaleStatus([]byte(`{"BackendState":"Running"}`), nil); got != shared.SecurityEnabled {
		t.Fatalf("running Tailscale = %q", got)
	}
	if got := parseTailscaleStatus([]byte(`{"BackendState":"NeedsLogin"}`), nil); got != shared.SecurityDisabled {
		t.Fatalf("not logged in Tailscale = %q", got)
	}
}
