package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/toolazytoname/lodge/internal/domain"
)

type notificationQueueStub struct {
	delivery      domain.EventNotificationDelivery
	claimed       bool
	deliveredID   int64
	deliveredAt   time.Time
	retriedID     int64
	nextAttempt   time.Time
	errorKind     string
	terminal      bool
	claimChannel  string
	claimLeaseEnd time.Time
}

func (queue *notificationQueueStub) ClaimEventNotification(_ context.Context, channel string, _ time.Time, leaseUntil time.Time) (domain.EventNotificationDelivery, bool, error) {
	queue.claimChannel = channel
	queue.claimLeaseEnd = leaseUntil
	if queue.claimed {
		return domain.EventNotificationDelivery{}, false, nil
	}
	queue.claimed = true
	return queue.delivery, true, nil
}

func (queue *notificationQueueStub) MarkEventNotificationDelivered(_ context.Context, id int64, deliveredAt time.Time) error {
	queue.deliveredID = id
	queue.deliveredAt = deliveredAt
	return nil
}

func (queue *notificationQueueStub) RetryEventNotification(_ context.Context, id int64, nextAttempt time.Time, errorKind string, terminal bool) error {
	queue.retriedID = id
	queue.nextAttempt = nextAttempt
	queue.errorKind = errorKind
	queue.terminal = terminal
	return nil
}

func webhookTestDelivery(attempt int) domain.EventNotificationDelivery {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	return domain.EventNotificationDelivery{
		ID: 41, Channel: webhookChannel, Transition: domain.EventOpened, Attempt: attempt,
		Event: domain.Event{
			ID: "event-1", HostID: "host-a", Kind: "host.offline", Severity: domain.SeverityCritical,
			State: domain.EventActive, DedupeKey: "host-a:host:offline", Title: "Host offline",
			Detail: "Agent did not respond", FirstObservedAt: now, LastObservedAt: now,
		},
	}
}

func TestWebhookNotifierDeliversVersionedRedactedPayload(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.Header.Get("Content-Type") != "application/json" || request.Header.Get("Authorization") != "Bearer webhook-secret" {
			t.Errorf("unexpected request headers: method=%s headers=%v", request.Method, request.Header)
		}
		if request.Header.Get("X-Lodge-Delivery") != "event-1:opened:webhook" {
			t.Errorf("idempotency header = %q", request.Header.Get("X-Lodge-Delivery"))
		}
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	queue := &notificationQueueStub{delivery: webhookTestDelivery(1)}
	notifier := newWebhookNotifier(queue, server.URL, "webhook-secret", server.Client())
	now := time.Date(2026, 8, 8, 12, 1, 0, 0, time.UTC)
	notifier.now = func() time.Time { return now }
	processed, err := notifier.deliverNext(context.Background())
	if err != nil || !processed {
		t.Fatalf("delivery failed: processed=%v err=%v", processed, err)
	}
	if queue.claimChannel != webhookChannel || !queue.claimLeaseEnd.Equal(now.Add(webhookLease)) || queue.deliveredID != 41 || !queue.deliveredAt.Equal(now) {
		t.Fatalf("queue lifecycle mismatch: %+v", queue)
	}
	encoded, err := json.Marshal(received)
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	if received["version"] != float64(1) || received["transition"] != "opened" || strings.Contains(body, "dedupe") || strings.Contains(body, "webhook-secret") {
		t.Fatalf("payload contract or redaction mismatch: %s", body)
	}
}

func TestWebhookNotifierRetriesTransientAndStopsPermanentFailures(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		attempt      int
		wantKind     string
		wantTerminal bool
		wantDelay    time.Duration
	}{
		{name: "server error", status: http.StatusServiceUnavailable, attempt: 1, wantKind: "status_503", wantDelay: 5 * time.Second},
		{name: "rate limited", status: http.StatusTooManyRequests, attempt: 2, wantKind: "status_429", wantDelay: 30 * time.Second},
		{name: "bad request", status: http.StatusBadRequest, attempt: 1, wantKind: "status_400", wantTerminal: true},
		{name: "retry budget", status: http.StatusServiceUnavailable, attempt: maximumWebhookTries, wantKind: "status_503", wantTerminal: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.WriteHeader(test.status)
			}))
			defer server.Close()
			queue := &notificationQueueStub{delivery: webhookTestDelivery(test.attempt)}
			notifier := newWebhookNotifier(queue, server.URL, "", server.Client())
			now := time.Date(2026, 8, 8, 12, 2, 0, 0, time.UTC)
			notifier.now = func() time.Time { return now }
			processed, err := notifier.deliverNext(context.Background())
			if !processed || err == nil {
				t.Fatalf("failed delivery was not reported: processed=%v err=%v", processed, err)
			}
			if queue.retriedID != 41 || queue.errorKind != test.wantKind || queue.terminal != test.wantTerminal || !queue.nextAttempt.Equal(now.Add(test.wantDelay)) {
				t.Fatalf("failure state mismatch: %+v", queue)
			}
			if strings.Contains(err.Error(), server.URL) {
				t.Fatalf("delivery error leaked endpoint: %v", err)
			}
		})
	}
}

func TestWebhookNotifierDoesNotFollowRedirects(t *testing.T) {
	targetCalls := 0
	server := httptest.NewServer(http.NewServeMux())
	defer server.Close()
	mux := server.Config.Handler.(*http.ServeMux)
	mux.HandleFunc("/hook", func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, "/target", http.StatusFound)
	})
	mux.HandleFunc("/target", func(response http.ResponseWriter, _ *http.Request) {
		targetCalls++
		response.WriteHeader(http.StatusNoContent)
	})
	client := server.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	queue := &notificationQueueStub{delivery: webhookTestDelivery(1)}
	notifier := newWebhookNotifier(queue, server.URL+"/hook", "", client)
	if processed, err := notifier.deliverNext(context.Background()); !processed || err == nil {
		t.Fatalf("redirect was not rejected: processed=%v err=%v", processed, err)
	}
	if targetCalls != 0 || !queue.terminal || queue.errorKind != "status_302" {
		t.Fatalf("redirect handling mismatch: targetCalls=%d queue=%+v", targetCalls, queue)
	}
}

func TestNewWebhookNotifierRequiresHTTPS(t *testing.T) {
	if _, err := NewWebhookNotifier(&notificationQueueStub{}, "http://hooks.example.test/lodge", ""); err == nil {
		t.Fatal("HTTP webhook endpoint was accepted")
	}
}
