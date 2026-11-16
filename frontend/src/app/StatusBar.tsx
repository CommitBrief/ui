// SPDX-License-Identifier: GPL-3.0-or-later
import { useCallback, useEffect, useRef, useState } from "react";
import { budgetStatus, listBotJobs, onRunChanged, onSchedulesChanged, type BudgetStatus, type ScheduledJob } from "../api/schedules";
import { Icon } from "../components";
import { useI18n } from "../i18n";
import { formatCost, formatRelative, parseTime } from "../screens/Jobs/format";
import { useNow } from "../screens/Jobs/parts";
import { schedulerLine } from "./schedulerText";
import { useSchedulerStatus } from "./useSchedulerStatus";
import styles from "./StatusBar.module.css";

/** How often the bar reads again, besides after every run event. */
const REFRESH_MS = 60_000;

interface Snapshot {
  jobs: ScheduledJob[];
  budget: BudgetStatus;
}

/**
 * Wording of the bar's figures, split out so it can be tested without a runtime.
 * `active` counts the jobs the scheduler will run: on and not waiting for the user.
 */
export function summarize(jobs: readonly { enabled: boolean; attentionCode: string; nextDue: string | null }[]): {
  active: number;
  attention: number;
  nextDueAt: number | null;
} {
  let active = 0;
  let attention = 0;
  let nextDueAt: number | null = null;
  for (const job of jobs) {
    if (job.attentionCode !== "") attention++;
    if (!job.enabled || job.attentionCode !== "") continue;
    active++;
    const due = parseTime(job.nextDue);
    if (due !== null && (nextDueAt === null || due < nextDueAt)) nextDueAt = due;
  }
  return { active, attention, nextDueAt };
}

/**
 * The bar at the foot of every screen: how many schedules run and when the next
 * slot is, the scheduler's last (and next) look at the clock or that it is paused,
 * today's cost against the daily budget and how many schedules wait for the user.
 * It stays quiet for screen
 * readers (no live region); the Schedules screen is where details live.
 */
export function StatusBar() {
  const { t, locale } = useI18n();
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null);
  const [failed, setFailed] = useState(false);
  const now = useNow(false);
  const scheduler = useSchedulerStatus();

  const alive = useRef(true);
  const read = useCallback(
    () =>
      Promise.all([listBotJobs(), budgetStatus()]).then(
        ([jobs, budget]) => {
          if (!alive.current) return;
          setSnapshot({ jobs, budget });
          setFailed(false);
        },
        () => {
          if (alive.current) setFailed(true);
        },
      ),
    [],
  );

  useEffect(() => {
    alive.current = true;
    let timer = 0;
    const reload = () => {
      window.clearTimeout(timer);
      timer = window.setTimeout(() => void read(), 500);
    };
    const offRun = onRunChanged(reload);
    const offJobs = onSchedulesChanged(reload);
    void read();
    const interval = window.setInterval(() => void read(), REFRESH_MS);
    return () => {
      alive.current = false;
      window.clearTimeout(timer);
      window.clearInterval(interval);
      offRun();
      offJobs();
    };
  }, [read]);

  let content = null;
  if (failed && snapshot === null) {
    content = (
      <li className={styles.item}>
        <Icon name="severityInfo" size={14} />
        {t("statusbar.unavailable")}
      </li>
    );
  } else if (snapshot) {
    const { active, attention, nextDueAt } = summarize(snapshot.jobs);
    const { budget } = snapshot;
    const spent = formatCost(budget.spentUsd);
    const line = scheduler.status ? schedulerLine(scheduler.status, scheduler.now, locale, t) : null;
    content = (
      <>
        <li className={styles.item}>
          <Icon name="schedules" size={14} />
          <span>
            {active > 0 ? t("statusbar.active", { count: active }) : t("statusbar.active.none")}
            {nextDueAt !== null && scheduler.status?.paused !== true ? ` · ${t("statusbar.next", { when: formatRelative(nextDueAt, now, locale) })}` : ""}
          </span>
        </li>
        {line ? (
          <li className={`${styles.item} ${line.paused ? styles.warn : styles.optional}`}>
            <Icon name={line.paused ? "statusCancelled" : "jobs"} size={14} />
            {line.text}
          </li>
        ) : null}
        <li className={`${styles.item} ${budget.exceeded ? styles.warn : ""}`}>
          {budget.exceeded ? <Icon name="statusAttention" size={14} /> : null}
          <span>
            {budget.limitUsd > 0 ? t("statusbar.cost", { spent, limit: formatCost(budget.limitUsd) }) : t("statusbar.cost.noLimit", { spent })}
            {budget.exceeded ? ` · ${t("statusbar.budget.exceeded")}` : ""}
          </span>
        </li>
        {attention > 0 ? (
          <li className={`${styles.item} ${styles.warn}`}>
            <Icon name="statusAttention" size={14} />
            <a href="#/schedules">{t("statusbar.attention", { count: attention })}</a>
          </li>
        ) : null}
        {failed ? (
          <li className={styles.item}>
            <Icon name="severityInfo" size={14} />
            {t("statusbar.unavailable")}
          </li>
        ) : null}
      </>
    );
  }

  return (
    <footer className={styles.bar} aria-label={t("statusbar.label")}>
      <ul className={styles.list}>{content}</ul>
    </footer>
  );
}
