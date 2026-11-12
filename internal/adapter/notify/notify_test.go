// SPDX-License-Identifier: GPL-3.0-or-later

package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/port"
)

type sent struct{ id, title, body string }

type recSender struct {
	mu  sync.Mutex
	got []sent
	err error
}

func (r *recSender) Send(_ context.Context, id, title, body string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, sent{id, title, body})
	return r.err
}

func (r *recSender) sentSoFar() []sent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]sent(nil), r.got...)
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestNotifyWordsTheReasons(t *testing.T) {
	s := &recSender{}
	n := New(s, quiet)
	ctx := context.Background()

	n.Notify(ctx, port.Notification{Kind: port.NotifyNewFindings, RunID: 12, JobName: "Review requests", Findings: 5, PullRequests: 2})
	n.Notify(ctx, port.Notification{Kind: port.NotifyNewFindings, RunID: 13, JobName: "x", Findings: 1, PullRequests: 1})
	n.Notify(ctx, port.Notification{Kind: port.NotifyRunFailed, RunID: 14, JobName: "Review requests", ErrorCode: errcode.Timeout})
	n.Notify(ctx, port.Notification{Kind: port.NotifyRunFailed, RunID: 15, JobName: "Review requests", ErrorCode: errcode.GHNotAuthenticated, NeedsAttention: true})
	n.Notify(ctx, port.Notification{Kind: port.NotifyBudgetExceeded, BudgetUSD: 5, SpentUSD: 5.036})
	n.Notify(ctx, port.Notification{Kind: port.NotifyUpdateAvailable, Version: "0.2.0"})

	want := []sent{
		{"commitbrief-ui-findings-12", "New review findings", "Review requests: 5 findings in 2 pull requests."},
		{"commitbrief-ui-findings-13", "New review findings", "x: 1 finding in 1 pull request."},
		{"commitbrief-ui-failed-14", "Review run failed", "Review requests: a review timed out."},
		{"commitbrief-ui-failed-15", "Review bot needs attention", "Review requests: GitHub CLI is not logged in. It will not run again until you resume it."},
		{"commitbrief-ui-budget", "Daily budget reached", "Spent $5.04 of $5.00 today. No new reviews start until tomorrow."},
		{"commitbrief-ui-update-0.2.0", "Update available", "CommitBrief UI 0.2.0 is ready to install. Open the Updates screen to install it."},
	}
	n.Close(ctx) // waits for the queue to be shown
	got := s.sentSoFar()
	if len(got) != len(want) {
		t.Fatalf("sent %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("notification %d = %+v\nwant %+v", i, got[i], want[i])
		}
	}
}

func TestNotifyIsBestEffort(t *testing.T) {
	ctx := context.Background()
	New(nil, quiet).Notify(ctx, port.Notification{Kind: port.NotifyBudgetExceeded}) // no sender: no panic

	s := &recSender{err: errors.New("no notification daemon")}
	n := New(s, quiet)
	n.Notify(ctx, port.Notification{Kind: port.NotifyBudgetExceeded}) // a failing sender is not an error for the caller
	n.Close(ctx)
	if len(s.sentSoFar()) != 1 {
		t.Errorf("sent = %d", len(s.sentSoFar()))
	}

	s = &recSender{}
	n = New(s, quiet)
	n.Notify(ctx, port.Notification{Kind: "something_else"})
	n.Close(ctx)
	if len(s.sentSoFar()) != 0 {
		t.Error("an unknown kind was shown")
	}

	// After Close nothing is accepted, and Close can be repeated.
	n.Notify(ctx, port.Notification{Kind: port.NotifyBudgetExceeded})
	n.Close(ctx)
	if len(s.sentSoFar()) != 0 {
		t.Error("a notification after Close was shown")
	}
}

