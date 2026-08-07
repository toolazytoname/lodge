package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/toolazytoname/lodge/internal/domain"
)

const maximumNotificationCooldown = 24 * time.Hour

type NotificationChannelPolicy struct {
	Channel  string
	Cooldown time.Duration
}

func validateNotificationPolicies(policies []NotificationChannelPolicy) error {
	seen := make(map[string]struct{}, len(policies))
	for _, policy := range policies {
		if strings.TrimSpace(policy.Channel) == "" || len(policy.Channel) > 64 {
			return errors.New("notification channel is invalid")
		}
		for _, character := range policy.Channel {
			if unicode.IsControl(character) {
				return errors.New("notification channel contains control characters")
			}
		}
		if policy.Cooldown < 0 || policy.Cooldown > maximumNotificationCooldown {
			return errors.New("notification cooldown must be between 0 and 24 hours")
		}
		if _, duplicate := seen[policy.Channel]; duplicate {
			return fmt.Errorf("duplicate notification channel %q", policy.Channel)
		}
		seen[policy.Channel] = struct{}{}
	}
	return nil
}

func enqueueEventNotificationsTx(ctx context.Context, tx *sql.Tx, transitions []domain.EventTransition, policies []NotificationChannelPolicy, createdAt time.Time) error {
	for _, transition := range transitions {
		for _, policy := range policies {
			switch transition.Type {
			case domain.EventOpened:
				notBefore := createdAt.UTC()
				if policy.Cooldown > 0 {
					var lastDelivered sql.NullString
					err := tx.QueryRowContext(ctx, `
SELECT max(delivered_at) FROM event_notification_outbox
WHERE channel = ? AND dedupe_key = ? AND transition = 'opened' AND state = 'delivered'`,
						policy.Channel, transition.Event.DedupeKey).Scan(&lastDelivered)
					if err != nil {
						return err
					}
					if lastDelivered.Valid {
						last, err := parseTime(lastDelivered.String)
						if err != nil {
							return err
						}
						if cooled := last.Add(policy.Cooldown); cooled.After(notBefore) {
							notBefore = cooled
						}
					}
				}
				if _, err := tx.ExecContext(ctx, `
INSERT INTO event_notification_outbox(event_id, transition, channel, dedupe_key, state, not_before, next_attempt_at, created_at)
VALUES (?, 'opened', ?, ?, 'pending', ?, ?, ?)
ON CONFLICT(event_id, transition, channel) DO NOTHING`, transition.Event.ID, policy.Channel,
					transition.Event.DedupeKey, formatTime(notBefore), formatTime(notBefore), formatTime(createdAt)); err != nil {
					return fmt.Errorf("enqueue opened event notification: %w", err)
				}
			case domain.EventRecovered:
				result, err := tx.ExecContext(ctx, `
UPDATE event_notification_outbox SET state = 'cancelled', last_error_kind = ''
WHERE event_id = ? AND transition = 'opened' AND channel = ? AND state = 'pending'`,
					transition.Event.ID, policy.Channel)
				if err != nil {
					return fmt.Errorf("cancel stale opened notification: %w", err)
				}
				cancelled, err := result.RowsAffected()
				if err != nil {
					return err
				}
				if cancelled > 0 {
					continue
				}
				var notified int
				if err := tx.QueryRowContext(ctx, `
SELECT count(*) FROM event_notification_outbox
WHERE event_id = ? AND transition = 'opened' AND channel = ? AND state IN ('sending', 'delivered')`,
					transition.Event.ID, policy.Channel).Scan(&notified); err != nil {
					return err
				}
				if notified == 0 {
					continue
				}
				if _, err := tx.ExecContext(ctx, `
INSERT INTO event_notification_outbox(event_id, transition, channel, dedupe_key, state, not_before, next_attempt_at, created_at)
VALUES (?, 'recovered', ?, ?, 'pending', ?, ?, ?)
ON CONFLICT(event_id, transition, channel) DO NOTHING`, transition.Event.ID, policy.Channel,
					transition.Event.DedupeKey, formatTime(createdAt), formatTime(createdAt), formatTime(createdAt)); err != nil {
					return fmt.Errorf("enqueue recovered event notification: %w", err)
				}
			default:
				return fmt.Errorf("unsupported event transition %q", transition.Type)
			}
		}
	}
	return nil
}

