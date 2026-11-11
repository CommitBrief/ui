// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/domain/job"
	"github.com/CommitBrief/ui/internal/domain/run"
	"github.com/CommitBrief/ui/internal/domain/schedule"
	"github.com/CommitBrief/ui/internal/port"
)

type fakeAutoStart struct {
	on  bool
	err error
}

func (f *fakeAutoStart) Enabled(context.Context) (bool, error) { return f.on, f.err }
func (f *fakeAutoStart) Enable(context.Context) error {
	if f.err == nil {
		f.on = true
	}
	return f.err
}
func (f *fakeAutoStart) Disable(context.Context) error {
	if f.err == nil {
		f.on = false
	}
	return f.err
}

func newSchedules(t *testing.T) (*Schedules, *memDB, *fakeClock, *fakeAutoStart) {
	t.Helper()
	ist := istanbul(t)
	db := newMemDB()
	clock := &fakeClock{t: time.Date(2026, time.November, 11, 8, 0, 0, 0, ist), loc: ist}
	auto := &fakeAutoStart{}
	s := NewSchedules(SchedulesDeps{
		Jobs: db, Budget: NewBudget(BudgetDeps{Settings: db, Costs: db, Clock: clock}),
		AutoStart: auto, Clock: clock, Logger: quietLogger,
	})
	return s, db, clock, auto
}

func goodInput() BotJobInput {
	return BotJobInput{
		Name: "Review requests", Mode: job.ModePreview, Enabled: true, Targets: []int64{1, 2},
		Windows: []schedule.Window{{Days: schedule.Weekdays, Start: 9*60 + 30, End: 17*60 + 45, Every: 15, TZ: "Europe/Istanbul"}},
	}
}

func runCode(err error) (run.Code, string) {
	var re *run.Error
	if errors.As(err, &re) {
		return re.Code, re.Field
	}
	return "", ""
}

func TestScheduleSaveBotJobRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, db, _, _ := newSchedules(t)

	got, err := s.SaveBotJob(ctx, goodInput())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == 0 || got.Name != "Review requests" || got.Mode != job.ModePreview || !got.Enabled || len(got.Windows) != 1 || got.IncludeDrafts || got.PostConsentAt != nil {
		t.Fatalf("saved = %+v", got)
	}
	// 08:00 on a Wednesday: the next slot is today's 09:30.
	if got.NextDue == nil || got.NextDue.Hour() != 9 || got.NextDue.Minute() != 30 {
		t.Errorf("NextDue = %v", got.NextDue)
	}
	rec, _ := db.GetJob(ctx, got.ID)
	if rec.Kind != string(job.KindBotReviewRequested) || len(rec.Windows) != 1 || len(rec.RepositoryIDs) != 2 {
		t.Errorf("stored = %+v", rec)
	}

	list, err := s.ListBotJobs(ctx)
	if err != nil || len(list) != 1 || list[0].ID != got.ID {
		t.Fatalf("ListBotJobs = %+v, %v", list, err)
	}
	// Other kinds are not the bot's business.
	other, _ := db.CreateJob(ctx, port.JobRecord{Kind: "review.pr", Name: "x", Mode: "preview"})
	if list, _ := s.ListBotJobs(ctx); len(list) != 1 {
		t.Errorf("ListBotJobs listed another kind: %+v", list)
	}
	// ... and no method of this service touches one by its id.
	s.d.Runner = &runnerFake{}
	for name, f := range map[string]func() error{
		"delete": func() error { return s.DeleteJob(ctx, other.ID) },
		"resume": func() error { _, err := s.ResumeJob(ctx, other.ID); return err },
		"enable": func() error { _, err := s.SetJobEnabled(ctx, other.ID, false); return err },
		"run":    func() error { _, err := s.RunJobNow(ctx, other.ID); return err },
	} {
		if c, field := runCode(f()); c != run.CodeJobInvalid || field != "id" {
			t.Errorf("%s of a job of another kind = %q, %q; want job_invalid on id", name, c, field)
		}
	}
	if _, err := db.GetJob(ctx, other.ID); err != nil {
		t.Errorf("the other job was changed: %v", err)
	}

	// Update: the job is replaced in place, including its windows.
	in := goodInput()
	in.ID, in.Name, in.IncludeDrafts, in.Windows = got.ID, "Renamed", true, nil
	upd, err := s.SaveBotJob(ctx, in)
	if err != nil || upd.Name != "Renamed" || !upd.IncludeDrafts || len(upd.Windows) != 0 || upd.NextDue != nil {
		t.Fatalf("updated = %+v, %v", upd, err)
	}
}