func TestNotifyKeepsUserTextSafe(t *testing.T) {
	s := &recSender{}
	n := New(s, quiet)
	n.Notify(context.Background(), port.Notification{
		Kind: port.NotifyNewFindings, RunID: 1, Findings: 2, PullRequests: 1,
		JobName: "line one\nline two\x00\x1b[31m" + strings.Repeat("é", 200),
	})
	n.Close(context.Background())
	body := s.sentSoFar()[0].body
	if strings.ContainsAny(body, "\n\x00\x1b") {
		t.Errorf("control characters survived: %q", body)
	}
	if strings.Count(body, "é") > maxJobNameLen {
		t.Errorf("an overlong job name was not cut: %d runes", strings.Count(body, "é"))
	}
	if !strings.Contains(body, "...") {
		t.Errorf("a cut name is marked: %q", body)
	}

	s = &recSender{}
	n = New(s, quiet)
	n.Notify(context.Background(), port.Notification{Kind: port.NotifyNewFindings, RunID: 2, Findings: 1, PullRequests: 1, JobName: " \n "})
	n.Close(context.Background())
	if got := s.sentSoFar(); len(got) != 1 || !strings.HasPrefix(got[0].body, "Review bot:") {
		t.Errorf("an empty name falls back: %+v", got)
	}
}

// blockingSender stands for the permission dialog macOS shows on the first
// notification: Send does not return until released, and ignores its context.
type blockingSender struct {
	entered chan struct{}
	release chan struct{}
}

func (b *blockingSender) Send(context.Context, string, string, string) error {
	b.entered <- struct{}{}
	<-b.release
	return nil
}

// The scheduler calls Notify from its tick and holds its lock while it does; a
// Sender stuck in a permission dialog must not hold either of them (nor Quit).
func TestNotifyNeverBlocksOnAStuckSender(t *testing.T) {
	b := &blockingSender{entered: make(chan struct{}, 1), release: make(chan struct{})}
	n := New(b, quiet)
	defer close(b.release)

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ctx := context.Background()
		for i := 0; i < 3*queueSize; i++ { // more than the queue holds: the surplus is dropped
			n.Notify(ctx, port.Notification{Kind: port.NotifyBudgetExceeded})
		}
		// Quit: Close gives up after its deadline and does not wait for the dialog.
		stop, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		n.Close(stop)
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("Notify or Close blocked behind a Sender that does not return")
	}
	select {
	case <-b.entered:
	default:
		t.Error("the worker never reached the sender")
	}
}

func TestLazyRetriesAFailedOpenWithBackoff(t *testing.T) {
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	opens, shown := 0, 0
	openErr := errors.New("the user did not allow notifications")
	l := NewLazy(func(context.Context) (SendFunc, error) {
		opens++
		if openErr != nil {
			return nil, openErr
		}
		return func(context.Context, string, string, string) error { shown++; return nil }, nil
	})
	l.now = func() time.Time { return now }
	ctx := context.Background()

	if err := l.Send(ctx, "a", "t", "b"); !errors.Is(err, openErr) || opens != 1 {
		t.Fatalf("first send = %v (opens %d)", err, opens)
	}
	// Within the pause nothing asks again: the failure is reported at once.
	now = now.Add(30 * time.Second)
	if err := l.Send(ctx, "a", "t", "b"); !errors.Is(err, openErr) || opens != 1 {
		t.Errorf("send during the pause = %v (opens %d)", err, opens)
	}
	// After a minute it tries again; failing again doubles the pause.
	now = now.Add(31 * time.Second)
	if err := l.Send(ctx, "a", "t", "b"); !errors.Is(err, openErr) || opens != 2 {
		t.Errorf("second try = %v (opens %d)", err, opens)
	}
	now = now.Add(90 * time.Second)
	if err := l.Send(ctx, "a", "t", "b"); opens != 2 || err == nil {
		t.Errorf("the pause is now two minutes: %v (opens %d)", err, opens)
	}
	// The pause is capped.
	for i := 0; i < 10; i++ {
		now = now.Add(retryMax + time.Second)
		_ = l.Send(ctx, "a", "t", "b")
	}
	if l.delay != retryMax {
		t.Errorf("delay = %s, want the cap %s", l.delay, retryMax)
	}

	// The user granted the permission: the next try works and sticks.
	openErr = nil
	now = now.Add(retryMax + time.Second)
	if err := l.Send(ctx, "a", "t", "b"); err != nil || shown != 1 {
		t.Fatalf("after the permission = %v (shown %d)", err, shown)
	}
	before := opens
	if err := l.Send(ctx, "b", "t", "b"); err != nil || shown != 2 || opens != before {
		t.Errorf("an opened sender is reused: %v (shown %d, opens %d)", err, shown, opens-before)
	}
}
