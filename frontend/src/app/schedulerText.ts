// SPDX-License-Identifier: GPL-3.0-or-later
import type { SchedulerStatus } from "../api/schedules";
import type { Locale, MessageKey, MessageParams } from "../i18n";
import { formatRelative, parseTime } from "../screens/Jobs/format";

type Translate = (key: MessageKey, params?: MessageParams) => string;

/** A tick is 30 s apart, so under a minute it is told in seconds ("12 seconds ago"), not as "now". */
function relativeTick(time: number, now: number, locale: Locale): string {
  const diff = time - now;
  if (Math.abs(diff) >= 60_000) return formatRelative(time, now, locale);
  return new Intl.RelativeTimeFormat(locale, { numeric: "auto" }).format(Math.round(diff / 1000), "second");
}

export interface SchedulerLine {
  /** What the status bar and the scheduler notice say. */
  text: string;
  /** Paused: shown in the warning color, with its own icon. */
  paused: boolean;
}

/**
 * One line about the scheduler: "Bot paused", or the last look at the clock and,
 * when it will look again, the next one. A state outside the closed set reads as
 * running (the backend never sends one).
 */
export function schedulerLine(status: { paused: boolean; state: string; lastTick: SchedulerStatus["lastTick"]; nextTick: SchedulerStatus["nextTick"] }, now: number, locale: Locale, t: Translate): SchedulerLine {
  const last = parseTime(status.lastTick);
  const next = parseTime(status.nextTick);
  if (status.paused) {
    const base = t("statusbar.paused");
    return { text: last !== null ? `${base} · ${t("statusbar.lastTick", { when: relativeTick(last, now, locale) })}` : base, paused: true };
  }
  const parts = [last !== null ? t("statusbar.lastTick", { when: relativeTick(last, now, locale) }) : t("statusbar.lastTick.none")];
  if (next !== null) parts.push(t("statusbar.nextTick", { when: relativeTick(next, now, locale) }));
  if (status.state === "outside_window") parts.push(t("statusbar.outsideWindow"));
  return { text: parts.join(" · "), paused: false };
}
