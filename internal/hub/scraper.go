package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	s.scrapeAll(ctx)
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.scrapeAll(ctx)
		}
	}
}

func (s *Scraper) scrapeAll(ctx context.Context) {
	var wg sync.WaitGroup
	for _, a := range s.store.Agents() {
		wg.Add(1)
		go func(a AgentConfig) {
			defer wg.Done()
			s.scrapeOne(ctx, a)
		}(a)
	}
	wg.Wait()
}

func (s *Scraper) scrapeOne(ctx context.Context, a AgentConfig) {
	// ping —— 最轻量，先探活与版本。
	var ping shared.Ping
	if err := s.getJSON(ctx, a, "/v1/ping", &ping); err != nil {
		s.store.Update(a.ID, false, err.Error(), shared.Ping{}, nil, nil)
		return
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
		s.store.Update(a.ID, false, fmt.Sprintf("status: %v; services: %v", se1, se2), ping, nil, nil)
		return
	}
	var stPtr *shared.Status
	if se1 == nil {
		stPtr = &st
	}
	services := sv.Services
	if se2 != nil {
		services = nil // 端口维度缺失，但 status 有效
	}
	s.store.Update(a.ID, true, "", ping, stPtr, services)
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
