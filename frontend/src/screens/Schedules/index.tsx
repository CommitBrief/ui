// SPDX-License-Identifier: GPL-3.0-or-later
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { cliStatus, type CLIStatus } from "../../api/settings";
import { listRuns, type Run } from "../../api/jobs";
import { describeRepoError, listRepos, toRepoError, type Repository } from "../../api/repos";
import {
  ScheduleError,
  deleteBotJob,
  describeScheduleError,
  listBotJobs,
  onRunChanged,
  pauseScheduler,
  resumeBotJob,
  resumeScheduler,
  runBotJobNow,
  setBotJobEnabled,
  toScheduleError,
  trackedPRs,
  type ScheduledJob,
  type TrackedPRView,
} from "../../api/schedules";
import { useSchedulerStatus } from "../../app/useSchedulerStatus";
import { Button, EmptyState, PageHeader, useToast } from "../../components";
import { useI18n } from "../../i18n";
import { Notice, useNow } from "../Jobs/parts";
import { indexRepos } from "../Jobs/runView";
import { ConfirmDialog } from "./ConfirmDialog";
import { SchedulerNotice } from "./SchedulerNotice";
import { ScheduleForm } from "./ScheduleForm";
import { ScheduleList } from "./ScheduleList";
import { TrackedPRs } from "./TrackedPRs";
import styles from "./Schedules.module.css";

/** How many runs of history the health and tracked-PR views read. */
const RUN_HISTORY = 500;
/** The schedules and the run history are read again this often, so "next run" and the health stay true while the screen is open. */
const REFRESH_MS = 60_000;

type FormTarget = { job: ScheduledJob | null };
type RunNowTarget = { job: ScheduledJob };

/**
 * Schedules and bot: the review-requested bot jobs with their time windows, mode
 * and health, a form to create or change them, and the pull requests the bot has
 * looked at. Preview is the default and the quiet mode; post is loud, asks for
 * consent once and is off when the CLI cannot post (UI-0008).
 */
