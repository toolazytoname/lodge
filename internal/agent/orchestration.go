package agent

import (
	"encoding/json"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

type composeMetadata struct {
	Project string
	Service string
}

var composeLabelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

var dockerComposeQuery = []string{
	"docker", "ps", "--all", "--no-trunc", "--format",
	`[{{json .ID}},{{json (.Label "com.docker.compose.project")}},{{json (.Label "com.docker.compose.service")}}]`,
}

// parseComposeMetadata accepts only the two official Compose identity labels
// emitted by docker ps. It deliberately never reads the full label map,
// working_dir, config_files, environment, or container command line.
func parseComposeMetadata(content []byte) map[string]composeMetadata {
	result := make(map[string]composeMetadata)
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var tuple []string
		if err := json.Unmarshal([]byte(line), &tuple); err != nil || len(tuple) != 3 {
			continue
		}
		containerID, project, service := tuple[0], tuple[1], tuple[2]
		if !validContainerID(containerID) || !composeLabelPattern.MatchString(project) {
			continue
		}
		if service != "" && !composeLabelPattern.MatchString(service) {
			continue
		}
		result[containerID] = composeMetadata{Project: project, Service: service}
	}
	return result
}

func writeComposeMetadata(content []byte, writer io.Writer) error {
	metadata := parseComposeMetadata(content)
	containerIDs := make([]string, 0, len(metadata))
	for containerID := range metadata {
		containerIDs = append(containerIDs, containerID)
	}
	sort.Strings(containerIDs)
	encoder := json.NewEncoder(writer)
	for _, containerID := range containerIDs {
		value := metadata[containerID]
		if err := encoder.Encode([]string{containerID, value.Project, value.Service}); err != nil {
			return err
		}
	}
	return nil
}

func validContainerID(value string) bool {
	if len(value) < 12 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func findComposeMetadata(metadata map[string]composeMetadata, containerID string) (composeMetadata, bool) {
	if value, found := metadata[containerID]; found {
		return value, true
	}
	for metadataID, value := range metadata {
		if strings.HasPrefix(metadataID, containerID) || strings.HasPrefix(containerID, metadataID) {
			return value, true
		}
	}
	return composeMetadata{}, false
}

type systemdUnitMetadata struct {
	ID           string
	LoadState    string
	ActiveState  string
	SubState     string
	FragmentPath string
}

func parseSystemdUnits(content []byte) []systemdUnitMetadata {
	var result []systemdUnitMetadata
	current := make(map[string]string)
	flush := func() {
		unit := systemdUnitMetadata{
			ID: current["Id"], LoadState: current["LoadState"], ActiveState: current["ActiveState"],
			SubState: current["SubState"], FragmentPath: current["FragmentPath"],
		}
		if unit.valid() {
			result = append(result, unit)
		}
		current = make(map[string]string)
	}
	for _, line := range strings.Split(string(content), "\n") {
		if line == "" {
			if len(current) > 0 {
				flush()
			}
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if found {
			current[key] = value
		}
	}
	if len(current) > 0 {
		flush()
	}
	return result
}

func (unit systemdUnitMetadata) valid() bool {
	if unit.LoadState != "loaded" || !strings.HasSuffix(unit.ID, ".service") || len(unit.ID) > 256 || !utf8.ValidString(unit.ID) {
		return false
	}
	if strings.Contains(unit.ID, "/") {
		return false
	}
	for _, character := range unit.ID {
		if character == 0 || unicode.IsControl(character) {
			return false
		}
	}
	switch unit.ActiveState {
	case "active", "activating", "deactivating", "reloading", "inactive", "failed":
		return true
	default:
		return false
	}
}

func (unit systemdUnitMetadata) custom() bool {
	if !filepath.IsAbs(unit.FragmentPath) {
		return false
	}
	cleaned := filepath.Clean(unit.FragmentPath)
	return strings.HasPrefix(cleaned, "/etc/systemd/system/") ||
		strings.HasPrefix(cleaned, "/usr/local/lib/systemd/system/") ||
		strings.HasPrefix(cleaned, "/usr/local/etc/systemd/system/")
}

// relevant means either an operator-managed active unit or any failed unit.
// Inactive package units are intentionally omitted to avoid hundreds of noisy
// entries; a failed package unit is always surfaced regardless of origin.
func (unit systemdUnitMetadata) relevant() bool {
	if unit.ActiveState == "failed" {
		return true
	}
	return unit.custom() && (unit.ActiveState == "active" || unit.ActiveState == "activating" ||
		unit.ActiveState == "deactivating" || unit.ActiveState == "reloading")
}

func (unit systemdUnitMetadata) status() string {
	if unit.SubState == "" || unit.SubState == unit.ActiveState {
		return unit.ActiveState
	}
	return unit.ActiveState + "/" + unit.SubState
}
