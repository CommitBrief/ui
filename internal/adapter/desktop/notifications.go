// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// NotificationSender shows native desktop notifications through the Wails
// notification service. Its Open method is the notify.Opener the notify adapter
// wraps (notify.NewLazy) and drives from its worker goroutine.
//
// The Wails service is created and started here but is deliberately not
// registered with the application: a registered service is bound to the webview,
// and the webview must not be able to raise notifications (or reach its
// authorization and category calls) by itself. Only the scheduler, through the
// notify adapter, sends them.
//
// Nothing happens at launch: Open runs on the first notification, so a machine
// that never needs one (no bot, no findings) pays nothing, and a platform without
// notification support (an unbundled development build on macOS, no D-Bus
// notification daemon on Linux) only logs when something would have been shown.
// macOS answers the permission request through a dialog that can stay open for
// minutes; that is why Open is never called from the scheduler's own goroutine.
type NotificationSender struct {
	mu  sync.Mutex
	svc *notifications.NotificationService // started once, kept between tries
}

// NewNotificationSender returns the sender. It does nothing until Open.
func NewNotificationSender() *NotificationSender { return &NotificationSender{} }

// Open starts the notification service (once) and asks for permission. It is
// safe to call again after a failure: a refused or timed-out permission request
// is asked again, the service is not started twice. It returns the function
// that shows a notification.
func (s *NotificationSender) Open(ctx context.Context) (func(ctx context.Context, id, title, body string) error, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.svc == nil {
		if application.Get() == nil {
			return nil, errors.New("the desktop application is not running")
		}
		svc := notifications.New()
		if err := svc.ServiceStartup(context.WithoutCancel(ctx), application.ServiceOptions{}); err != nil {
			return nil, fmt.Errorf("starting the notification service: %w", err)
		}
		s.svc = svc
	}
	ok, err := s.svc.RequestNotificationAuthorization()
	switch {
	case err != nil:
		return nil, fmt.Errorf("asking for permission to notify: %w", err)
	case !ok:
		return nil, errors.New("the user did not allow notifications")
	}
	svc := s.svc
	return func(_ context.Context, id, title, body string) error {
		return svc.SendNotification(notifications.NotificationOptions{ID: id, Title: title, Body: body})
	}, nil
}
