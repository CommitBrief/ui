// SPDX-License-Identifier: GPL-3.0-or-later
import type { SchedulerStatus } from "../../api/schedules";
import { schedulerLine } from "../../app/schedulerText";
import { Button } from "../../components";
import { useI18n } from "../../i18n";
import { Notice } from "../Jobs/parts";

export interface SchedulerNoticeProps {
  /** Null until the first read; the notice then only says the app has to be running. */
  status: SchedulerStatus | null;
  now: number;
  /** A pause or resume call is in flight. */
  busy: boolean;
  onPause: () => void;
  onResume: () => void;
}

/**
 * The scheduler in one place: schedules run only while the app runs, whether it is
 * paused (the same state as the tray's Pause/Resume, which survives a restart), its
 * last and next look at the clock, and the button that flips it. Paused is loud:
 * a warning with its own icon and the reminder that manual runs still work.
 */
export function SchedulerNotice({ status, now, busy, onPause, onResume }: SchedulerNoticeProps) {
  const { t, locale } = useI18n();
  const paused = status?.paused === true;
  const line = status ? schedulerLine(status, now, locale, t) : null;
  return (
    <Notice
      tone={paused ? "warning" : "info"}
      icon={paused ? "statusCancelled" : "schedules"}
      title={t(paused ? "schedules.paused.title" : "schedules.alive.title")}
      action={
        status ? (
          paused ? (
            <Button variant="primary" size="sm" loading={busy} onClick={onResume}>
              {t("schedules.scheduler.resume")}
            </Button>
          ) : (
            <Button size="sm" loading={busy} onClick={onPause}>
              {t("schedules.scheduler.pause")}
            </Button>
          )
        ) : null
      }
    >
      <span>{t(paused ? "schedules.paused.text" : "schedules.alive.text")}</span>
      {line ? (
        <p>
          <span className="sr-only">{t("schedules.scheduler.label")}: </span>
          {line.text}
        </p>
      ) : null}
    </Notice>
  );
}