func TestScheduleSaveBotJobValidation(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := newSchedules(t)
	cases := []struct {
		name  string
		edit  func(*BotJobInput)
		code  run.Code
		field string
	}{
		{"no days", func(in *BotJobInput) { in.Windows[0].Days = 0 }, run.CodeScheduleInvalid, "windows[0].days"},
		{"bad zone", func(in *BotJobInput) { in.Windows[0].TZ = "Mars/Base" }, run.CodeScheduleInvalid, "windows[0].tz"},
		{"end before start", func(in *BotJobInput) { in.Windows[0].End = 100 }, run.CodeScheduleInvalid, "windows[0].end"},
		{"zero interval", func(in *BotJobInput) { in.Windows[0].Every = 0 }, run.CodeScheduleInvalid, "windows[0].every"},
		{"second window bad", func(in *BotJobInput) {
			in.Windows = append(in.Windows, schedule.Window{Days: schedule.Monday, Start: 0, End: 60, Every: 5, TZ: ""})
		}, run.CodeScheduleInvalid, "windows[1].tz"},
		{"too many windows", func(in *BotJobInput) {
			in.Windows = make([]schedule.Window, MaxWindows+1)
		}, run.CodeScheduleInvalid, "windows"},
		{"empty name", func(in *BotJobInput) { in.Name = " " }, run.CodeJobInvalid, "name"},
		{"no target", func(in *BotJobInput) { in.Targets = nil }, run.CodeJobInvalid, "targets"},
		{"unknown mode", func(in *BotJobInput) { in.Mode = "dry" }, run.CodeJobInvalid, "mode"},
	}
	for _, tc := range cases {
		in := goodInput()
		tc.edit(&in)
		if c, f := runCode(func() error { _, err := s.SaveBotJob(ctx, in); return err }()); c != tc.code || f != tc.field {
			t.Errorf("%s: %q (%q), want %q (%q)", tc.name, c, f, tc.code, tc.field)
		}
	}
	// Nothing was stored by the refused ones.
	if list, _ := s.ListBotJobs(ctx); len(list) != 0 {
		t.Errorf("refused jobs were stored: %+v", list)
	}
	if c, _ := runCode(func() error {
		_, err := s.SaveBotJob(ctx, BotJobInput{ID: 99, Name: "x", Mode: job.ModePreview, Targets: []int64{1}})
		return err
	}()); c != run.CodeJobNotFound {
		t.Errorf("updating a missing job: %q", c)
	}
}

// A post job is saved only with the consent recorded (UI-0008 section 2).
func TestScheduleSavePostJobNeedsConsent(t *testing.T) {
	ctx := context.Background()
	s, db, clock, _ := newSchedules(t)

	in := goodInput()
	in.Mode = job.ModePost
	if c, f := runCode(func() error { _, err := s.SaveBotJob(ctx, in); return err }()); c != run.CodeJobInvalid || f != "spec.postConsentAt" {
		t.Fatalf("post without consent: %q (%q)", c, f)
	}
	if list, _ := db.ListJobs(ctx); len(list) != 0 {
		t.Fatal("a post job without consent was stored")
	}

	in.PostConsent = true
	saved, err := s.SaveBotJob(ctx, in)
	if err != nil || saved.Mode != job.ModePost || saved.PostConsentAt == nil || !saved.PostConsentAt.Equal(clock.Now().UTC().Truncate(time.Second)) {
		t.Fatalf("post with consent = %+v, %v", saved, err)
	}
	consent := *saved.PostConsentAt

	// A later edit without ticking the box keeps the earlier consent (and its
	// time): the user is not asked again for what they already accepted.
	clock.Add(time.Hour)
	in.ID, in.PostConsent, in.Name = saved.ID, false, "renamed"
	again, err := s.SaveBotJob(ctx, in)
	if err != nil || again.PostConsentAt == nil || !again.PostConsentAt.Equal(consent) {
		t.Fatalf("edit = %+v, %v", again, err)
	}

	// Back to preview drops the consent; post again needs a new one.
	in.Mode = job.ModePreview
	prev, err := s.SaveBotJob(ctx, in)
	if err != nil || prev.PostConsentAt != nil {
		t.Fatalf("preview = %+v, %v", prev, err)
	}
	in.Mode = job.ModePost
	if c, f := runCode(func() error { _, err := s.SaveBotJob(ctx, in); return err }()); c != run.CodeJobInvalid || f != "spec.postConsentAt" {
		t.Errorf("post after preview without consent: %q (%q)", c, f)
	}

	// Ticking the consent on a preview job does not make it a post job.
	in.Mode, in.PostConsent = job.ModePreview, true
	if p, err := s.SaveBotJob(ctx, in); err != nil || p.PostConsentAt != nil {
		t.Errorf("preview with the box ticked = %+v, %v", p, err)
	}
}

