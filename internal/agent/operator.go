package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	operatorPolicyVersion      = 1
	maximumOperatorPolicy      = 16 << 10
	maximumOperatorRequest     = 260 << 10
	maximumOperatorFile        = 256 << 10
	maximumOperatorOwners      = 16
	maximumOperatorListEntries = 256
	operatorOpReadFile         = "read-file"
	operatorOpWriteFile        = "write-file"
	operatorOpListDir          = "list-dir"
	operatorOpUnitStatus       = "unit-status"
	operatorOpUnitRestart      = "unit-restart"
)

var listOperatorCommand = []string{"/usr/local/bin/lodge-agent", "--list-operator"}
var executeOperatorCommand = []string{"/usr/local/bin/lodge-agent", "--execute-operator"}

var ownerNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

var deniedOwnerNames = map[string]struct{}{
	"root": {}, "lodge": {}, "lodge-admin": {}, "daemon": {}, "bin": {}, "sys": {},
	"sync": {}, "games": {}, "man": {}, "lp": {}, "mail": {}, "news": {}, "uucp": {},
	"proxy": {}, "www-data": {}, "backup": {}, "list": {}, "irc": {}, "gnats": {},
	"nobody": {}, "nfsnobody": {}, "messagebus": {}, "syslog": {}, "uuidd": {},
	"sshd": {}, "ntp": {}, "mysql": {}, "postgres": {}, "redis": {}, "nginx": {},
	"caddy": {}, "docker": {}, "containerd": {}, "dnsmasq": {}, "tcpdump": {},
	"tss": {}, "polkitd": {}, "usbmux": {}, "fwupd": {}, "snapd": {}, "lxd": {},
	"landscape": {}, "pollinate": {}, "debian-tor": {},
}

var deniedPathComponents = map[string]struct{}{
	".ssh": {}, ".gnupg": {}, ".gpg": {}, ".aws": {}, ".azure": {}, ".kube": {},
	".docker": {}, ".password-store": {}, ".age": {},
}

var deniedPathPrefixes = []string{
	".config/gcloud",
	".config/gh",
	".config/op",
	".config/sops",
	".local/share/keyrings",
}

var deniedBasenames = map[string]struct{}{
	".netrc": {}, ".npmrc": {}, ".pypirc": {}, ".git-credentials": {},
	".vault-token": {}, "id_rsa": {}, "id_dsa": {}, "id_ecdsa": {},
	"id_ed25519": {}, "authorized_keys": {}, "known_hosts": {},
}

var deniedSystemUnits = map[string]struct{}{
	"lodge-agent.service": {}, "lodge-hub.service": {}, "ssh.service": {},
	"sshd.service": {}, "docker.service": {}, "containerd.service": {},
	"dbus.service": {}, "dbus-broker.service": {}, "NetworkManager.service": {},
	"tailscaled.service": {}, "cron.service": {}, "crond.service": {},
	"ufw.service": {}, "fail2ban.service": {}, "polkit.service": {},
	"systemd-logind.service": {}, "systemd-journald.service": {},
	"systemd-networkd.service": {}, "systemd-resolved.service": {},
}

var deniedUnitPrefixes = []string{
	"systemd-", "user@", "user-runtime-dir@", "getty@", "serial-getty@", "autovt@",
}

var operatorOperations = []string{
	operatorOpReadFile, operatorOpWriteFile, operatorOpListDir,
	operatorOpUnitStatus, operatorOpUnitRestart,
}

type operatorPolicy struct {
	Version int      `json:"version"`
	Owners  []string `json:"owners"`
}

