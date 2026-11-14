// SPDX-License-Identifier: GPL-3.0-or-later
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  JobError,
  cancelRun,
  describeJobError,
  getRun,
  onRunStatus,
  onTaskStatus,
  toJobError,
  type Run,
  type Task,
} from "../../api/jobs";
import { Button, Icon, PageHeader, StatusBadge, useToast } from "../../components";
import { useI18n } from "../../i18n";
import { FindingsTable } from "./FindingsTable";
import { elapsed, formatCost, formatDateTime, formatDuration, formatRelative, parseTime, shortOid } from "./format";
import { parseFindings, parseReport, type ReportView } from "./findings";
import { LogPanel } from "./LogPanel";
import { Facts, FailureNotice, ModeLabel, ModeNotice, Notice, useNow } from "./parts";
import { useRunTitle, useTaskKind, useTaskTitle } from "./RunList";
import { badgeStatus, isActive, runMode, statusName, taskMode, type RepoIndex } from "./runView";
import styles from "./Jobs.module.css";

export interface RunDetailProps {
  runId: number;
  repos: RepoIndex;
  onBack: () => void;
}

const triggerKey = {
  manual: "jobs.trigger.manual",
  schedule: "jobs.trigger.schedule",
  catchup: "jobs.trigger.catchup",
} as const;

/** The task to show first: the one still working, else the first. */
function defaultTask(run: Run): Task | undefined {
  const tasks = run.tasks ?? [];
  return tasks.find((task) => isActive(task.status)) ?? tasks[0];
}

