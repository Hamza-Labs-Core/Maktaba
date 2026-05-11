// Package push routes notification dispatches to APNs (iOS) and FCM
// (Android, Web). The cloud is the only entity that holds APNs/FCM
// credentials — on-prem servers send "push" requests over the relay
// tunnel and we fan out from here.
package push

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Notification is the cross-platform shape on-prem servers submit.
// Platform-specific fields are encoded in `Data` (free-form JSON).
type Notification struct {
	UserID string
	Title  string
	Body   string
	Topic  string
	Data   map[string]string
}

// Dispatcher writes to push_dispatch_log and routes to the correct
// platform driver based on `push_devices.platform`.
type Dispatcher struct {
	DB    *sql.DB
	APNs  Driver
	FCM   Driver
}

// Driver is the per-platform send interface. APNsDriver and FCMDriver
// implement it; tests use a fake.
type Driver interface {
	Send(ctx context.Context, token string, n Notification) error
	Name() string
}

func NewDispatcher(db *sql.DB, apns, fcm Driver) *Dispatcher {
	return &Dispatcher{DB: db, APNs: apns, FCM: fcm}
}

// Send fans out to every registered device. Failures per device are
// logged but do not fail the request — a bad token is the most common
// case and we don't want a single stale device blocking the rest.
func (d *Dispatcher) Send(ctx context.Context, n Notification) error {
	rows, err := d.DB.QueryContext(ctx, `
        SELECT platform, token FROM push_devices WHERE user_id = $1
    `, n.UserID)
	if err != nil {
		return err
	}
	defer rows.Close()

	type target struct {
		platform, token string
	}
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.platform, &t.token); err != nil {
			return err
		}
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(targets) == 0 {
		return errors.New("push: no devices registered")
	}

	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		go func(t target) {
			defer wg.Done()
			drv := d.driverFor(t.platform)
			if drv == nil {
				d.logResult(ctx, n.UserID, t.platform, n.Topic, "no_driver", "")
				return
			}
			err := drv.Send(ctx, t.token, n)
			status := "ok"
			errStr := ""
			if err != nil {
				status = "err"
				errStr = err.Error()
			}
			d.logResult(ctx, n.UserID, t.platform, n.Topic, status, errStr)
		}(t)
	}
	wg.Wait()
	return nil
}

func (d *Dispatcher) driverFor(platform string) Driver {
	switch platform {
	case "ios":
		return d.APNs
	case "android", "web":
		return d.FCM
	}
	return nil
}

func (d *Dispatcher) logResult(ctx context.Context, uid, platform, topic, status, errStr string) {
	_, _ = d.DB.ExecContext(ctx, `
        INSERT INTO push_dispatch_log (user_id, platform, topic, status, error)
        VALUES ($1,$2,$3,$4,$5)
    `, uid, platform, topic, status, errStr)
}

// FakeDriver is a no-op driver used by tests.
type FakeDriver struct {
	Sent []Notification
	mu   sync.Mutex
}

func (f *FakeDriver) Name() string { return "fake" }
func (f *FakeDriver) Send(ctx context.Context, token string, n Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Sent = append(f.Sent, n)
	return nil
}

// RetryDriver wraps another with bounded retries and exponential backoff.
// Used in prod around APNsDriver and FCMDriver so transient 5xx don't
// drop notifications.
type RetryDriver struct {
	Underlying Driver
	MaxRetries int
	Base       time.Duration
}

func (r *RetryDriver) Name() string { return r.Underlying.Name() }
func (r *RetryDriver) Send(ctx context.Context, token string, n Notification) error {
	var lastErr error
	for i := 0; i <= r.MaxRetries; i++ {
		err := r.Underlying.Send(ctx, token, n)
		if err == nil {
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(r.Base << i):
		}
	}
	return fmt.Errorf("push: gave up after %d retries: %w", r.MaxRetries, lastErr)
}
