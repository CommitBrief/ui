// SPDX-License-Identifier: GPL-3.0-or-later

// Package notify words the scheduler's notifications and hands them to the
// operating system. It implements port.Notifier on top of a Sender, the one piece
// that touches the desktop (the Wails notification service, in the desktop
// adapter), so the wording and the rules stay testable without a window system.
//
// Showing a notification can block for minutes: the first one makes macOS ask
// the user for permission, and the operating system's answer is not tied to any
// context. So Notify only queues (it never blocks the scheduler's tick or the
// quit path) and one worker goroutine hands the queue to the Sender.
//
// The text is English constants, like the tray labels: native notifications are
// outside the webview and its message catalog, and are localized together with
// the rest of i18n (phase 15). Only counts, the job name and a failure class go
// into a notification; findings, pull request titles and other repository
// content never do.
package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/port"
)

// Sender shows one notification. id is unique per event.
type Sender interface {
	Send(ctx context.Context, id, title, body string) error
}

// queueSize bounds the notifications waiting for the worker. The scheduler
// raises a handful an hour at most; a full queue means the sender is stuck (the
// permission dialog is still open), and the surplus is dropped rather than
// piled up.
const queueSize = 16

// Notifier implements port.Notifier on a worker goroutine.
type Notifier struct {
	sender Sender
	log    *slog.Logger

	mu     sync.Mutex
	closed bool
	queue  chan port.Notification

	workCtx context.Context
	cancel  context.CancelFunc
	done    chan struct{}
}

var _ port.Notifier = (*Notifier)(nil)

// New builds a Notifier and starts its worker. A nil sender makes every
// notification a no-op (and starts nothing); a nil logger means slog.Default().
// Close stops the worker.
func New(sender Sender, log *slog.Logger) *Notifier {
	if log == nil {
		log = slog.Default()
	}
	n := &Notifier{sender: sender, log: log, done: make(chan struct{})}
	if sender == nil {
		close(n.done)
		return n
	}
	n.queue = make(chan port.Notification, queueSize)
	n.workCtx, n.cancel = context.WithCancel(context.Background())
	go n.work()
	return n
}

// Notify implements port.Notifier. It queues the notification and returns at
// once. A notification that cannot be shown (no permission, no notification
// daemon, an unbundled development build, a full queue) is logged and dropped.
func (n *Notifier) Notify(_ context.Context, x port.Notification) {
	if n.sender == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return
	}
	select {
	case n.queue <- x:
	default:
		n.log.Warn("a desktop notification was dropped: the notification queue is full", "kind", x.Kind)
	}
}

// Close stops accepting notifications and waits for the queued ones to be
// shown, until ctx ends; then it cancels what is still pending and returns
// without waiting for a Sender that does not come back (the operating system's
// permission dialog is not interruptible). Safe to call more than once.
func (n *Notifier) Close(ctx context.Context) {
	n.mu.Lock()
	if !n.closed {
		n.closed = true
		if n.queue != nil {
			close(n.queue)
		}
	}
	n.mu.Unlock()
	select {
	case <-n.done:
	case <-ctx.Done():
		if n.cancel != nil {
			n.cancel()
		}
	}
}

func (n *Notifier) work() {
	defer close(n.done)
	defer n.cancel()
	for x := range n.queue {
		if n.workCtx.Err() != nil {
			continue // closing: drop what is left
		}
		n.show(x)
	}
}

func (n *Notifier) show(x port.Notification) {
	id, title, body, ok := compose(x)
	if !ok {
		return
	}
	if err := n.sender.Send(n.workCtx, id, title, body); err != nil {
		n.log.Warn("a desktop notification could not be shown", "kind", x.Kind, "error", err)
	}
}

// maxJobNameLen bounds the job name inside a notification.
const maxJobNameLen = 60

