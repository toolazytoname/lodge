package hub

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	rateLimitThreshold = 5                // 前 N 次失败不锁
	rateLimitBaseDelay = 30 * time.Second // 超过阈值后，锁定时长的起点
	rateLimitMaxDelay  = 30 * time.Minute // 锁定时长上限
	rateLimitIdleTTL   = time.Hour        // 超过这么久没有新尝试就清理掉该 IP 的记录

	// globalLimiterKey 是全局失败计数的 map key，与真实 IP 的命名空间不冲突
	// （IP/host 不含空格）。公网走 Vercel 转发到 Tailscale Funnel，中间代理
	// 未必老实透传真实客户端 IP（实测 X-Forwarded-For 首个地址逐请求跳变），
	// 按 IP 分桶单独用不住——所有失败都额外计入这个全局桶兜底，任一边先触发
	// 阈值就锁，不依赖代理链是否可信。
	globalLimiterKey = "* global *"
)

type loginAttempt struct {
	failures    int
	lockedUntil time.Time
	lastAttempt time.Time
}

// loginLimiter 按客户端 IP 记连续登录失败次数，超过阈值后指数退避锁定。
// 内存态，hub 重启即清空——可接受，重启本身已是运维介入。
type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string]*loginAttempt
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{attempts: make(map[string]*loginAttempt)}
}

// locked 返回该 IP（或全局桶）当前是否被锁定，及剩余锁定时长。
func (l *loginLimiter) locked(ip string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if wait, ok := l.lockedLocked(globalLimiterKey); ok {
		return wait, true
	}
	return l.lockedLocked(ip)
}

// lockedLocked 是 locked 去掉加锁的内部版本，调用方需已持有 l.mu。
func (l *loginLimiter) lockedLocked(key string) (time.Duration, bool) {
	a, ok := l.attempts[key]
	if !ok {
		return 0, false
	}
	if wait := time.Until(a.lockedUntil); wait > 0 {
		return wait, true
	}
	return 0, false
}

// recordFailure 记一次失败：同时计入该 IP 与全局两个桶，任一个超过阈值
// 就按指数退避设置锁定期。
func (l *loginLimiter) recordFailure(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.recordFailureLocked(ip)
	if ip != globalLimiterKey {
		l.recordFailureLocked(globalLimiterKey)
	}
}

func (l *loginLimiter) recordFailureLocked(key string) {
	a, ok := l.attempts[key]
	if !ok {
		a = &loginAttempt{}
		l.attempts[key] = a
	}
	a.failures++
	a.lastAttempt = time.Now()
	if a.failures > rateLimitThreshold {
		delay := rateLimitBaseDelay
		for i := 0; i < a.failures-rateLimitThreshold-1 && delay < rateLimitMaxDelay; i++ {
			delay *= 2
		}
		if delay > rateLimitMaxDelay {
			delay = rateLimitMaxDelay
		}
		a.lockedUntil = time.Now().Add(delay)
	}
}

// recordSuccess 登录成功后清空该 IP 与全局桶的失败记录。
func (l *loginLimiter) recordSuccess(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, ip)
	delete(l.attempts, globalLimiterKey)
}

// cleanup 清掉长期不活跃的记录，防止内存无限增长。
func (l *loginLimiter) cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for ip, a := range l.attempts {
		if now.Sub(a.lastAttempt) > rateLimitIdleTTL {
			delete(l.attempts, ip)
		}
	}
}

// runCleanup 后台定期清理，ctx 取消时退出。
func (l *loginLimiter) runCleanup(ctx context.Context) {
	ticker := time.NewTicker(rateLimitIdleTTL)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.cleanup()
		}
	}
}

// clientIP 取登录限速要用的客户端标识。hub 只监听 127.0.0.1，公网请求经
// Tailscale Funnel 反代到本机回环连接，真实来源 IP 在 X-Forwarded-For 里
// ——只在连接确实来自回环地址时才信任该头，避免非回环场景下被伪造。
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if host == "127.0.0.1" || host == "::1" {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if first := strings.TrimSpace(strings.Split(xff, ",")[0]); first != "" {
				return first
			}
		}
	}
	return host
}