export function SchedulesScreen() {
  const { t } = useI18n();
  const toast = useToast();
  const [jobs, setJobs] = useState<ScheduledJob[] | null>(null);
  const [loadError, setLoadError] = useState<ScheduleError | null>(null);
  const [runs, setRuns] = useState<Run[] | null>(null);
  const [runsFailed, setRunsFailed] = useState(false);
  const [repos, setRepos] = useState<Repository[]>([]);
  const [tracked, setTracked] = useState<TrackedPRView[] | null>(null);
  const [trackedFailed, setTrackedFailed] = useState(false);
  const [schedulerBusy, setSchedulerBusy] = useState(false);
  const scheduler = useSchedulerStatus();
  const [cli, setCli] = useState<CLIStatus | null>(null);
  const [cliFailed, setCliFailed] = useState(false);
  const [form, setForm] = useState<FormTarget | null>(null);
  const [deleting, setDeleting] = useState<ScheduledJob | null>(null);
  const [runNowPost, setRunNowPost] = useState<RunNowTarget | null>(null);
  const [dialogBusy, setDialogBusy] = useState(false);
  const [dialogError, setDialogError] = useState<string | null>(null);
  const [busyIds, setBusyIds] = useState<ReadonlySet<number>>(new Set());

  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  const now = useNow(false);
  const repoIndex = useMemo(() => indexRepos(repos), [repos]);

  const refreshJobs = useCallback(
    (quiet: boolean): Promise<void> =>
      listBotJobs().then(
        (next) => {
          if (!alive.current) return;
          setJobs(next);
          setLoadError(null);
        },
        (error: unknown) => {
          if (!alive.current) return;
          if (quiet) toast({ kind: "error", title: t("schedules.loadError.title"), description: describeScheduleError(toScheduleError(error), t) });
          else setLoadError(toScheduleError(error));
        },
      ),
    [t, toast],
  );

  const refreshRuns = useCallback(
    () =>
      listRuns(RUN_HISTORY).then(
        (next) => {
          if (!alive.current) return;
          setRuns(next);
          setRunsFailed(false);
        },
        () => {
          // The list keeps what it has; the notice says the history is unreadable.
          if (alive.current) setRunsFailed(true);
        },
      ),
    [],
  );

  const refreshTracked = useCallback(
    () =>
      trackedPRs().then(
        (next) => {
          if (!alive.current) return;
          setTracked(next);
          setTrackedFailed(false);
        },
        () => {
          // The table keeps what it has; the notice says the list is unreadable.
          if (alive.current) setTrackedFailed(true);
        },
      ),
    [],
  );

  const refreshCli = useCallback(
    () =>
      cliStatus().then(
        (next) => {
          if (!alive.current) return;
          setCli(next);
          setCliFailed(false);
        },
        () => {
          if (alive.current) setCliFailed(true);
        },
      ),
    [],
  );

  const refreshRepos = useCallback(
    () =>
      listRepos().then(
        (list) => {
          if (alive.current) setRepos(list);
        },
        (error: unknown) => {
          if (alive.current) toast({ kind: "error", title: t("jobs.toast.reposFailed"), description: describeRepoError(toRepoError(error), t) });
        },
      ),
    [t, toast],
  );

  // Subscribe to run events first, then read, so no change between the two is missed. Bursts become one re-read.
  useEffect(() => {
    let timer = 0;
    const reload = () => {
      window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        void refreshRuns();
        void refreshJobs(true);
        void refreshTracked();
      }, 400);
    };
    const off = onRunChanged(reload);
    void refreshJobs(false);
    void refreshRuns();
    void refreshTracked();
    void refreshRepos();
    void refreshCli();
    // A run that found nothing to review ends without a status event, so the history is read on the timer too.
    const interval = window.setInterval(() => {
      void refreshJobs(true);
      void refreshRuns();
      void refreshTracked();
    }, REFRESH_MS);
    return () => {
      window.clearTimeout(timer);
      window.clearInterval(interval);
      off();
    };
    // Mount only: the refreshers are stable for the life of the screen apart from the language, which the next read picks up.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Each tick can start a run, flag a schedule for attention or move its next slot, none of which sends an event of its own: read again after every tick.
  const lastTick = scheduler.status?.lastTick ?? null;
  const seenTick = useRef<string | null | undefined>(undefined);
  useEffect(() => {
    const first = seenTick.current === undefined;
    const changed = seenTick.current !== lastTick;
    seenTick.current = lastTick;
    if (first || !changed) return;
    void refreshJobs(true);
    void refreshRuns();
    void refreshTracked();
  }, [lastTick, refreshJobs, refreshRuns, refreshTracked]);

  const setBusy = (id: number, on: boolean) =>
    setBusyIds((current) => {
      const next = new Set(current);
      if (on) next.add(id);
      else next.delete(id);
      return next;
    });

  const replaceJob = (saved: ScheduledJob) => setJobs((current) => (current ? current.map((job) => (job.id === saved.id ? saved : job)) : current));

  const changeScheduler = async (pause: boolean) => {
    setSchedulerBusy(true);
    try {
      scheduler.set(await (pause ? pauseScheduler() : resumeScheduler()));
      if (alive.current) void refreshJobs(true);
    } catch (error) {
      if (alive.current) {
        toast({ kind: "error", title: t(pause ? "schedules.scheduler.pause.failed" : "schedules.scheduler.resume.failed"), description: describeScheduleError(toScheduleError(error), t) });
      }
    } finally {
      if (alive.current) setSchedulerBusy(false);
    }
  };

  const toggle = async (job: ScheduledJob, enabled: boolean) => {
    setBusy(job.id, true);
    try {
      replaceJob(await setBotJobEnabled(job.id, enabled));
    } catch (error) {
      if (alive.current) toast({ kind: "error", title: t("schedules.toggle.failed"), description: describeScheduleError(toScheduleError(error), t) });
    } finally {
      if (alive.current) setBusy(job.id, false);
    }
  };

  const resume = async (job: ScheduledJob) => {
    setBusy(job.id, true);
    try {
      replaceJob(await resumeBotJob(job.id));
      if (alive.current) toast({ kind: "success", title: t("schedules.resumed") });
    } catch (error) {
      if (alive.current) toast({ kind: "error", title: t("schedules.resume.failed"), description: describeScheduleError(toScheduleError(error), t) });
    } finally {
      if (alive.current) setBusy(job.id, false);
    }
  };

  /** Starts a run now. Returns true when it started. */
  const startNow = async (job: ScheduledJob): Promise<boolean> => {
    setBusy(job.id, true);
    try {
      await runBotJobNow(job.id);
      if (alive.current) {
        toast({ kind: "success", title: t("schedules.runNow.started"), description: t("schedules.runNow.startedText") });
        void refreshRuns();
      }
      return true;
    } catch (error) {
      const message = describeScheduleError(toScheduleError(error), t);
      if (alive.current) {
        if (runNowPost) setDialogError(message);
        else toast({ kind: "error", title: t("schedules.runNow.failed"), description: message });
      }
      return false;
    } finally {
      if (alive.current) setBusy(job.id, false);
    }
  };

  const runNow = (job: ScheduledJob) => {
    // A post job writes to GitHub right away: ask first.
    if (job.mode === "post") {
      setDialogError(null);
      setRunNowPost({ job });
    } else void startNow(job);
  };

  const confirmRunNow = async () => {
    if (!runNowPost) return;
    setDialogBusy(true);
    const started = await startNow(runNowPost.job);
    if (!alive.current) return;
    setDialogBusy(false);
    if (started) setRunNowPost(null);
  };

  const confirmDelete = async () => {
    if (!deleting) return;
    setDialogBusy(true);
    setDialogError(null);
    try {
      await deleteBotJob(deleting.id);
      if (!alive.current) return;
      setJobs((current) => (current ? current.filter((job) => job.id !== deleting.id) : current));
      setDeleting(null);
      toast({ kind: "success", title: t("schedules.deleted") });
    } catch (error) {
      if (alive.current) setDialogError(describeScheduleError(toScheduleError(error), t));
    } finally {
      if (alive.current) setDialogBusy(false);
    }
  };

  // The repositories and the CLI are read again when the form opens, so a repo added or a CLI updated a moment ago is there.
  const openForm = (job: ScheduledJob | null) => {
    void refreshRepos();
    void refreshCli();
    setForm({ job });
  };

  const posting = cli?.capabilities.posting === true;

  const hasJobs = jobs !== null && jobs.length > 0;
  const newSchedule = (
    <Button variant="primary" icon="plus" onClick={() => openForm(null)}>
      {t("schedules.new")}
    </Button>
  );

  let body;
  if (loadError) {
    body = (
      <Notice
        tone="error"
        title={t("schedules.loadError.title")}
        live="alert"
        action={
          <Button
            onClick={() => {
              setLoadError(null);
              void refreshJobs(false);
            }}
          >
            {t("schedules.retry")}
          </Button>
        }
      >
        {describeScheduleError(loadError, t)}
      </Notice>
    );
  } else if (jobs === null) {
    body = (
      <p className={styles.muted} role="status">
        {t("schedules.loading")}
      </p>
    );
  } else if (jobs.length === 0) {
    body = (
      <EmptyState
        icon="schedules"
        title={t("screen.schedules.empty.title")}
        description={t("screen.schedules.empty.description")}
        action={newSchedule}
      />
    );
  } else {
    body = (
      <>
        <ScheduleList
          jobs={jobs}
          runs={runs}
          repos={repoIndex}
          now={now}
          paused={scheduler.status?.paused === true}
          busyIds={busyIds}
          onToggle={(job, enabled) => void toggle(job, enabled)}
          onEdit={openForm}
          onDelete={(job) => {
            setDialogError(null);
            setDeleting(job);
          }}
          onRunNow={runNow}
          onResume={(job) => void resume(job)}
        />
        {tracked ? <TrackedPRs rows={tracked} now={now} /> : null}
        {trackedFailed ? (
          <Notice
            tone="warning"
            title={t("schedules.tracked.error.title")}
            action={
              <Button size="sm" onClick={() => void refreshTracked()}>
                {t("schedules.retry")}
              </Button>
            }
          >
            {t("schedules.tracked.error.text")}
          </Notice>
        ) : null}
      </>
    );
  }

  return (
    <>
      <PageHeader title={t("screen.schedules.title")} description={t("screen.schedules.description")} actions={hasJobs ? newSchedule : null} />
      <div className={styles.stack}>
        <SchedulerNotice
          status={scheduler.status}
          now={scheduler.now}
          busy={schedulerBusy}
          onPause={() => void changeScheduler(true)}
          onResume={() => void changeScheduler(false)}
        />
        {runsFailed && hasJobs ? (
          <Notice
            tone="warning"
            title={t("schedules.runsError.title")}
            action={
              <Button size="sm" onClick={() => void refreshRuns()}>
                {t("schedules.retry")}
              </Button>
            }
          >
            {t("schedules.runsError.text")}
          </Notice>
        ) : null}
        {body}
      </div>

      {form ? (
        <ScheduleForm
          job={form.job}
          repos={repos}
          posting={posting}
          cliUnknown={cliFailed || cli === null}
          onClose={() => setForm(null)}
          onSaved={(saved) => {
            setForm(null);
            toast({ kind: "success", title: t("schedules.saved") });
            setJobs((current) => {
              if (!current) return [saved];
              return current.some((job) => job.id === saved.id) ? current.map((job) => (job.id === saved.id ? saved : job)) : [...current, saved];
            });
          }}
        />
      ) : null}

      {deleting ? (
        <ConfirmDialog
          title={t("schedules.delete.title", { name: deleting.name })}
          description={t("schedules.delete.description")}
          confirmLabel={t("schedules.delete")}
          confirmIcon="trash"
          confirmVariant="danger"
          busy={dialogBusy}
          error={dialogError}
          onConfirm={() => void confirmDelete()}
          onClose={() => setDeleting(null)}
        />
      ) : null}

      {runNowPost ? (
        <ConfirmDialog
          title={t("schedules.runNow.post.title", { name: runNowPost.job.name })}
          description={t("schedules.runNow.post.text")}
          confirmLabel={t("schedules.confirm.run")}
          confirmIcon="send"
          busy={dialogBusy}
          error={dialogError}
          onConfirm={() => void confirmRunNow()}
          onClose={() => setRunNowPost(null)}
        />
      ) : null}
    </>
  );
}
