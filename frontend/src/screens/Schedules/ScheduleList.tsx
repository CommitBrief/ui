// SPDX-License-Identifier: GPL-3.0-or-later
import type { Run, ScheduledJob } from "../../api/schedules";
import { Button, Icon, ModeBadge, Switch, type IconName } from "../../components";
import { useI18n, type MessageKey } from "../../i18n";
import { formatRelative, parseTime } from "../Jobs/format";
import { FailureNotice } from "../Jobs/parts";
import type { RepoIndex } from "../Jobs/runView";
import { jobHealth, type HealthState } from "./health";
import { systemTimeZone, windowSummary } from "./schedule";
import styles from "./Schedules.module.css";

// A health state always carries an icon and a text label, so it never rests on color alone.
const healthIcon: Record<HealthState, IconName> = {
  healthy: "statusSucceeded",
  failing: "statusFailed",
  attention: "statusAttention",
  paused: "statusCancelled",
  idle: "statusQueued",
};

const healthLabel: Record<HealthState, MessageKey> = {
  healthy: "schedules.health.healthy",
  failing: "schedules.health.failing",
  attention: "schedules.health.attention",
  paused: "schedules.health.paused",
  idle: "schedules.health.idle",
};

/** How many repo names a card lists before "+N more". */
const MAX_REPOS_SHOWN = 3;

export interface ScheduleListProps {
  jobs: readonly ScheduledJob[];
  /** The run history; null while it is unread or unreadable (health is then not claimed). */
  runs: readonly Run[] | null;
  repos: RepoIndex;
  now: number;
  /** The scheduler is paused: no job has a next run until it is resumed. */
  paused: boolean;
  /** Ids with a call in flight; each card's controls are held until its own call returns. */
  busyIds: ReadonlySet<number>;
  onToggle: (job: ScheduledJob, enabled: boolean) => void;
  onEdit: (job: ScheduledJob) => void;
  onDelete: (job: ScheduledJob) => void;
  onRunNow: (job: ScheduledJob) => void;
  onResume: (job: ScheduledJob) => void;
}

