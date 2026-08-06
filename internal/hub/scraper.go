package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/toolazytoname/lodge/internal/shared"
)

// Scraper 周期性并发拉取各 agent，把结果写回 Store。
// 单台失败不影响其他台 —— 一台挂了，其余照常上报，前端只看到那台离线。
type Scraper struct {
	store    Store
	client   *http.Client
	interval time.Duration
}

func NewScraper(store Store, interval time.Duration) *Scraper {
	return &Scraper{
		store:    store,
		interval: interval,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// Run 阻塞运行采集循环，直到 ctx 取消。首次立即采集一轮，之后按 interval。
func (s *Scraper) Run(ctx context.Context) {
	if err := s.scrapeAll(ctx); err != nil {
		log.Printf("lodge hub scrape: %v", err)
	}
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.scrapeAll(ctx); err != nil {
				log.Printf("lodge hub scrape: %v", err)
			}
		}
	}
}

func (s *Scraper) scrapeAll(ctx context.Context) error {
	var wg sync.WaitGroup
	var errorMu sync.Mutex
	var scrapeErrors []error
	for _, a := range s.store.Agents() {
		wg.Add(1)
		go func(a AgentConfig) {
			defer wg.Done()
			if err := s.scrapeOne(ctx, a); err != nil {
				errorMu.Lock()
				scrapeErrors = append(scrapeErrors, err)
				errorMu.Unlock()
			}
		}(a)
	}
	wg.Wait()
	return errors.Join(scrapeErrors...)
}

func (s *Scraper) scrapeOne(ctx context.Context, a AgentConfig) error {
	observedAt := time.Now().UTC()
	// ping —— 最轻量，先探活与版本。
	var ping shared.Ping
	if err := s.getJSON(ctx, a, "/v1/ping", &ping); err != nil {
		persistErr := s.store.Update(ctx, a.ID, false, err.Error(), shared.Ping{}, nil, nil, observedAt)
		return errors.Join(fmt.Errorf("agent %s ping: %w", a.ID, err), persistErr)
	}
	if ping.APIVersion != shared.APIVersion {
		compatibilityErr := fmt.Errorf("agent %s API version %q is incompatible; Hub requires %q", a.ID, ping.APIVersion, shared.APIVersion)
		persistErr := s.store.Update(ctx, a.ID, false, compatibilityErr.Error(), ping, nil, nil, observedAt)
		return errors.Join(compatibilityErr, persistErr)
	}

	// status 与 services 并发拉。
	var (
		st       shared.Status
		sv       shared.ServicesResponse
		se1, se2 error
	)
	var iwg sync.WaitGroup
	iwg.Add(2)
	go func() { defer iwg.Done(); se1 = s.getJSON(ctx, a, "/v1/status", &st) }()
	go func() { defer iwg.Done(); se2 = s.getJSON(ctx, a, "/v1/services", &sv) }()
	iwg.Wait()

	// status/services 任一成功即认为在线；两者都失败才算离线。
	if se1 != nil && se2 != nil {
		collectionErr := fmt.Errorf("agent %s status and services failed: status: %v; services: %v", a.ID, se1, se2)
		persistErr := s.store.Update(ctx, a.ID, false, collectionErr.Error(), ping, nil, nil, observedAt)
		return errors.Join(collectionErr, persistErr)
	}
	var stPtr *shared.Status
	if se1 == nil {
		stPtr = &st
	}
	services := sv.Services
	if se2 != nil {
		services = nil // 端口维度缺失，但 status 有效
	}
	if err := s.store.Update(ctx, a.ID, true, "", ping, stPtr, services, observedAt); err != nil {
		return err
	}
	if se1 != nil || se2 != nil {
		return fmt.Errorf("agent %s partial observation: status: %v; services: %v", a.ID, se1, se2)
	}
	return nil
}

// getJSON 带 Bearer token 请求 agent 并解码。非 2xx 视为失败。
func (s *Scraper) getJSON(ctx context.Context, a AgentConfig, path string, out interface{}) error {
	url := strings.TrimRight(a.URL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s: HTTP %d %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