// compose words a notification: its id, title and body.
func compose(x port.Notification) (id, title, body string, ok bool) {
	name := displayName(x.JobName)
	switch x.Kind {
	case port.NotifyNewFindings:
		return fmt.Sprintf("commitbrief-ui-findings-%d", x.RunID),
			"New review findings",
			fmt.Sprintf("%s: %s in %s.", name, plural(x.Findings, "finding", "findings"), plural(x.PullRequests, "pull request", "pull requests")),
			true
	case port.NotifyRunFailed:
		title = "Review run failed"
		if x.NeedsAttention {
			title = "Review bot needs attention"
		}
		body = fmt.Sprintf("%s: %s.", name, describe(x.ErrorCode))
		if x.NeedsAttention {
			body += " It will not run again until you resume it."
		}
		return fmt.Sprintf("commitbrief-ui-failed-%d", x.RunID), title, body, true
	case port.NotifyBudgetExceeded:
		return "commitbrief-ui-budget",
			"Daily budget reached",
			fmt.Sprintf("Spent $%.2f of $%.2f today. No new reviews start until tomorrow.", x.SpentUSD, x.BudgetUSD),
			true
	case port.NotifyUpdateAvailable:
		return "commitbrief-ui-update-" + x.Version,
			"Update available",
			fmt.Sprintf("CommitBrief UI %s is ready to install. Open the Updates screen to install it.", x.Version),
			true
	}
	return "", "", "", false
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// displayName makes a user-chosen job name safe for a notification: control
// characters are dropped and a long name is cut on a rune boundary.
func displayName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if s == "" {
		return "Review bot"
	}
	if utf8.RuneCountInString(s) > maxJobNameLen {
		r := []rune(s)
		s = string(r[:maxJobNameLen]) + "..."
	}
	return s
}

// describe is a short phrase for a failure class.
func describe(c errcode.Code) string {
	switch c {
	case errcode.GHNotAuthenticated:
		return "GitHub CLI is not logged in"
	case errcode.GHNotFound:
		return "GitHub CLI was not found"
	case errcode.CLINotFound:
		return "commitbrief was not found"
	case errcode.CLITooOld:
		return "commitbrief is too old"
	case errcode.CapabilityMissing:
		return "commitbrief needs an update for this mode"
	case errcode.ProviderNotAPI:
		return "the AI provider cannot return structured findings"
	case errcode.SelfPR:
		return "a pull request is your own"
	case errcode.Timeout:
		return "a review timed out"
	case errcode.TooVolatile:
		return "a pull request changed too fast"
	case errcode.ProviderError:
		return "the AI provider reported an error"
	case errcode.SecretDetected:
		return "a secret was flagged in a diff"
	case errcode.CostAborted:
		return "the daily budget stopped a review"
	case errcode.Degraded:
		return "an AI answer could not be read"
	case "":
		return "a review failed"
	}
	return "a review failed (" + string(c) + ")"
}

// SendFunc shows one notification; it is what an Opener returns.
type SendFunc = func(ctx context.Context, id, title, body string) error

// Opener prepares the desktop side (starts the notification service, asks for the
// permission) and returns the function that shows a notification.
type Opener = func(ctx context.Context) (SendFunc, error)

// Backoff bounds for Lazy: after a failed open the next try waits this long,
// doubling up to the cap.
const (
	retryInitial = time.Minute
	retryMax     = 30 * time.Minute
)

// Lazy is a Sender that opens the desktop side on the first notification and
// keeps what it opened. A failed open (the permission was refused or timed out,
// no notification daemon yet) is not final: the user may grant the permission in
// the system settings later, so the open is tried again after a growing pause
// (one minute, doubling to half an hour). Between tries a notification fails at
// once instead of asking again.
type Lazy struct {
	open Opener
	now  func() time.Time

	mu      sync.Mutex
	send    SendFunc
	err     error
	retryAt time.Time
	delay   time.Duration
}

var _ Sender = (*Lazy)(nil)

// NewLazy wraps an Opener.
func NewLazy(open Opener) *Lazy { return &Lazy{open: open, now: time.Now} }

// Send implements Sender.
func (l *Lazy) Send(ctx context.Context, id, title, body string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.send == nil {
		if now := l.now(); now.Before(l.retryAt) {
			return fmt.Errorf("notifications are unavailable, trying again in %s: %w", l.retryAt.Sub(now).Round(time.Second), l.err)
		}
		send, err := l.open(ctx)
		if err != nil {
			l.err = err
			l.delay = min(max(2*l.delay, retryInitial), retryMax)
			l.retryAt = l.now().Add(l.delay)
			return err
		}
		l.send, l.err, l.delay, l.retryAt = send, nil, 0, time.Time{}
	}
	return l.send(ctx, id, title, body)
}
