//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var dockerProxyQuery = []string{
	"docker", "ps", "--no-trunc", "--format",
	`[{{json .ID}},{{json .Names}},{{json .Image}}]`,
}

type dockerMount struct {
	Type        string `json:"Type"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
}

// WriteProxyRoutes reads only standard proxy configuration locations and
// Docker bind-mount metadata, then emits validated route records. Raw config,
// certificates, keys, headers, environment variables, and config paths never
// leave this root-owned helper.
func WriteProxyRoutes(writer io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("proxy route collector must run as root through the exact sudoers rule")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	discovery := collectProxyRoutes(ctx)
	return writeProxyDiscovery(discovery, writer)
}

func collectProxyRoutes(ctx context.Context) proxyDiscovery {
	var discovery proxyDiscovery

	if content, found, err := readBoundedRegularFile("/etc/caddy/Caddyfile"); err != nil {
		discovery.Warnings = append(discovery.Warnings, "host Caddy config is not a safe regular file")
	} else if found {
		routes, parseErr := parseCaddyRoutes(content, "systemd:caddy.service")
		if parseErr != nil {
			discovery.Warnings = append(discovery.Warnings, proxyParseWarning("Caddy", "host"))
		} else {
			discovery.Routes = append(discovery.Routes, routes...)
		}
		if containsConfigImport(content) {
			discovery.Warnings = append(discovery.Warnings, "host Caddy imports are not expanded")
		}
	}

	if nginxPath, err := exec.LookPath("nginx"); err == nil {
		stdout, runErr := runBoundedProxyCommand(ctx, []string{nginxPath, "-T"})
		if runErr != nil {
			discovery.Warnings = append(discovery.Warnings, "host Nginx effective config could not be read")
		} else if routes, parseErr := parseNginxRoutes(stdout, "systemd:nginx.service"); parseErr != nil {
			discovery.Warnings = append(discovery.Warnings, proxyParseWarning("Nginx", "host"))
		} else {
			discovery.Routes = append(discovery.Routes, routes...)
		}
	}

	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return discovery
	}
	query := append([]string{dockerPath}, dockerProxyQuery[1:]...)
	stdout, err := runBoundedProxyCommand(ctx, query)
	if err != nil {
		discovery.Warnings = append(discovery.Warnings, "Docker proxy inventory could not be read")
		return discovery
	}
	for _, candidate := range parseDockerProxyCandidates(stdout) {
		kind := proxyKind(candidate[1], candidate[2])
		if kind == "" {
			continue
		}
		mounts, inspectErr := inspectDockerMounts(ctx, dockerPath, candidate[0])
		if inspectErr != nil {
			discovery.Warnings = append(discovery.Warnings, "Docker "+kind+" bind mounts could not be inspected")
			continue
		}
		destination := "/etc/" + kind + "/" + map[string]string{"caddy": "Caddyfile", "nginx": "nginx.conf"}[kind]
		source := ""
		for _, mount := range mounts {
			if mount.Type == "bind" && mount.Destination == destination {
				source = mount.Source
				break
			}
		}
		if source == "" {
			discovery.Warnings = append(discovery.Warnings, "Docker "+kind+" config is not a supported bind-mounted file")
			continue
		}
		content, found, readErr := readBoundedRegularFile(source)
		if readErr != nil || !found {
			discovery.Warnings = append(discovery.Warnings, "Docker "+kind+" config is not a safe regular file")
			continue
		}
		workloadKey := "docker:" + candidate[1]
		if kind == "caddy" {
			routes, parseErr := parseCaddyRoutes(content, workloadKey)
			if parseErr != nil {
				discovery.Warnings = append(discovery.Warnings, proxyParseWarning("Caddy", "Docker"))
				continue
			}
			discovery.Routes = append(discovery.Routes, routes...)
			if containsConfigImport(content) {
				discovery.Warnings = append(discovery.Warnings, "Docker Caddy imports are not expanded")
			}
		} else {
			routes, parseErr := parseNginxRoutes(content, workloadKey)
			if parseErr != nil {
				discovery.Warnings = append(discovery.Warnings, proxyParseWarning("Nginx", "Docker"))
				continue
			}
			discovery.Routes = append(discovery.Routes, routes...)
			if containsConfigImport(content) {
				discovery.Warnings = append(discovery.Warnings, "Docker Nginx includes are not expanded")
			}
		}
	}
	discovery.Routes = mergeProxyRoutes(discovery.Routes)
	discovery.Warnings = uniqueSorted(discovery.Warnings)
	if len(discovery.Warnings) > maxProxyWarnings {
		discovery.Warnings = discovery.Warnings[:maxProxyWarnings]
	}
	return discovery
}

func runBoundedProxyCommand(ctx context.Context, argv []string) ([]byte, error) {
	if len(argv) == 0 {
		return nil, errors.New("empty proxy command")
	}
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	stdout := boundedBuffer{limit: maxProxyConfigBytes}
	stderr := boundedBuffer{limit: maxPrivilegedStderr}
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("proxy command timed out")
		}
		return nil, errors.New("proxy command failed")
	}
	if stdout.exceeded || stderr.exceeded {
		return nil, errors.New("proxy command exceeded its output limit")
	}
	return stdout.Bytes(), nil
}

func readBoundedRegularFile(path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxProxyConfigBytes {
		return nil, true, errors.New("proxy config is not a bounded regular file")
	}
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, true, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || openedInfo.Size() > maxProxyConfigBytes || !os.SameFile(info, openedInfo) {
		return nil, true, errors.New("proxy config changed during safe open")
	}
	content, err := io.ReadAll(io.LimitReader(file, maxProxyConfigBytes+1))
	if err != nil || len(content) > maxProxyConfigBytes {
		return nil, true, errors.New("proxy config exceeded its read limit")
	}
	return content, true, nil
}

func parseDockerProxyCandidates(content []byte) [][3]string {
	var result [][3]string
	for _, line := range strings.Split(string(content), "\n") {
		if len(result) >= 128 || strings.TrimSpace(line) == "" {
			continue
		}
		var tuple []string
		if json.Unmarshal([]byte(line), &tuple) != nil || len(tuple) != 3 || !validContainerID(tuple[0]) || !validContainerName(tuple[1]) {
			continue
		}
		result = append(result, [3]string{tuple[0], tuple[1], tuple[2]})
	}
	return result
}

func validContainerName(value string) bool {
	if len(value) < 1 || len(value) > 128 || !asciiAlphaNumeric(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		if !asciiAlphaNumeric(value[index]) && value[index] != '_' && value[index] != '.' && value[index] != '-' {
			return false
		}
	}
	return true
}

func proxyKind(name, image string) string {
	image = strings.TrimSpace(strings.ToLower(image))
	if at := strings.Index(image, "@"); at >= 0 {
		image = image[:at]
	}
	if slash := strings.LastIndex(image, "/"); slash >= 0 {
		image = image[slash+1:]
	}
	if colon := strings.Index(image, ":"); colon >= 0 {
		image = image[:colon]
	}
	name = strings.ToLower(name)
	for _, kind := range []string{"caddy", "nginx"} {
		if image == kind || name == kind || strings.HasPrefix(name, kind+"-") || strings.HasSuffix(name, "-"+kind) {
			return kind
		}
	}
	return ""
}

func inspectDockerMounts(ctx context.Context, dockerPath, containerID string) ([]dockerMount, error) {
	stdout, err := runBoundedProxyCommand(ctx, []string{dockerPath, "inspect", "--format", "{{json .Mounts}}", containerID})
	if err != nil {
		return nil, err
	}
	var mounts []dockerMount
	if err := json.Unmarshal(stdout, &mounts); err != nil {
		return nil, errors.New("Docker mount metadata is invalid")
	}
	if len(mounts) > 128 {
		return nil, errors.New("Docker mount count exceeds limit")
	}
	return mounts, nil
}

func containsConfigImport(content []byte) bool {
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "import ") || strings.HasPrefix(line, "include ") {
			return true
		}
	}
	return false
}
