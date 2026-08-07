package hub

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/toolazytoname/lodge/internal/domain"
	"github.com/toolazytoname/lodge/internal/shared"
)

// Store is the Hub application boundary. Agent connection credentials and the
// latest UI projection stay in memory; implementations may durably persist
// observations and annotations behind these methods.
type Store interface {
	SetAgents(context.Context, []AgentConfig) error
	Agents() []AgentConfig
	Update(context.Context, string, bool, string, shared.Ping, *shared.Status, []shared.Service, time.Time) error
	Snapshot() []AgentSnapshot
	Annotations(agentID string) map[string]Annotation
	SetAnnotation(context.Context, string, string, Annotation) error
	WebLinkChecks(context.Context) ([]domain.WebLinkCheck, error)
	ReplaceWebLinkChecks(context.Context, []domain.WebLinkCheck) error
}

// MemStore is the non-durable runtime projection used by tests and wrapped by
// SQLiteStore in production. It deliberately has no file path or Save method,
// so Agent bearer tokens cannot accidentally be serialized by this layer.
type MemStore struct {
	mu     sync.RWMutex
	agents []AgentConfig
	snaps  map[string]*AgentSnapshot
	anns   map[string]map[string]Annotation
	checks []domain.WebLinkCheck
}

func NewMemStore() *MemStore {
	return &MemStore{
		snaps: make(map[string]*AgentSnapshot),
		anns:  make(map[string]map[string]Annotation),
	}
}

func (s *MemStore) SetAgents(ctx context.Context, configured []AgentConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	agents := append([]AgentConfig(nil), configured...)
	s.mu.Lock()
	s.agents = agents
	for _, agent := range agents {
		if _, exists := s.snaps[agent.ID]; !exists {
			s.snaps[agent.ID] = &AgentSnapshot{ID: agent.ID}
		}
		s.snaps[agent.ID].Name = agent.Name
	}
	s.mu.Unlock()
	return nil
}

func (s *MemStore) Agents() []AgentConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]AgentConfig(nil), s.agents...)
}

func (s *MemStore) Update(ctx context.Context, id string, online bool, lastError string, ping shared.Ping, status *shared.Status, services []shared.Service, observedAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, exists := s.snaps[id]
	if !exists {
		snapshot = &AgentSnapshot{ID: id}
		s.snaps[id] = snapshot
	}
	snapshot.Online = online
	snapshot.LastError = lastError
	snapshot.AgentVer = ping.AgentVer
	snapshot.Status = status
	snapshot.Services = services
	if online {
		snapshot.LastSeen = observedAt.UTC().Format(time.RFC3339)
	}
	return nil
}

func (s *MemStore) Snapshot() []AgentSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]AgentSnapshot, 0, len(s.agents))
	for _, agent := range s.agents {
		if snapshot, exists := s.snaps[agent.ID]; exists {
			result = append(result, *snapshot)
		}
	}
	return result
}

func (s *MemStore) Annotations(agentID string) map[string]Annotation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	source := s.anns[agentID]
	result := make(map[string]Annotation, len(source))
	for key, annotation := range source {
		result[key] = annotation
	}
	return result
}

func (s *MemStore) SetAnnotation(ctx context.Context, agentID, key string, annotation Annotation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.anns[agentID] == nil {
		s.anns[agentID] = make(map[string]Annotation)
	}
	annotation.Key = key
	annotation.AgentID = agentID
	s.anns[agentID][key] = annotation
	return nil
}

func (s *MemStore) WebLinkChecks(ctx context.Context) ([]domain.WebLinkCheck, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]domain.WebLinkCheck(nil), s.checks...), nil
}

func (s *MemStore) ReplaceWebLinkChecks(ctx context.Context, checks []domain.WebLinkCheck) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	seen := make(map[[3]string]struct{}, len(checks))
	for _, check := range checks {
		if err := check.Validate(); err != nil {
			return err
		}
		key := [3]string{string(check.HostID), check.WorkloadKey, check.URL}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate Web link check %s/%s", check.HostID, check.WorkloadKey)
		}
		seen[key] = struct{}{}
	}
	s.mu.Lock()
	s.checks = append([]domain.WebLinkCheck(nil), checks...)
	s.mu.Unlock()
	return nil
}