func TestScheduleAttentionResumeEnableAndDelete(t *testing.T) {
	ctx := context.Background()
	s, db, _, _ := newSchedules(t)
	saved, _ := s.SaveBotJob(ctx, goodInput())
	_ = db.SetAttention(ctx, saved.ID, "gh_not_authenticated", time.Now())

	list, _ := s.ListBotJobs(ctx)
	if list[0].AttentionCode != errcode.GHNotAuthenticated || list[0].AttentionAt == nil || list[0].NextDue != nil {
		t.Fatalf("a job that needs attention shows no next slot: %+v", list[0])
	}

	// Turning it off and on keeps the windows and does not clear the attention.
	off, err := s.SetJobEnabled(ctx, saved.ID, false)
	if err != nil || off.Enabled || len(off.Windows) != 1 || off.AttentionCode == "" {
		t.Fatalf("disabled = %+v, %v", off, err)
	}

	resumed, err := s.ResumeJob(ctx, saved.ID)
	if err != nil || resumed.AttentionCode != "" || resumed.AttentionAt != nil {
		t.Fatalf("resumed = %+v, %v", resumed, err)
	}

	// Saving (an edit) is also a fix.
	_ = db.SetAttention(ctx, saved.ID, "cli_too_old", time.Now())
	in := goodInput()
	in.ID = saved.ID
	if edited, err := s.SaveBotJob(ctx, in); err != nil || edited.AttentionCode != "" {
		t.Errorf("edited = %+v, %v", edited, err)
	}

	if err := s.DeleteJob(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	for name, f := range map[string]func() error{
		"delete": func() error { return s.DeleteJob(ctx, saved.ID) },
		"resume": func() error { _, err := s.ResumeJob(ctx, saved.ID); return err },
		"enable": func() error { _, err := s.SetJobEnabled(ctx, saved.ID, true); return err },
	} {
		if c, _ := runCode(f()); c != run.CodeJobNotFound {
			t.Errorf("%s of a missing job: %q", name, c)
		}
	}
}

type runnerFake struct{ id int64 }

func (r *runnerFake) RunJob(_ context.Context, id int64) (run.Run, error) {
	r.id = id
	return run.Run{ID: 1, JobID: id}, nil
}

func TestScheduleRunNowAndBudgetStatus(t *testing.T) {
	ctx := context.Background()
	s, db, clock, _ := newSchedules(t)
	r := &runnerFake{}
	s.d.Runner = r
	bot, _ := s.SaveBotJob(ctx, goodInput())
	if out, err := s.RunJobNow(ctx, bot.ID); err != nil || out.JobID != bot.ID || r.id != bot.ID {
		t.Errorf("RunJobNow = %+v, %v", out, err)
	}
	_ = db.Set(ctx, "budget.daily_usd", "2")
	finishedTask(db, 1.5, clock.Now())
	st, err := s.BudgetStatus(ctx)
	if err != nil || st.LimitUSD != 2 || st.SpentUSD != 1.5 || st.Exceeded {
		t.Errorf("BudgetStatus = %+v, %v", st, err)
	}
}

func TestScheduleAutostartIsOffUntilTheUserTurnsItOn(t *testing.T) {
	ctx := context.Background()
	s, _, _, auto := newSchedules(t)
	if on, err := s.AutostartEnabled(ctx); err != nil || on {
		t.Fatalf("a fresh install starts at login: %v, %v", on, err)
	}
	if err := s.SetAutostart(ctx, true); err != nil || !auto.on {
		t.Fatalf("enable: %v (on=%v)", err, auto.on)
	}
	if on, _ := s.AutostartEnabled(ctx); !on {
		t.Error("not reported as enabled")
	}
	if err := s.SetAutostart(ctx, false); err != nil || auto.on {
		t.Fatalf("disable: %v (on=%v)", err, auto.on)
	}

	auto.err = port.ErrAutostartUnsupported
	if c, _ := runCode(s.SetAutostart(ctx, true)); c != run.CodeAutostartUnsupported {
		t.Errorf("unsupported: %q", c)
	}
	auto.err = errors.New("permission denied: /Users/me/Library/LaunchAgents")
	err := s.SetAutostart(ctx, true)
	if c, _ := runCode(err); c != run.CodeAutostartFailed {
		t.Errorf("failed: %q", c)
	}
	if err.Error() != string(run.CodeAutostartFailed) {
		t.Errorf("the error text leaks the cause: %q", err.Error())
	}

	none := NewSchedules(SchedulesDeps{Clock: &fakeClock{loc: time.UTC}})
	if c, _ := runCode(none.SetAutostart(ctx, true)); c != run.CodeAutostartUnsupported {
		t.Errorf("no login item: %q", c)
	}
}
