package hub

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/toolazytoname/lodge/internal/domain"
)

const (
	maxWebLinkProbeTargets  = 64
	webLinkProbeConcurrency = 8
	webLinkProbeTimeout     = 5 * time.Second
)

type webLinkTarget struct {
	hostID      domain.HostID
	workloadKey string
	url         string
}

type webLinkProbeRunner interface {
	Probe(context.Context, []webLinkTarget, time.Time) []domain.WebLinkCheck
}

// webLinkProber intentionally performs only a bounded HEAD request. It does
// not use environment proxies, follow redirects, attach credentials, or read
// response bodies. This is still an authenticated management-plane network
// capability and is never exposed without CSRF protection.
type webLinkProber struct {
	client      *http.Client
	concurrency int
}

func newWebLinkProber() *webLinkProber {
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: -1}).DialContext,
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   3 * time.Second,
		ResponseHeaderTimeout: 4 * time.Second,
		MaxIdleConns:          0,
	}
	return &webLinkProber{
		client: &http.Client{
			Transport: transport,
			Timeout:   webLinkProbeTimeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		concurrency: webLinkProbeConcurrency,
	}
}

func (prober *webLinkProber) Probe(ctx context.Context, targets []webLinkTarget, checkedAt time.Time) []domain.WebLinkCheck {
	checks := make([]domain.WebLinkCheck, len(targets))
	concurrency := prober.concurrency
	if concurrency < 1 {
		concurrency = 1
	}
	semaphore := make(chan struct{}, concurrency)
	var wait sync.WaitGroup
	wait.Add(len(targets))
	for index, target := range targets {
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
				checks[index] = prober.probeOne(ctx, target, checkedAt)
			case <-ctx.Done():
				checks[index] = unreachableWebLinkCheck(target, checkedAt, 0, "timeout")
			}
		}()
	}
	wait.Wait()
	return checks
}

func (prober *webLinkProber) probeOne(ctx context.Context, target webLinkTarget, checkedAt time.Time) domain.WebLinkCheck {
	startedAt := time.Now()
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, target.url, nil)
	if err != nil {
		return unreachableWebLinkCheck(target, checkedAt, 0, "request")
	}
	request.Header.Set("User-Agent", "Lodge-Link-Check/1")
	response, err := prober.client.Do(request)
	latency := time.Since(startedAt).Milliseconds()
	if err != nil {
		return unreachableWebLinkCheck(target, checkedAt, latency, classifyWebLinkError(err))
	}
	_ = response.Body.Close()
	state := domain.WebLinkReachable
	if response.StatusCode >= http.StatusInternalServerError {
		state = domain.WebLinkDegraded
	}
	return domain.WebLinkCheck{
		HostID: target.hostID, WorkloadKey: target.workloadKey, URL: target.url,
		State: state, HTTPStatus: response.StatusCode, LatencyMS: latency, CheckedAt: checkedAt,
	}
}

func unreachableWebLinkCheck(target webLinkTarget, checkedAt time.Time, latency int64, kind string) domain.WebLinkCheck {
	return domain.WebLinkCheck{
		HostID: target.hostID, WorkloadKey: target.workloadKey, URL: target.url,
		State: domain.WebLinkUnreachable, LatencyMS: latency, ErrorKind: kind, CheckedAt: checkedAt,
	}
}

func classifyWebLinkError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var certificateInvalid x509.CertificateInvalidError
	var hostnameInvalid x509.HostnameError
	var unknownAuthority x509.UnknownAuthorityError
	if errors.As(err, &certificateInvalid) || errors.As(err, &hostnameInvalid) || errors.As(err, &unknownAuthority) {
		return "tls"
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return "dns"
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return "timeout"
	}
	var operationError *net.OpError
	if errors.As(err, &operationError) {
		return "connect"
	}
	return "network"
}

func currentWebLinkTargets(store Store) []webLinkTarget {
	publicHosts := make(map[string]string)
	for _, agent := range store.Agents() {
		publicHosts[agent.ID] = agent.PublicHost
	}
	seen := make(map[[3]string]struct{})
	var targets []webLinkTarget
	for _, snapshot := range store.Snapshot() {
		services := JoinServices(snapshot.Services, store.Annotations(snapshot.ID), publicHosts[snapshot.ID])
		for _, service := range services {
			candidates := []string{service.URL}
			for _, route := range service.Routes {
				candidates = append(candidates, route.URL)
			}
			for _, candidate := range candidates {
				parsed, err := url.Parse(candidate)
				if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
					continue
				}
				if parsed.Path == "" {
					parsed.Path = "/"
				}
				normalized := parsed.String()
				key := [3]string{snapshot.ID, service.Key, normalized}
				if _, duplicate := seen[key]; duplicate {
					continue
				}
				seen[key] = struct{}{}
				targets = append(targets, webLinkTarget{
					hostID: domain.HostID(snapshot.ID), workloadKey: service.Key, url: normalized,
				})
			}
		}
	}
	sort.Slice(targets, func(left, right int) bool {
		if targets[left].hostID != targets[right].hostID {
			return targets[left].hostID < targets[right].hostID
		}
		if targets[left].workloadKey != targets[right].workloadKey {
			return targets[left].workloadKey < targets[right].workloadKey
		}
		return targets[left].url < targets[right].url
	})
	return targets
}
