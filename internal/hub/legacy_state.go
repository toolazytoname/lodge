package hub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/toolazytoname/lodge/internal/domain"
)

const maxLegacyStateBytes = 4 << 20

type legacyStateFile struct {
	// Agent records are accepted only to read the old format. Their URLs and
	// tokens are never decoded into application config or written to SQLite.
	Agents      []json.RawMessage                `json:"agents"`
	Annotations map[string]map[string]Annotation `json:"annotations"`
}

type LegacyImportResult struct {
	Found               bool
	Performed           bool
	ImportedAnnotations int64
	SkippedUnknownHosts int
	LegacyAgentRecords  int
	Digest              string
}

// ImportLegacyState imports annotations from the former JSON snapshot exactly
// once. Agent connection records are intentionally ignored because the private
// config file is their only authority.
func (s *SQLiteStore) ImportLegacyState(ctx context.Context, path string) (LegacyImportResult, error) {
	if path == "" {
		return LegacyImportResult{}, nil
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return LegacyImportResult{}, nil
	}
	if err != nil {
		return LegacyImportResult{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return LegacyImportResult{}, errors.New("legacy state must be a regular file, not a symlink")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return LegacyImportResult{}, fmt.Errorf("legacy state %s must be owner-only (0600)", path)
	}
	if info.Size() > maxLegacyStateBytes {
		return LegacyImportResult{}, fmt.Errorf("legacy state exceeds %d bytes", maxLegacyStateBytes)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return LegacyImportResult{}, err
	}
	if len(contents) > maxLegacyStateBytes {
		return LegacyImportResult{}, fmt.Errorf("legacy state exceeds %d bytes", maxLegacyStateBytes)
	}
	var legacy legacyStateFile
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&legacy); err != nil {
		return LegacyImportResult{}, fmt.Errorf("decode legacy state: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return LegacyImportResult{}, fmt.Errorf("decode legacy state: %w", err)
	}

	sum := sha256.Sum256(contents)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	result := LegacyImportResult{Found: true, LegacyAgentRecords: len(legacy.Agents), Digest: digest}
	configured := make(map[string]struct{}, len(s.Agents()))
	for _, agent := range s.Agents() {
		configured[agent.ID] = struct{}{}
	}
	hostIDs := make([]string, 0, len(legacy.Annotations))
	for hostID := range legacy.Annotations {
		hostIDs = append(hostIDs, hostID)
	}
	sort.Strings(hostIDs)
	updatedAt := time.Now().UTC()
	var annotations []domain.Annotation
	for _, hostID := range hostIDs {
		byKey := legacy.Annotations[hostID]
		if _, exists := configured[hostID]; !exists {
			result.SkippedUnknownHosts += len(byKey)
			continue
		}
		keys := make([]string, 0, len(byKey))
		for key := range byKey {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			normalized, err := validateAnnotation(byKey[key])
			if err != nil {
				return LegacyImportResult{}, fmt.Errorf("legacy annotation %s/%s: %w", hostID, key, err)
			}
			annotations = append(annotations, domain.Annotation{
				HostID: domain.HostID(hostID), WorkloadKey: key,
				Alias: normalized.Alias, URL: normalized.URL, Hidden: normalized.Hidden,
				Notes: normalized.Notes, UpdatedAt: updatedAt,
			})
		}
	}
	performed, imported, err := s.database.ImportAnnotations(ctx, digest, annotations)
	if err != nil {
		return LegacyImportResult{}, err
	}
	result.Performed = performed
	result.ImportedAnnotations = imported
	if performed {
		if err := s.reloadAnnotations(ctx); err != nil {
			return LegacyImportResult{}, err
		}
	}
	return result, nil
}