export function ScheduleList({ jobs, runs, repos, now, paused, busyIds, onToggle, onEdit, onDelete, onRunNow, onResume }: ScheduleListProps) {
  const { t, locale } = useI18n();
  const zone = systemTimeZone();

  return (
    <div className={styles.listBox}>
      <ul className={styles.list} aria-label={t("schedules.list.label")}>
        {jobs.map((job) => {
          const busy = busyIds.has(job.id);
          const health = runs ? jobHealth(job, runs) : null;
          const state: HealthState | "unknown" = health ? health.state : job.attentionCode !== "" ? "attention" : job.enabled ? "unknown" : "paused";
          const next = job.nextDue ? parseTime(job.nextDue) : null;
          const targets = job.targets ?? [];
          const shown = targets.slice(0, MAX_REPOS_SHOWN).map((id) => {
            const repo = repos.get(id);
            return repo ? `${repo.owner}/${repo.name}` : t("jobs.repo.deleted");
          });
          const windows = job.windows ?? [];
          return (
            <li key={job.id} className={`${styles.item} ${job.mode === "post" ? styles.itemPost : ""}`}>
              <div className={styles.main}>
                <div className={styles.titleRow}>
                  <h2 className={styles.name}>{job.name}</h2>
                  <ModeBadge mode={job.mode === "post" ? "post" : "preview"} />
                  <span className={`${styles.pill} ${styles[`pill-${state}`]}`}>
                    <Icon name={state === "unknown" ? "severityInfo" : healthIcon[state]} size={14} />
                    {t(state === "unknown" ? "schedules.health.unknown" : healthLabel[state])}
                  </span>
                </div>

                <ul className={styles.facts}>
                  <li className={styles.fact}>
                    <Icon name="repos" size={14} className={styles.factIcon} />
                    <span className={styles.factText}>
                      <span className="sr-only">{t("schedules.targets.label")}: </span>
                      {targets.length === 0 ? (
                        t("schedules.targets.none")
                      ) : (
                        <span className={`${styles.mono} ${styles.factValue}`}>
                          {shown.join(", ")}
                          {targets.length > MAX_REPOS_SHOWN ? ` ${t("schedules.targets.more", { count: targets.length - MAX_REPOS_SHOWN })}` : ""}
                        </span>
                      )}
                    </span>
                  </li>
                  <li className={styles.fact}>
                    <Icon name="schedules" size={14} className={styles.factIcon} />
                    <span className={styles.factText}>
                      <span className="sr-only">{t("schedules.windows.label")}: </span>
                      {windows.length === 0 ? (
                        t("schedules.windows.none")
                      ) : (
                        <ul className={styles.windowList}>
                          {windows.map((w, index) => {
                            const summary = windowSummary(w, t);
                            return (
                              <li key={index} className={styles.factValue}>
                                {w.tz !== zone ? t("schedules.window.zone", { summary, zone: w.tz }) : summary}
                              </li>
                            );
                          })}
                        </ul>
                      )}
                    </span>
                  </li>
                  <li className={styles.fact}>
                    <Icon name="statusQueued" size={14} className={styles.factIcon} />
                    <span className={styles.factText}>
                      {next !== null && !paused ? (
                        <time dateTime={job.nextDue ?? undefined}>{t("schedules.next", { when: formatRelative(next, now, locale) })}</time>
                      ) : (
                        t(paused && job.enabled && job.attentionCode === "" ? "schedules.next.paused" : "schedules.next.none")
                      )}
                    </span>
                  </li>
                  {health ? (
                    <li className={styles.fact}>
                      <Icon name={health.failStreak > 0 ? "statusFailed" : "statusSucceeded"} size={14} className={styles.factIcon} />
                      <span className={styles.factText}>
                        {health.lastSuccessAt !== null
                          ? t("schedules.health.lastSuccess", { when: formatRelative(health.lastSuccessAt, now, locale) })
                          : t("schedules.health.noSuccess")}
                        {health.failStreak > 0 ? ` · ${t("schedules.health.streak", { count: health.failStreak })}` : ""}
                      </span>
                    </li>
                  ) : null}
                  <li className={styles.fact}>
                    <Icon name="severityInfo" size={14} className={styles.factIcon} />
                    <span className={styles.factText}>{t(job.includeDrafts ? "schedules.drafts.included" : "schedules.drafts.skipped")}</span>
                  </li>
                </ul>

                {job.attentionCode !== "" ? (
                  <div className={styles.resumeRow}>
                    <div className={styles.resumeNotice}>
                      <FailureNotice code={job.attentionCode} attention />
                    </div>
                    <Button
                      variant="primary"
                      size="sm"
                      loading={busy}
                      aria-label={t("schedules.resume.aria", { name: job.name })}
                      onClick={() => onResume(job)}
                    >
                      {t("schedules.resume")}
                    </Button>
                  </div>
                ) : null}
              </div>

              <div className={styles.controls}>
                <Switch
                  className={styles.toggle}
                  label={t(job.enabled ? "schedules.enabled" : "schedules.enabled.off")}
                  aria-label={t("schedules.enabled.aria", { name: job.name })}
                  checked={job.enabled}
                  disabled={busy}
                  onChange={(event) => onToggle(job, event.target.checked)}
                />
                <div className={styles.actions}>
                  <Button size="sm" disabled={busy} aria-label={t("schedules.runNow.aria", { name: job.name })} onClick={() => onRunNow(job)}>
                    {t("schedules.runNow")}
                  </Button>
                  <Button variant="ghost" size="sm" icon="pencil" disabled={busy} aria-label={t("schedules.edit.aria", { name: job.name })} onClick={() => onEdit(job)}>
                    {t("schedules.edit")}
                  </Button>
                  <Button variant="ghost" size="sm" icon="trash" disabled={busy} aria-label={t("schedules.delete.aria", { name: job.name })} onClick={() => onDelete(job)}>
                    {t("schedules.delete")}
                  </Button>
                </div>
              </div>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