type operatorRequest struct {
	Owner   string `json:"owner"`
	Op      string `json:"op"`
	Path    string `json:"path,omitempty"`
	Content string `json:"content,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
	Unit    string `json:"unit,omitempty"`
}

type operatorListResponse struct {
	Owners     []string `json:"owners"`
	Operations []string `json:"operations"`
}

type operatorResult struct {
	OK      bool     `json:"ok"`
	Owner   string   `json:"owner"`
	Op      string   `json:"op"`
	Path    string   `json:"path,omitempty"`
	Unit    string   `json:"unit,omitempty"`
	Content string   `json:"content,omitempty"`
	SHA256  string   `json:"sha256,omitempty"`
	Entries []string `json:"entries,omitempty"`
	Active  string   `json:"active,omitempty"`
	User    string   `json:"user,omitempty"`
	Summary string   `json:"summary"`
}

func decodeOperatorPolicy(content []byte) (operatorPolicy, error) {
	if len(content) > maximumOperatorPolicy {
		return operatorPolicy{}, errors.New("operator policy exceeds 16 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var policy operatorPolicy
	if err := decoder.Decode(&policy); err != nil {
		return operatorPolicy{}, errors.New("operator policy JSON is invalid")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return operatorPolicy{}, errors.New("operator policy contains trailing JSON")
	}
	if err := validateOperatorPolicy(policy); err != nil {
		return operatorPolicy{}, err
	}
	return policy, nil
}

func validateOperatorPolicy(policy operatorPolicy) error {
	if policy.Version != operatorPolicyVersion {
		return fmt.Errorf("operator policy version must be %d", operatorPolicyVersion)
	}
	if policy.Owners == nil || len(policy.Owners) > maximumOperatorOwners {
		return errors.New("operator policy owners are missing or exceed limit")
	}
	seen := make(map[string]struct{}, len(policy.Owners))
	for _, owner := range policy.Owners {
		if err := validateOwnerName(owner); err != nil {
			return err
		}
		if _, duplicate := seen[owner]; duplicate {
			return fmt.Errorf("duplicate operator owner %q", owner)
		}
		seen[owner] = struct{}{}
	}
	return nil
}

func validateOwnerName(name string) error {
	if !ownerNamePattern.MatchString(name) {
		return errors.New("operator owner name is invalid")
	}
	if deniedOwnerName(name) {
		return fmt.Errorf("operator owner %q is a denied system account", name)
	}
	return nil
}

func deniedOwnerName(name string) bool {
	if _, found := deniedOwnerNames[name]; found {
		return true
	}
	return strings.HasPrefix(name, "systemd") || strings.HasPrefix(name, "_")
}

func ownerApproved(policy operatorPolicy, name string) bool {
	for _, owner := range policy.Owners {
		if owner == name {
			return true
		}
	}
	return false
}

func decodeOperatorRequest(reader io.Reader) (operatorRequest, error) {
	content, err := io.ReadAll(io.LimitReader(reader, maximumOperatorRequest+1))
	if err != nil || len(content) > maximumOperatorRequest {
		return operatorRequest{}, errors.New("operator request exceeds limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var request operatorRequest
	if err := decoder.Decode(&request); err != nil {
		return operatorRequest{}, errors.New("operator request JSON is invalid")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return operatorRequest{}, errors.New("operator request contains trailing JSON")
	}
	return validateOperatorRequest(request)
}

func validateOperatorRequest(request operatorRequest) (operatorRequest, error) {
	if err := validateOwnerName(request.Owner); err != nil {
		return operatorRequest{}, err
	}
	switch request.Op {
	case operatorOpReadFile:
		cleaned, err := validateRelativeOwnerPath(request.Path, false)
		if err != nil {
			return operatorRequest{}, err
		}
		if request.Content != "" || request.SHA256 != "" || request.Unit != "" {
			return operatorRequest{}, errors.New("read-file only accepts owner and path")
		}
		request.Path = cleaned
	case operatorOpWriteFile:
		cleaned, err := validateRelativeOwnerPath(request.Path, false)
		if err != nil {
			return operatorRequest{}, err
		}
		if request.Unit != "" {
			return operatorRequest{}, errors.New("write-file does not accept a unit")
		}
		if len(request.Content) > maximumOperatorFile {
			return operatorRequest{}, errors.New("operator file exceeds 256 KiB")
		}
		if !utf8.ValidString(request.Content) || strings.ContainsRune(request.Content, 0) {
			return operatorRequest{}, errors.New("operator file content is invalid")
		}
		if request.SHA256 != "" && !validSHA256Hex(request.SHA256) {
			return operatorRequest{}, errors.New("operator file sha256 is invalid")
		}
		request.Path = cleaned
	case operatorOpListDir:
		cleaned, err := validateRelativeOwnerPath(request.Path, true)
		if err != nil {
			return operatorRequest{}, err
		}
		if request.Content != "" || request.SHA256 != "" || request.Unit != "" {
			return operatorRequest{}, errors.New("list-dir only accepts owner and path")
		}
		request.Path = cleaned
	case operatorOpUnitStatus, operatorOpUnitRestart:
		if !systemdActionResourcePattern.MatchString(request.Unit) {
			return operatorRequest{}, errors.New("operator unit name is invalid")
		}
		if deniedSystemUnit(request.Unit) {
			return operatorRequest{}, errors.New("operator unit is a denied system service")
		}
		if request.Path != "" || request.Content != "" || request.SHA256 != "" {
			return operatorRequest{}, errors.New("unit operations only accept owner and unit")
		}
	default:
		return operatorRequest{}, errors.New("operator operation is not supported")
	}
	return request, nil
}

func validateRelativeOwnerPath(rel string, allowEmpty bool) (string, error) {
	if rel == "" || rel == "." {
		if allowEmpty {
			return "", nil
		}
		return "", errors.New("operator path is required")
	}
	if strings.Contains(rel, `\`) || strings.Contains(rel, "\x00") || path.IsAbs(rel) {
		return "", errors.New("operator path is invalid")
	}
	if !utf8.ValidString(rel) || hasActionControl(rel) {
		return "", errors.New("operator path is invalid")
	}
	cleaned := path.Clean("/" + rel)
	if cleaned == "/" {
		return "", errors.New("operator path is invalid")
	}
	cleaned = cleaned[1:]
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("operator path escapes home")
	}
	if credentialOwnerPath(cleaned) {
		return "", errors.New("operator path is a denied credential location")
	}
	return cleaned, nil
}

func credentialOwnerPath(cleaned string) bool {
	parts := strings.Split(cleaned, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return true
		}
		if _, found := deniedPathComponents[part]; found {
			return true
		}
	}
	base := parts[len(parts)-1]
	if _, found := deniedBasenames[base]; found {
		return true
	}
	for _, prefix := range deniedPathPrefixes {
		if cleaned == prefix || strings.HasPrefix(cleaned, prefix+"/") {
			return true
		}
	}
	return false
}

func deniedSystemUnit(unit string) bool {
	if _, found := deniedSystemUnits[unit]; found {
		return true
	}
	for _, prefix := range deniedUnitPrefixes {
		if strings.HasPrefix(unit, prefix) {
			return true
		}
	}
	return false
}

func validSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if character >= '0' && character <= '9' || character >= 'a' && character <= 'f' {
			continue
		}
		return false
	}
	return true
}

func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func approvedOwnerHome(home string) bool {
	home = path.Clean(home)
	if !path.IsAbs(home) || home == "/" {
		return false
	}
	denied := []string{
		"/root", "/nonexistent", "/tmp", "/var", "/etc", "/usr", "/bin", "/sbin",
		"/dev", "/proc", "/sys", "/run", "/boot", "/lib", "/lib64",
	}
	for _, prefix := range denied {
		if home == prefix || strings.HasPrefix(home, prefix+"/") {
			return false
		}
	}
	return true
}

func emptyOperatorPolicy() operatorPolicy {
	return operatorPolicy{Version: operatorPolicyVersion, Owners: []string{}}
}
