package hub

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
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
	ObservationSummaryHistory(context.Context, domain.HostID, int) ([]domain.ObservationSummary, error)
	Events(context.Context, domain.HostID, int) ([]domain.Event, error)
	AcknowledgeEvent(context.Context, string, time.Time) (domain.Event, bool, error)
	WebLinkChecks(context.Context) ([]domain.WebLinkCheck, error)
	ReplaceWebLinkChecks(context.Context, []domain.WebLinkCheck) error
	CreateOperation(context.Context, domain.Operation) error
	StartOperation(context.Context, string, time.Time) (domain.Operation, bool, error)
	FinishOperation(context.Context, string, domain.OperationState, time.Time, string, string) (domain.Operation, bool, error)
	Operations(context.Context, domain.HostID, int) ([]domain.Operation, error)
}

var ErrEventResolved = errors.New("event already resolved")
var ErrOperationState = errors.New("operation state transition is invalid")

// MemStore is the non-durable runtime projection used by tests and wrapped by
// SQLiteStore in production. It deliberately has no file path or Save method,
// so Agent bearer tokens cannot accidentally be serialized by this layer.
type MemStore struct {
	mu         sync.RWMutex
	agents     []AgentConfig
	snaps      map[string]*AgentSnapshot
	anns       map[string]map[string]Annotation
	checks     []domain.WebLinkCheck
	history    map[string][]domain.ObservationSummary
	operations map[string]domain.Operation
}

func NewMemStore() *MemStore {
	return &MemStore{
		snaps:      make(map[string]*AgentSnapshot),
		anns:       make(map[string]map[string]Annotation),
		history:    make(map[string][]domain.ObservationSummary),
		operations: make(map[string]domain.Operation),
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
	summary, err := summarizeRuntimeUpdate(id, online, lastError, ping, status, services, observedAt)
	if err != nil {
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
	s.history[id] = append([]domain.ObservationSummary{summary}, s.history[id]...)
	if len(s.history[id]) > 1000 {
		s.history[id] = s.history[id][:1000]
	}
	return nil
}

func summarizeRuntimeUpdate(id string, online bool, lastError string, ping shared.Ping, status *shared.Status, services []shared.Service, observedAt time.Time) (domain.ObservationSummary, error) {
	summary := domain.ObservationSummary{
		HostID: domain.HostID(id), ObservedAt: observedAt.UTC(), Online: online,
		LastError: lastError, AgentVersion: ping.AgentVer, WorkloadCount: len(services),
	}
	for _, service := range services {
		if strings.EqualFold(strings.TrimSpace(service.Status), "failed") ||
			strings.EqualFold(strings.TrimSpace(service.Health), "unhealthy") {
			summary.FailedWorkloadCount++
		}
		for _, port := range service.Ports {
			if port.Exposure == shared.ExposurePublic {
				summary.WildcardEndpointCount++
			}
		}
	}
	if status != nil {
		summary.CPUs = status.Load.CPUs
		summary.Load1 = status.Load.One
		summary.MemoryUsedPct = domain.UsagePercent(status.Memory.UsedBytes, status.Memory.TotalBytes)
		summary.WarningCount = len(status.Warnings)
		for _, disk := range status.Disks {
			if disk.Mount == "/" {
				summary.DiskUsedPct = domain.UsagePercent(disk.UsedBytes, disk.TotalBytes)
				break
			}
		}
	}
	if err := summary.Validate(); err != nil {
		return domain.ObservationSummary{}, err
	}
	return summary, nil
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

func (s *MemStore) ObservationSummaryHistory(ctx context.Context, hostID domain.HostID, limit int) ([]domain.ObservationSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("history limit must be between 1 and 1000")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	available := s.history[string(hostID)]
	if limit > len(available) {
		limit = len(available)
	}
	return append([]domain.ObservationSummary(nil), available[:limit]...), nil
}

func (s *MemStore) Events(ctx context.Context, _ domain.HostID, limit int) ([]domain.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 500 {
		return nil, errors.New("event limit must be between 1 and 500")
	}
	return []domain.Event{}, nil
}

func (s *MemStore) AcknowledgeEvent(ctx context.Context, _ string, _ time.Time) (domain.Event, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.Event{}, false, err
	}
	return domain.Event{}, false, nil
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

func (s *MemStore) CreateOperation(ctx context.Context, operation domain.Operation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if operation.State != domain.OperationRequested {
		return ErrOperationState
	}
	if err := operation.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, duplicate := s.operations[operation.ID]; duplicate {
		return errors.New("operation ID already exists")
	}
	s.operations[operation.ID] = operation
	return nil
}

func (s *MemStore) StartOperation(ctx context.Context, id string, startedAt time.Time) (domain.Operation, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.Operation{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	operation, found := s.operations[id]
	if !found {
		return domain.Operation{}, false, nil
	}
	if operation.State != domain.OperationRequested {
		return domain.Operation{}, true, ErrOperationState
	}
	startedAt = startedAt.UTC()
	operation.State, operation.StartedAt = domain.OperationRunning, &startedAt
	if err := operation.Validate(); err != nil {
		return domain.Operation{}, true, err
	}
	s.operations[id] = operation
	return operation, true, nil
}

func (s *MemStore) FinishOperation(ctx context.Context, id string, state domain.OperationState, finishedAt time.Time, summary, errorKind string) (domain.Operation, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.Operation{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	operation, found := s.operations[id]
	if !found {
		return domain.Operation{}, false, nil
	}
	if operation.State != domain.OperationRunning || state != domain.OperationSucceeded && state != domain.OperationFailed && state != domain.OperationRolledBack {
		return domain.Operation{}, true, ErrOperationState
	}
	finishedAt = finishedAt.UTC()
	operation.State, operation.FinishedAt = state, &finishedAt
	operation.ResultSummary, operation.Error = summary, errorKind
	if err := operation.Validate(); err != nil {
		return domain.Operation{}, true, err
	}
	s.operations[id] = operation
	return operation, true, nil
}

func (s *MemStore) Operations(ctx context.Context, hostID domain.HostID, limit int) ([]domain.Operation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 500 {
		return nil, errors.New("operation limit must be between 1 and 500")
	}
	s.mu.RLock()
	operations := make([]domain.Operation, 0, len(s.operations))
	for _, operation := range s.operations {
		if hostID == "" || operation.HostID == hostID {
			operations = append(operations, operation)
		}
	}
	s.mu.RUnlock()
	sort.Slice(operations, func(left, right int) bool {
		if !operations[left].RequestedAt.Equal(operations[right].RequestedAt) {
			return operations[left].RequestedAt.After(operations[right].RequestedAt)
		}
		return operations[left].ID > operations[right].ID
	})
	if len(operations) > limit {
		operations = operations[:limit]
	}
	return operations, nil
}