export function RunDetail({ runId, repos, onBack }: RunDetailProps) {
  const { t, locale } = useI18n();
  const toast = useToast();
  const runTitle = useRunTitle(repos);
  const taskTitle = useTaskTitle(repos);
  const taskKind = useTaskKind();
  const [run, setRun] = useState<Run | null>(null);
  const [error, setError] = useState<JobError | null>(null);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [canceling, setCanceling] = useState(false);
  const alive = useRef(true);
  // The clock ticks every second only while this run is still going.
  const now = useNow(run === null || isActive(run.status));

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  // The shell moves focus to the h1 on navigation; opening a run is a navigation inside the screen.
  useEffect(() => {
    document.querySelector<HTMLElement>("[data-page-heading]")?.focus();
  }, [runId]);

  const load = useCallback(
    (): Promise<void> =>
      getRun(runId).then(
        (next) => {
          if (!alive.current) return;
          setRun(next);
          setError(null);
        },
        (failure: unknown) => {
          if (alive.current) setError(toJobError(failure));
        },
      ),
    [runId],
  );

  // Subscribe first, then read: a change that lands between the two is not missed.
  // Bursts of events (every task of a run reports) become one re-read.
  useEffect(() => {
    let timer = 0;
    const reload = () => {
      window.clearTimeout(timer);
      timer = window.setTimeout(() => void load(), 200);
    };
    const offRun = onRunStatus((event) => {
      if (event.runId === runId) reload();
    });
    const offTask = onTaskStatus((event) => {
      if (event.runId === runId) reload();
    });
    void load();
    return () => {
      window.clearTimeout(timer);
      offRun();
      offTask();
    };
  }, [load, runId]);

  const tasks = useMemo(() => run?.tasks ?? [], [run]);
  const task = tasks.find((item) => item.id === selectedId) ?? (run ? defaultTask(run) : undefined);
  const findings = useMemo(() => (task ? parseFindings(task.findings) : []), [task]);
  const report = useMemo<ReportView | null>(() => (task ? parseReport(task.report) : null), [task]);

  const cancel = async () => {
    setCanceling(true);
    try {
      await cancelRun(runId);
      if (alive.current) await load();
    } catch (failure) {
      if (alive.current) toast({ kind: "error", title: t("jobs.cancel.failed"), description: describeJobError(toJobError(failure), t) });
    } finally {
      if (alive.current) setCanceling(false);
    }
  };

  const back = (
    <Button variant="ghost" icon="arrowLeft" onClick={onBack}>
      {t("jobs.back")}
    </Button>
  );

  if (error && !run) {
    return (
      <>
        <PageHeader title={t("jobs.run.title", { id: runId })} actions={back} />
        <Notice
          tone="error"
          title={t("jobs.run.loadError")}
          live="alert"
          action={
            error.code === "run_not_found" ? undefined : (
              <Button
                onClick={() => {
                  setError(null);
                  void load();
                }}
              >
                {t("jobs.retry")}
              </Button>
            )
          }
        >
          {describeJobError(error, t)}
        </Notice>
      </>
    );
  }

  if (!run) {
    return (
      <>
        <PageHeader title={t("jobs.run.title", { id: runId })} actions={back} />
        <p className={styles.muted} role="status">
          {t("common.loading")}
        </p>
      </>
    );
  }

  const active = isActive(run.status);
  const mode = runMode(run);
  // Every task that posts (or may have posted), with what its report says it did.
  const postItems = tasks
    .filter((item) => taskMode(item) === "post")
    .map((item) => ({ title: taskTitle(item), remote: parseReport(item.report)?.remote ?? null, active: isActive(item.status) }));
  const started = parseTime(run.startedAt);
  const took = elapsed(run.startedAt, run.finishedAt, now);
  const taskStatus = task ? statusName(task.status) : "queued";
  const code = task?.errorCode !== undefined && task.errorCode !== "" ? task.errorCode : run.errorCode;
  const trigger = triggerKey[run.trigger as keyof typeof triggerKey] ?? triggerKey.manual;

  const rows = [
    { label: t("jobs.fact.status"), value: <StatusBadge status={badgeStatus(run.status)} /> },
    { label: t("jobs.fact.mode"), value: <ModeLabel mode={mode} /> },
    { label: t("jobs.fact.trigger"), value: t(trigger) },
    ...(started !== null
      ? [
          {
            label: t("jobs.fact.started"),
            value: (
              <time dateTime={run.startedAt}>
                {formatDateTime(started, locale)} ({formatRelative(started, now, locale)})
              </time>
            ),
          },
        ]
      : []),
    ...(took !== null ? [{ label: t("jobs.fact.duration"), value: formatDuration(took, locale) }] : []),
    { label: t("jobs.fact.cost"), value: active && run.costUsd === 0 ? "—" : formatCost(run.costUsd) },
    ...(task && task.cliVersion !== "" ? [{ label: t("jobs.fact.cli"), value: <code className={styles.mono}>{task.cliVersion}</code> }] : []),
    ...(report && report.provider !== ""
      ? [
          {
            label: t("jobs.fact.model"),
            value: (
              <span className={styles.mono}>
                {report.model !== "" ? `${report.provider} / ${report.model}` : report.provider}
                {report.cached ? ` (${t("jobs.fact.cached")})` : ""}
              </span>
            ),
          },
        ]
      : []),
    ...(task && task.headOid !== "" ? [{ label: t("jobs.fact.head"), value: <code className={styles.mono}>{shortOid(task.headOid)}</code> }] : []),
    ...(task && task.verdict !== "" ? [{ label: t("jobs.fact.verdict"), value: task.verdict }] : []),
  ];

  return (
    <>
      <PageHeader
        title={runTitle(run)}
        description={t("jobs.run.subtitle", { id: run.id })}
        actions={
          <>
            {back}
            {active ? (
              <Button variant="danger" icon="stop" loading={canceling} onClick={() => void cancel()}>
                {t("jobs.cancel.run")}
              </Button>
            ) : null}
          </>
        }
      />
      <div className={styles.stack}>
        {error ? (
          <Notice tone="warning" live="status">
            {t("jobs.run.refreshFailed", { message: describeJobError(error, t) })}
          </Notice>
        ) : null}

        <ModeNotice mode={mode} items={postItems} />

        {code !== "" && statusName(run.status) !== "canceled" && !active ? (
          <FailureNotice code={code} attention={statusName(run.status) === "attention"} />
        ) : null}
        {statusName(run.status) === "canceled" ? (
          <Notice tone="info" icon="statusCancelled">
            {t("jobs.canceled.text")}
          </Notice>
        ) : null}

        <section className={styles.card} aria-label={t("jobs.summary.label")}>
          <Facts rows={rows} />
        </section>

        {tasks.length > 1 ? (
          <section className={styles.card} aria-labelledby="tasks-title">
            <h2 id="tasks-title" className={styles.cardTitle}>
              {t("jobs.tasks.title", { count: tasks.length })}
            </h2>
            <ul className={styles.taskList}>
              {tasks.map((item) => (
                <li key={item.id}>
                  <button
                    type="button"
                    className={styles.taskButton}
                    aria-pressed={item.id === task?.id}
                    onClick={() => setSelectedId(item.id)}
                  >
                    <span className={styles.taskName}>{taskTitle(item)}</span>
                    <span className={styles.taskKind}>{taskKind(item)}</span>
                    <StatusBadge status={badgeStatus(item.status)} />
                    {statusName(item.status) === "succeeded" ? (
                      <span className={styles.taskCount}>{t("jobs.meta.findings", { count: item.findingCount })}</span>
                    ) : null}
                  </button>
                </li>
              ))}
            </ul>
          </section>
        ) : null}

        {task ? (
          <>
            {tasks.length > 1 ? (
              <p className={styles.taskHeading}>
                <Icon name="jobs" size={16} />
                {taskTitle(task)} · {taskKind(task)}
              </p>
            ) : (
              <p className={styles.taskHeading}>
                <Icon name="jobs" size={16} />
                {taskKind(task)}
              </p>
            )}
            {/* A running task shows its log first; a finished one shows what it found. */}
            {taskStatus === "queued" || taskStatus === "running" ? (
              <>
                <LogPanel key={task.id} taskId={task.id} status={task.status} />
                <section className={styles.card} aria-labelledby="findings-title">
                  <h2 id="findings-title" className={styles.cardTitle}>
                    {t("jobs.findings.title")}
                  </h2>
                  <p className={styles.muted}>{t("jobs.findings.pending")}</p>
                </section>
              </>
            ) : (
              <>
                <section className={styles.card} aria-labelledby="findings-title">
                  <h2 id="findings-title" className={styles.cardTitle}>
                    {t("jobs.findings.title")}
                    {taskStatus === "succeeded" || findings.length > 0 ? <span className={styles.cardCount}>{findings.length}</span> : null}
                  </h2>
                  {taskStatus === "succeeded" || findings.length > 0 || report !== null ? (
                    <FindingsTable findings={findings} report={report} />
                  ) : (
                    <p className={styles.muted}>{t("jobs.findings.incomplete")}</p>
                  )}
                </section>
                <LogPanel key={task.id} taskId={task.id} status={task.status} />
              </>
            )}
          </>
        ) : (
          <p className={styles.muted}>{t("jobs.tasks.none")}</p>
        )}
      </div>
    </>
  );
}
