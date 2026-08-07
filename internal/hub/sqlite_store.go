package hub

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/toolazytoname/lodge/internal/domain"
	"github.com/toolazytoname/lodge/internal/shared"
	"github.com/toolazytoname/lodge/internal/storage"
)

// SQLiteStore combines an owner-private durable database with the in-memory
// projection required by the current v1 Web API. Agent URLs and bearer tokens
// exist only in the runtime projection.
type SQLiteStore struct {
	database *storage.SQLite
	runtime  *MemStore
}

func OpenSQLiteStore(ctx context.Context, path string, agents []AgentConfig) (*SQLiteStore, error) {
	database, err := storage.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	store := &SQLiteStore{database: database, runtime: NewMemStore()}
	if err := store.SetAgents(ctx, agents); err != nil {
		_ = database.Close()
		return nil, err
	}
	if err := store.reloadAnnotations(ctx); err != nil {
		_ = database.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) Close() error {
	return s.database.Close()
}

func (s *SQLiteStore) SetAgents(ctx context.Context, configured []AgentConfig) error {
	hosts := make([]domain.Host, 0, len(configured))
	for _, agent := range configured {
		hosts = append(hosts, domain.Host{
			ID: domain.HostID(agent.ID), Name: agent.Name, PublicHost: agent.PublicHost,
		})
	}
	if err := s.database.SyncHosts(ctx, hosts); err != nil {
		return fmt.Errorf("sync configured hosts: %w", err)
	}
	// The durable transaction has committed; keep its runtime projection in
	// sync even if the request context is cancelled immediately afterwards.
	return s.runtime.SetAgents(context.Background(), configured)
}

func (s *SQLiteStore) Agents() []AgentConfig {
	return s.runtime.Agents()
}

func (s *SQLiteStore) Update(ctx context.Context, id string, online bool, lastError string, ping shared.Ping, status *shared.Status, services []shared.Service, observedAt time.Time) error {
	agent, found := s.agent(id)
	if !found {
		return fmt.Errorf("update unknown agent %q", id)
	}
	observation, err := projectObservation(agent, online, lastError, ping, status, services, observedAt)
	if err != nil {
		return fmt.Errorf("project agent %s observation: %w", id, err)
	}
	if _, err := s.database.RecordObservation(ctx, observation); err != nil {
		return fmt.Errorf("persist agent %s observation: %w", id, err)
	}
	return s.runtime.Update(context.Background(), id, online, lastError, ping, status, services, observedAt)
}

func (s *SQLiteStore) Snapshot() []AgentSnapshot {
	return s.runtime.Snapshot()
}

func (s *SQLiteStore) Annotations(agentID string) map[string]Annotation {
	return s.runtime.Annotations(agentID)
}

func (s *SQLiteStore) SetAnnotation(ctx context.Context, agentID, key string, annotation Annotation) error {
	durable := domain.Annotation{
		HostID: domain.HostID(agentID), WorkloadKey: key,
		Alias: annotation.Alias, URL: annotation.URL, Hidden: annotation.Hidden,
		Notes: annotation.Notes, UpdatedAt: time.Now().UTC(),
	}
	if err := s.database.UpsertAnnotation(ctx, durable); err != nil {
		return err
	}
	return s.runtime.SetAnnotation(context.Background(), agentID, key, annotation)
}

func (s *SQLiteStore) WebLinkChecks(ctx context.Context) ([]domain.WebLinkCheck, error) {
	return s.database.WebLinkChecks(ctx)
}

func (s *SQLiteStore) ReplaceWebLinkChecks(ctx context.Context, checks []domain.WebLinkCheck) error {
	return s.database.ReplaceWebLinkChecks(ctx, checks)
}

func (s *SQLiteStore) Backup(ctx context.Context, destination string) error {
	return s.database.Backup(ctx, destination)
}

func (s *SQLiteStore) PruneObservations(ctx context.Context, before time.Time) (int64, error) {
	return s.database.PruneObservations(ctx, before)
}

func (s *SQLiteStore) ObservationHistory(ctx context.Context, hostID domain.HostID, limit int) ([]domain.Observation, error) {
	return s.database.ObservationHistory(ctx, hostID, limit)
}

func (s *SQLiteStore) ObservationSummaryHistory(ctx context.Context, hostID domain.HostID, limit int) ([]domain.ObservationSummary, error) {
	return s.database.ObservationSummaryHistory(ctx, hostID, limit)
}

func (s *SQLiteStore) RunObservationRetention(ctx context.Context, retention time.Duration) {
	if retention <= 0 {
		return
	}
	prune := func() {
		deleted, err := s.PruneObservations(ctx, time.Now().UTC().Add(-retention))
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("lodge hub observation retention: %v", err)
			}
			return
		}
		if deleted > 0 {
			log.Printf("lodge hub observation retention: pruned %d rows", deleted)
		}
	}
	prune()
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prune()
		}
	}
}

func BackupSQLiteDatabase(ctx context.Context, source, destination string) error {
	_, err := storage.BackupSQLiteFile(ctx, source, destination)
	return err
}

func (s *SQLiteStore) agent(id string) (AgentConfig, bool) {
	for _, agent := range s.runtime.Agents() {
		if agent.ID == id {
			return agent, true
		}
	}
	return AgentConfig{}, false
}

func (s *SQLiteStore) reloadAnnotations(ctx context.Context) error {
	for _, agent := range s.runtime.Agents() {
		annotations, err := s.database.Annotations(ctx, domain.HostID(agent.ID))
		if err != nil {
			return fmt.Errorf("load annotations for %s: %w", agent.ID, err)
		}
		for _, durable := range annotations {
			annotation := Annotation{
				Key: durable.WorkloadKey, AgentID: string(durable.HostID), Alias: durable.Alias,
				URL: durable.URL, Hidden: durable.Hidden, Notes: durable.Notes,
			}
			if err := s.runtime.SetAnnotation(context.Background(), agent.ID, durable.WorkloadKey, annotation); err != nil {
				return err
			}
		}
	}
	return nil
}