// ClaimEventNotification leases one due delivery. Expired sending leases are
// reclaimed, which gives the worker crash-safe at-least-once delivery.
func (s *SQLite) ClaimEventNotification(ctx context.Context, channel string, now, leaseUntil time.Time) (domain.EventNotificationDelivery, bool, error) {
	if err := validateNotificationPolicies([]NotificationChannelPolicy{{Channel: channel}}); err != nil {
		return domain.EventNotificationDelivery{}, false, err
	}
	if now.IsZero() || !leaseUntil.After(now) {
		return domain.EventNotificationDelivery{}, false, errors.New("notification lease is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.EventNotificationDelivery{}, false, err
	}
	defer tx.Rollback()
	var delivery domain.EventNotificationDelivery
	var eventID string
	err = tx.QueryRowContext(ctx, `
SELECT id, event_id, transition, attempt_count
FROM event_notification_outbox
WHERE channel = ?
  AND state IN ('pending', 'sending')
  AND next_attempt_at <= ?
ORDER BY next_attempt_at, id
LIMIT 1`, channel, formatTime(now)).Scan(&delivery.ID, &eventID, &delivery.Transition, &delivery.Attempt)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return domain.EventNotificationDelivery{}, false, err
		}
		return domain.EventNotificationDelivery{}, false, nil
	}
	if err != nil {
		return domain.EventNotificationDelivery{}, false, err
	}
	delivery.Channel = channel
	delivery.Attempt++
	result, err := tx.ExecContext(ctx, `
UPDATE event_notification_outbox
SET state = 'sending', attempt_count = ?, next_attempt_at = ?, last_error_kind = ''
WHERE id = ? AND state IN ('pending', 'sending') AND next_attempt_at <= ?`,
		delivery.Attempt, formatTime(leaseUntil), delivery.ID, formatTime(now))
	if err != nil {
		return domain.EventNotificationDelivery{}, false, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return domain.EventNotificationDelivery{}, false, err
	}
	if updated != 1 {
		return domain.EventNotificationDelivery{}, false, errors.New("notification lease was lost")
	}
	events, err := loadEventsTx(ctx, tx, "WHERE id = ?", eventID)
	if err != nil {
		return domain.EventNotificationDelivery{}, false, err
	}
	if len(events) != 1 {
		return domain.EventNotificationDelivery{}, false, errors.New("notification event is missing")
	}
	delivery.Event = events[0]
	if err := tx.Commit(); err != nil {
		return domain.EventNotificationDelivery{}, false, err
	}
	return delivery, true, nil
}

func (s *SQLite) MarkEventNotificationDelivered(ctx context.Context, id int64, deliveredAt time.Time) error {
	if id < 1 || deliveredAt.IsZero() {
		return errors.New("delivered notification is invalid")
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE event_notification_outbox
SET state = 'delivered', delivered_at = ?, next_attempt_at = ?, last_error_kind = ''
WHERE id = ? AND state = 'sending'`, formatTime(deliveredAt), formatTime(deliveredAt), id)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return errors.New("notification is not leased")
	}
	return nil
}

func (s *SQLite) RetryEventNotification(ctx context.Context, id int64, nextAttempt time.Time, errorKind string, terminal bool) error {
	if id < 1 || nextAttempt.IsZero() || strings.TrimSpace(errorKind) == "" || len(errorKind) > 64 {
		return errors.New("notification retry is invalid")
	}
	for _, character := range errorKind {
		if unicode.IsControl(character) {
			return errors.New("notification error kind contains control characters")
		}
	}
	state := "pending"
	if terminal {
		state = "failed"
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE event_notification_outbox
SET state = ?, next_attempt_at = ?, last_error_kind = ?
WHERE id = ? AND state = 'sending'`, state, formatTime(nextAttempt), errorKind, id)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return errors.New("notification is not leased")
	}
	return nil
}
