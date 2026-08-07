package agent

import (
	"testing"

	"github.com/toolazytoname/lodge/internal/shared"
)

func TestValidateSecurityPostureRejectsUnknownVocabulary(t *testing.T) {
	valid := shared.SecurityPosture{
		SSHListener: shared.SecurityEnabled, SSHPasswordAuthentication: shared.SecurityDisabled,
		SSHRootLogin: shared.SecurityRestricted, SSHPublicKeyAuthentication: shared.SecurityEnabled,
		Firewall: shared.SecurityUnavailable, Fail2Ban: shared.SecurityUnknown, Tailscale: shared.SecurityEnabled,
	}
	if err := validateSecurityPosture(valid); err != nil {
		t.Fatalf("valid posture rejected: %v", err)
	}
	valid.Firewall = "yes"
	if err := validateSecurityPosture(valid); err == nil {
		t.Fatal("invalid posture vocabulary was accepted")
	}
}
