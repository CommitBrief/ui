// SPDX-License-Identifier: GPL-3.0-or-later
import type { Locale } from "../../i18n";

// Plain formatting helpers for times, durations and money; pure and tested.

/** A Go zero time ("0001-01-01...") or a bad value is "no time". */
export function parseTime(value: string | null | undefined): number | null {
  if (!value) return null;
  const ms = Date.parse(value);
  if (Number.isNaN(ms) || new Date(ms).getUTCFullYear() < 2000) return null;
  return ms;
}

const durationUnits: Record<Locale, { s: string; min: string; h: string }> = {
  en: { s: "s", min: "min", h: "h" },
  tr: { s: "sn", min: "dk", h: "sa" },
};

/** "42 s", "3 min 05 s", "2 h 07 min" (Turkish: sn, dk, sa). Under a second reads "<1 s". */
export function formatDuration(ms: number, locale: Locale = "en"): string {
  if (!Number.isFinite(ms) || ms < 0) return "";
  const unit = durationUnits[locale];
  const total = Math.floor(ms / 1000);
  if (total < 1) return `<1 ${unit.s}`;
  if (total < 60) return `${total} ${unit.s}`;
  const minutes = Math.floor(total / 60);
  const seconds = total % 60;
  if (minutes < 60) return `${minutes} ${unit.min} ${String(seconds).padStart(2, "0")} ${unit.s}`;
  const hours = Math.floor(minutes / 60);
  return `${hours} ${unit.h} ${String(minutes % 60).padStart(2, "0")} ${unit.min}`;
}

/** How long a run or task took; a running one counts up to `now`. Empty when it has not started. */
export function elapsed(startedAt: string | null | undefined, finishedAt: string | null | undefined, now: number): number | null {
  const start = parseTime(startedAt);
  if (start === null) return null;
  const end = parseTime(finishedAt) ?? now;
  return Math.max(0, end - start);
}

/** "$0.0123" below a cent, "$1.23" above. Zero is "$0.00". */
export function formatCost(usd: number): string {
  if (!Number.isFinite(usd) || usd <= 0) return "$0.00";
  if (usd < 0.01) return `$${usd.toFixed(4)}`;
  return `$${usd.toFixed(2)}`;
}

const units: readonly [Intl.RelativeTimeFormatUnit, number][] = [
  ["day", 86_400_000],
  ["hour", 3_600_000],
  ["minute", 60_000],
];

/** "5 minutes ago", "yesterday", "just now"; the language follows the interface. */
export function formatRelative(time: number, now: number, locale: Locale): string {
  const diff = time - now;
  const formatter = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
  for (const [unit, size] of units) {
    if (Math.abs(diff) >= size) return formatter.format(Math.trunc(diff / size), unit);
  }
  return formatter.format(0, "second");
}

/** The full local date and time, for a tooltip and for the detail view. */
export function formatDateTime(time: number, locale: Locale): string {
  return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "medium" }).format(time);
}

/** The first 7 characters of a commit id. */
export function shortOid(oid: string): string {
  return oid.slice(0, 7);
}
