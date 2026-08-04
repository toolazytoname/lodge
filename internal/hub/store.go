package hub

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/toolazytoname/lodge/internal/shared"
)

// Store 是 hub 的数据层抽象。P0 实现是内存 + JSON 快照（MemStore）；
// P3 换 SQLite 时只需新增一个实现，上层（scraper/server）不动。
type Store interface {
	SetAgents([]AgentConfig)
	Agents() []AgentConfig
	// Update 用一次采集结果覆盖某 agent 的观测快照。
	Update(id string, online bool, lastErr string, ping shared.Ping, status *shared.Status, services []shared.Service)
	Snapshot() []AgentSnapshot
	Annotations(agentID string) map[string]Annotation
	SetAnnotation(agentID, key string, ann Annotation)
}

// snapshotFile 是 MemStore 落盘的内容。重启后恢复 online 状态为 false
// （真相要等下一轮采集），但保留 agents 配置与用户注解。
type snapshotFile struct {
	Agents      []AgentConfig                    `json:"agents"`
	Annotations map[string]map[string]Annotation `json:"annotations"` // agentID -> key -> ann
}

// MemStore 线程安全地保存 agents/观测/注解，并周期性把注解与配置落盘。
// 观测数据不落盘（本就是每轮刷新的临时真相，重启后首轮采集即恢复）。
type MemStore struct {
	mu     sync.RWMutex
	agents []AgentConfig
	// agentID -> snapshot
	snaps map[string]*AgentSnapshot
	// agentID -> key -> annotation
	anns map[string]map[string]Annotation

	path       string
	lastSaveAt time.Time
}

func NewMemStore(path string) *MemStore {
	s := &MemStore{
		path:  path,
		snaps: map[string]*AgentSnapshot{},
		anns:  map[string]map[string]Annotation{},
	}
	s.load()
	return s
}

func (s *MemStore) load() {
	if s.path == "" {
		return
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var f snapshotFile
	if json.Unmarshal(b, &f) != nil {
		return
	}
	s.agents = f.Agents
	if f.Annotations != nil {
		s.anns = f.Annotations
	}
	// 预建空快照，online=false，等采集刷新。
	for _, a := range s.agents {
		s.snaps[a.ID] = &AgentSnapshot{ID: a.ID, Name: a.Name, Online: false}
	}
}

// Save 把配置与注解写盘。观测快照不写（临时真相）。调用方应节流。
func (s *MemStore) Save() error {
	if s.path == "" {
		return nil
	}
	s.mu.RLock()
	f := snapshotFile{Agents: s.agents, Annotations: s.anns}
	s.mu.RUnlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *MemStore) SetAgents(cfg []AgentConfig) {
	s.mu.Lock()
	s.agents = cfg
	for _, a := range cfg {
		if _, ok := s.snaps[a.ID]; !ok {
			s.snaps[a.ID] = &AgentSnapshot{ID: a.ID, Name: a.Name}
		}
		s.snaps[a.ID].Name = a.Name
	}
	s.mu.Unlock()
	_ = s.Save()
}

func (s *MemStore) Agents() []AgentConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]AgentConfig, len(s.agents))
	copy(out, s.agents)
	return out
}

func (s *MemStore) Update(id string, online bool, lastErr string, ping shared.Ping, status *shared.Status, services []shared.Service) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sn, ok := s.snaps[id]
	if !ok {
		sn = &AgentSnapshot{ID: id}
		s.snaps[id] = sn
	}
	sn.Online = online
	sn.LastError = lastErr
	sn.AgentVer = ping.AgentVer
	sn.Status = status
	sn.Services = services
	if online {
		sn.LastSeen = time.Now().UTC().Format(time.RFC3339)
	}
}

func (s *MemStore) Snapshot() []AgentSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]AgentSnapshot, 0, len(s.snaps))
	for _, a := range s.agents { // 按配置顺序输出，而非 map 随机序
		if sn, ok := s.snaps[a.ID]; ok {
			out = append(out, *sn)
		}
	}
	return out
}

func (s *MemStore) Annotations(agentID string) map[string]Annotation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.anns[agentID]
	out := make(map[string]Annotation, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func (s *MemStore) SetAnnotation(agentID, key string, ann Annotation) {
	s.mu.Lock()
	if s.anns[agentID] == nil {
		s.anns[agentID] = map[string]Annotation{}
	}
	ann.Key = key
	ann.AgentID = agentID
	s.anns[agentID][key] = ann
	s.mu.Unlock()
	_ = s.Save()
}
