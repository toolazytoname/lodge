package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/toolazytoname/lodge/internal/domain"
)

const (
	webhookChannel      = "webhook"
	webhookLease        = 30 * time.Second
	webhookPollInterval = 5 * time.Second
	maximumWebhookTries = 8
)

type EventNotificationQueue interface {
	ClaimEventNotification(context.Context, string, time.Time, time.Time) (domain.EventNotificationDelivery, bool, error)
	MarkEventNotificationDelivered(context.Context, int64, time.Time) error
	RetryEventNotification(context.Context, int64, time.Time, string, bool) error
}

type WebhookNotifier struct {
	queue    EventNotificationQueue
	endpoint string
	secret   string
	client   *http.Client
	now      func() time.Time
}

type webhookPayload struct {
	Version     int                        `json:"version"`
	DeliveryKey string                     `json:"deliveryKey"`
	Transition  domain.EventTransitionType `json:"transition"`
	Event       webhookEvent               `json:"event"`
}

type webhookEvent struct {
	ID              string            `json:"id"`
	HostID          domain.HostID     `json:"hostId"`
	Kind            string            `json:"kind"`
	Severity        domain.Severity   `json:"severity"`
	State           domain.EventState `json:"state"`
	Title           string            `json:"title"`
	Detail          string            `json:"detail,omitempty"`
	FirstObservedAt time.Time         `json:"firstObservedAt"`
	LastObservedAt  time.Time         `json:"lastObservedAt"`
	AcknowledgedAt  *time.Time        `json:"acknowledgedAt,omitempty"`
	ResolvedAt      *time.Time        `json:"resolvedAt,omitempty"`
}

func NewWebhookNotifier(queue EventNotificationQueue, endpoint, secret string) (*WebhookNotifier, error) {
	if queue == nil {
		return nil, errors.New("webhook notification queue is required")
	}
	config := WebhookConfig{URL: endpoint}
	if err := normalizeWebhookConfig(&config); err != nil {
		return nil, err
	}
	if secret != "" && (len(secret) > maximumWebhookSecretBytes || !isVisibleASCII(secret)) {
		return nil, errors.New("webhook secret is invalid")
	}
	return newWebhookNotifier(queue, config.URL, secret, hardenedWebhookClient()), nil
}

func newWebhookNotifier(queue EventNotificationQueue, endpoint, secret string, client *http.Client) *WebhookNotifier {
	return &WebhookNotifier{queue: queue, endpoint: endpoint, secret: secret, client: client, now: time.Now}
}

func hardenedWebhookClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (notifier *WebhookNotifier) Run(ctx context.Context) {
	notifier.drain(ctx)
	ticker := time.NewTicker(webhookPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			notifier.drain(ctx)
		}
	}
}

func (notifier *WebhookNotifier) drain(ctx context.Context) {
	for count := 0; count < 32 && ctx.Err() == nil; count++ {
		processed, err := notifier.deliverNext(ctx)
		if err != nil {
			log.Printf("lodge hub webhook: %v", err)
		}
		if !processed {
			return
		}
	}
}

func (notifier *WebhookNotifier) deliverNext(ctx context.Context) (bool, error) {
	now := notifier.now().UTC()
	delivery, found, err := notifier.queue.ClaimEventNotification(ctx, webhookChannel, now, now.Add(webhookLease))
	if err != nil || !found {
		return found, err
	}
	payload := webhookPayload{
		Version:     1,
		DeliveryKey: fmt.Sprintf("%s:%s:%s", delivery.Event.ID, delivery.Transition, webhookChannel),
		Transition:  delivery.Transition,
		Event: webhookEvent{
			ID: delivery.Event.ID, HostID: delivery.Event.HostID, Kind: delivery.Event.Kind,
			Severity: delivery.Event.Severity, State: delivery.Event.State,
			Title: delivery.Event.Title, Detail: delivery.Event.Detail,
			FirstObservedAt: delivery.Event.FirstObservedAt, LastObservedAt: delivery.Event.LastObservedAt,
			AcknowledgedAt: delivery.Event.AcknowledgedAt, ResolvedAt: delivery.Event.ResolvedAt,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return true, notifier.finishFailure(ctx, delivery, now, "encode", true)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, notifier.endpoint, bytes.NewReader(body))
	if err != nil {
		return true, notifier.finishFailure(ctx, delivery, now, "request", true)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "lodge-hub/webhook-v1")
	request.Header.Set("X-Lodge-Delivery", payload.DeliveryKey)
	if notifier.secret != "" {
		request.Header.Set("Authorization", "Bearer "+notifier.secret)
	}
	response, err := notifier.client.Do(request)
	if err != nil {
		kind := webhookNetworkErrorKind(err)
		return true, notifier.finishFailure(ctx, delivery, now, kind, delivery.Attempt >= maximumWebhookTries)
	}
	_ = response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		if err := notifier.queue.MarkEventNotificationDelivered(ctx, delivery.ID, now); err != nil {
			return true, fmt.Errorf("delivery %d completion: %w", delivery.ID, err)
		}
		return true, nil
	}
	kind := fmt.Sprintf("status_%d", response.StatusCode)
	retryable := response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooEarly || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
	terminal := !retryable || delivery.Attempt >= maximumWebhookTries
	return true, notifier.finishFailure(ctx, delivery, now, kind, terminal)
}

func (notifier *WebhookNotifier) finishFailure(ctx context.Context, delivery domain.EventNotificationDelivery, now time.Time, kind string, terminal bool) error {
	nextAttempt := now
	if !terminal {
		nextAttempt = now.Add(webhookRetryDelay(delivery.Attempt))
	}
	if err := notifier.queue.RetryEventNotification(ctx, delivery.ID, nextAttempt, kind, terminal); err != nil {
		return fmt.Errorf("delivery %d failure state: %w", delivery.ID, err)
	}
	if terminal {
		return fmt.Errorf("delivery %d stopped after attempt %d (%s)", delivery.ID, delivery.Attempt, kind)
	}
	return fmt.Errorf("delivery %d scheduled after attempt %d (%s)", delivery.ID, delivery.Attempt, kind)
}

func webhookNetworkErrorKind(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return "timeout"
	}
	return "network"
}

func webhookRetryDelay(attempt int) time.Duration {
	delays := [...]time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute}
	if attempt < 1 {
		return delays[0]
	}
	if attempt > len(delays) {
		return time.Hour
	}
	return delays[attempt-1]
}
