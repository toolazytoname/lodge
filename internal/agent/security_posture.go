package agent

import (
	"encoding/json"
	"errors"

	"github.com/toolazytoname/lodge/internal/shared"
)

func collectSecurityPosture() (*shared.SecurityPosture, string) {
	stdout, _, err := runPrivileged(securityPostureCommand)
	if err != nil {
		return nil, "采集 SSH 与本机防护姿态失败"
	}
	var posture shared.SecurityPosture
	if err := json.Unmarshal(stdout, &posture); err != nil || validateSecurityPosture(posture) != nil {
		return nil, "SSH 与本机防护姿态格式无效"
	}
	return &posture, ""
}

func validateSecurityPosture(posture shared.SecurityPosture) error {
	for _, value := range []shared.SecuritySetting{
		posture.SSHListener, posture.SSHPasswordAuthentication, posture.SSHRootLogin,
		posture.SSHPublicKeyAuthentication, posture.Firewall, posture.Fail2Ban, posture.Tailscale,
	} {
		switch value {
		case shared.SecurityEnabled, shared.SecurityDisabled, shared.SecurityRestricted, shared.SecurityUnavailable, shared.SecurityUnknown:
		default:
			return errors.New("security posture contains an invalid setting")
		}
	}
	return nil
}
