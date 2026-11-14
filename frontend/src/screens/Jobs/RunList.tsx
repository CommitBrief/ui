// SPDX-License-Identifier: GPL-3.0-or-later
import type { Run, Task } from "../../api/jobs";
import { Button, StatusBadge } from "../../components";
import { useI18n, type MessageKey } from "../../i18n";
import { elapsed, formatCost, formatDateTime, formatDuration, formatRelative, parseTime } from "./format";
import { FailureNotice, ModeLabel } from "./parts";
import {
  badgeStatus,
  isActive,
  localReviewMode,
  localReviewRange,
  runErrorCode,
  runFindingCount,
  runMode,
  runVerdicts,
  statusName,
  taskTarget,
  type RepoIndex,
} from "./runView";
import styles from "./Jobs.module.css";

const kindLabel: Record<string, MessageKey> = {
  "review.local": "jobs.kind.local",
  "review.pr": "jobs.kind.pr",
  "bot.review_requested": "jobs.kind.bot",
};

/** `owner/name`, `owner/name#12`, or a stand-in for a repository that was deleted. */
export function useTaskTitle(repos: RepoIndex): (task: Pick<Task, "repositoryId" | "prNumber">) => string {
  const { t } = useI18n();
  return (task) => {
    const { slug, pr } = taskTarget(task, repos);
    const base = slug ?? t("jobs.repo.deleted");
    return pr > 0 ? `${base}#${pr}` : base;
  };
}

/** What kind of work a task is: "Local review, staged", "Pull request preview", "Review-requested bot". */
export function useTaskKind(): (task: Pick<Task, "kind" | "spec" | "prNumber">) => string {
  const { t } = useI18n();
  return (task) => {
    const base = t(kindLabel[task.kind] ?? "jobs.kind.other");
    const mode = localReviewMode(task);
    if (mode === "staged") return `${base} · ${t("jobs.mode.staged")}`;
    if (mode === "unstaged") return `${base} · ${t("jobs.mode.unstaged")}`;
    if (mode === "range") return `${base} · ${localReviewRange(task).join(" ")}`;
    return base;
  };
}

/** The title of a run: its first task's target, plus how many more tasks it has. */
export function useRunTitle(repos: RepoIndex): (run: Pick<Run, "id" | "tasks">) => string {
  const { t } = useI18n();
  const taskTitle = useTaskTitle(repos);
  return (run) => {
    const tasks = run.tasks ?? [];
    const first = tasks[0];
    if (!first) return t("jobs.run.title", { id: run.id });
    return tasks.length > 1 ? t("jobs.run.more", { title: taskTitle(first), count: tasks.length - 1 }) : taskTitle(first);
  };
}

export interface RunListProps {
  runs: readonly Run[];
  repos: RepoIndex;
  now: number;
  /** Ids of the runs whose cancel call is in flight. */
  cancelingIds: ReadonlySet<number>;
  onOpen: (run: Run) => void;
  onCancel: (run: Run) => void;
}

export function RunList({ runs, repos, now, cancelingIds, onOpen, onCancel }: RunListProps) {
  const { t, locale } = useI18n();
  const runTitle = useRunTitle(repos);
  const taskKind = useTaskKind();

  return (
    <div className={styles.listBox}>
      <ul className={styles.list} aria-label={t("jobs.list.label")}>
        {runs.map((run) => {
          const status = statusName(run.status);
          const active = isActive(run.status);
          const tasks = run.tasks ?? [];
          const started = parseTime(run.startedAt);
          const took = elapsed(run.startedAt, run.finishedAt, now);
          const findings = runFindingCount(run);
          const verdicts = runVerdicts(run);
          const code = runErrorCode(run);
          const title = runTitle(run);
          const kind = tasks[0] ? taskKind(tasks[0]) : t("jobs.kind.other");
          return (
            <li key={run.id} className={styles.item}>
              <div className={styles.main}>
                <div className={styles.titleRow}>
                  <h2 className={styles.runTitle}>
                    <button
                      type="button"
                      id={`run-open-${run.id}`}
                      className={styles.runOpen}
                      aria-label={t("jobs.open.aria", { title, id: run.id })}
                      onClick={() => onOpen(run)}
                    >
                      {title}
                    </button>
                  </h2>
                  <StatusBadge status={badgeStatus(run.status)} />
                  <ModeLabel mode={runMode(run)} />
                </div>
                <ul className={styles.metaList}>
                  <li>{kind}</li>
                  {started !== null ? (
                    <li>
                      <time dateTime={run.startedAt} title={formatDateTime(started, locale)}>
                        {t("jobs.meta.started", { when: formatRelative(started, now, locale) })}
                      </time>
                    </li>
                  ) : null}
                  {took !== null ? <li>{t(active ? "jobs.meta.running" : "jobs.meta.took", { duration: formatDuration(took, locale) })}</li> : null}
                  <li>{t("jobs.meta.cost", { cost: active && run.costUsd === 0 ? "—" : formatCost(run.costUsd) })}</li>
                  {status === "succeeded" ? <li>{t("jobs.meta.findings", { count: findings })}</li> : null}
                  {verdicts.map((verdict) => (
                    <li key={verdict}>{t("jobs.meta.verdict", { verdict })}</li>
                  ))}
                </ul>
                {code !== "" && status !== "canceled" ? <FailureNotice code={code} attention={status === "attention"} compact /> : null}
              </div>
              {active ? (
                <div className={styles.controls}>
                  <Button
                    size="sm"
                    icon="stop"
                    loading={cancelingIds.has(run.id)}
                    aria-label={t("jobs.cancel.aria", { title })}
                    onClick={() => onCancel(run)}
                  >
                    {t("jobs.cancel")}
                  </Button>
                </div>
              ) : null}
            </li>
          );
        })}
      </ul>
    </div>
  );
}
