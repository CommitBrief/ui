// SPDX-License-Identifier: GPL-3.0-or-later
import type { BotJobInput, ScheduledJob } from "../../api/schedules";
import type { MessageKey, MessageParams } from "../../i18n";

// Pure helpers behind the schedule form and list: the day mask, the window
// wording, the editable draft and its validation. No Wails import, so they are
// unit-tested without a runtime.

type Translate = (key: MessageKey, params?: MessageParams) => string;

/** Monday is bit 0 and Sunday bit 6, the backend's days_mask encoding. */
export const DAY_BITS = [1, 2, 4, 8, 16, 32, 64] as const;
export const WEEKDAYS = 31;
export const EVERY_DAY = 127;
export const MINUTES_PER_DAY = 1440;

const dayShort = [
  "schedules.day.mon",
  "schedules.day.tue",
  "schedules.day.wed",
  "schedules.day.thu",
  "schedules.day.fri",
  "schedules.day.sat",
  "schedules.day.sun",
] as const satisfies readonly MessageKey[];

const dayLong = [
  "schedules.day.long.mon",
  "schedules.day.long.tue",
  "schedules.day.long.wed",
  "schedules.day.long.thu",
  "schedules.day.long.fri",
  "schedules.day.long.sat",
  "schedules.day.long.sun",
] as const satisfies readonly MessageKey[];

export function dayShortKey(index: number): MessageKey {
  return dayShort[index] ?? dayShort[0];
}

export function dayLongKey(index: number): MessageKey {
  return dayLong[index] ?? dayLong[0];
}

/** "Mon–Fri", "Mon, Wed, Fri", "Every day"; consecutive runs of three or more days collapse to a range. */
export function daysSummary(mask: number, t: Translate): string {
  if ((mask & EVERY_DAY) === EVERY_DAY) return t("schedules.days.every");
  const parts: string[] = [];
  let i = 0;
  while (i < 7) {
    if ((mask & (DAY_BITS[i] ?? 0)) === 0) {
      i++;
      continue;
    }
    let j = i;
    while (j + 1 < 7 && (mask & (DAY_BITS[j + 1] ?? 0)) !== 0) j++;
    if (j - i >= 2) parts.push(`${t(dayShortKey(i))}–${t(dayShortKey(j))}`);
    else for (let k = i; k <= j; k++) parts.push(t(dayShortKey(k)));
    i = j + 1;
  }
  return parts.join(", ");
}

/** "09:00"; the end of the day (1440) is "24:00". */
export function formatMinutes(minutes: number): string {
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  return `${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}`;
}

/** The value for an `<input type="time">`, which has no 24:00: the end of the day shows as 00:00. */
export function toTimeInput(minutes: number): string {
  return formatMinutes(minutes % MINUTES_PER_DAY);
}

/** Reads an `<input type="time">` value. An end of 00:00 means midnight, the end of the day (1440). Null when it is not a time. */
export function parseTimeInput(value: string, isEnd: boolean): number | null {
  const match = /^(\d{1,2}):(\d{2})$/.exec(value.trim());
  if (!match) return null;
  const h = Number(match[1]);
  const m = Number(match[2]);
  if (h > 23 || m > 59) return null;
  const minutes = h * 60 + m;
  return isEnd && minutes === 0 ? MINUTES_PER_DAY : minutes;
}

/** "every 15 min", "every 2 h"; a whole number of hours reads as hours. */
export function everySummary(every: number, t: Translate): string {
  if (every >= 60 && every % 60 === 0) return t("schedules.every.hours", { count: every / 60 });
  return t("schedules.every.minutes", { count: every });
}

export interface WindowLike {
  days: number;
  start: number;
  end: number;
  every: number;
}

/** "Mon–Fri 09:00–18:00, every 15 min" (Turkish: "Pzt–Cum 09:00–18:00, 15 dk'da bir"). */
export function windowSummary(w: WindowLike, t: Translate): string {
  return t("schedules.window.summary", {
    days: daysSummary(w.days, t),
    start: formatMinutes(w.start),
    end: formatMinutes(w.end),
    every: everySummary(w.every, t),
  });
}

/** The IANA zone of this machine, "UTC" when the webview does not say. */
export function systemTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}

/** Whether the runtime knows the zone name. The backend decides for good; this only catches typos early. */
export function isValidTimeZone(name: string): boolean {
  const trimmed = name.trim();
  if (trimmed === "" || trimmed.length > 64 || trimmed === "Local") return false;
  try {
    new Intl.DateTimeFormat("en", { timeZone: trimmed });
    return true;
  } catch {
    return false;
  }
}

// ---- the editable draft ----

export type ScheduleMode = "preview" | "post";

export interface WindowDraft {
  /** Stable React key; not sent anywhere. */
  key: number;
  days: number;
  start: string;
  end: string;
  every: string;
  tz: string;
}

export interface ScheduleDraft {
  name: string;
  /** Repository ids the bot watches. */
  targets: number[];
  windows: WindowDraft[];
  /** The default: draft pull requests are skipped. */
  skipDrafts: boolean;
  enabled: boolean;
  mode: ScheduleMode;
  /** The user accepted the post consent text in this session. */
  consented: boolean;
}

let windowKey = 0;

export function newWindowDraft(tz: string): WindowDraft {
  windowKey += 1;
  return { key: windowKey, days: WEEKDAYS, start: "09:00", end: "18:00", every: "15", tz };
}

/** A new job: preview, skipping drafts, Mon–Fri 09:00–18:00 every 15 minutes in the system zone. */
export function emptyDraft(tz: string): ScheduleDraft {
  return { name: "", targets: [], windows: [newWindowDraft(tz)], skipDrafts: true, enabled: true, mode: "preview", consented: false };
}

export function toDraft(job: ScheduledJob, tz: string): ScheduleDraft {
  const windows = (job.windows ?? []).map((w) => {
    windowKey += 1;
    return { key: windowKey, days: w.days, start: toTimeInput(w.start), end: toTimeInput(w.end), every: String(w.every), tz: w.tz };
  });
  return {
    name: job.name,
    targets: [...(job.targets ?? [])],
    windows: windows.length > 0 ? windows : [newWindowDraft(tz)],
    skipDrafts: !job.includeDrafts,
    enabled: job.enabled,
    mode: job.mode === "post" ? "post" : "preview",
    consented: false,
  };
}

/** Whether the stored job already carries the user's post consent (it is kept as long as the job stays a post job). */
export function consentRecorded(job: Pick<ScheduledJob, "mode" | "postConsentAt"> | null): boolean {
  return job !== null && job.mode === "post" && job.postConsentAt !== null && job.postConsentAt !== undefined;
}

// ---- validation ----

export interface WindowIssues {
  days?: MessageKey;
  start?: MessageKey;
  end?: MessageKey;
  every?: MessageKey;
  tz?: MessageKey;
}

export interface DraftIssues {
  name?: MessageKey;
  targets?: MessageKey;
  windows: WindowIssues[];
}

export function windowIssues(w: WindowDraft): WindowIssues {
  const issues: WindowIssues = {};
  if (w.days === 0) issues.days = "schedules.form.days.required";
  const start = parseTimeInput(w.start, false);
  const end = parseTimeInput(w.end, true);
  if (start === null) issues.start = "schedules.form.time.invalid";
  if (end === null) issues.end = "schedules.form.time.invalid";
  else if (start !== null && end <= start) issues.end = "schedules.form.end.afterStart";
  const every = Number(w.every.trim());
  if (w.every.trim() === "" || !Number.isInteger(every) || every < 1 || every > MINUTES_PER_DAY) issues.every = "schedules.form.every.invalid";
  if (!isValidTimeZone(w.tz)) issues.tz = "schedules.form.tz.invalid";
  return issues;
}

export function validateDraft(draft: ScheduleDraft, maxName: number): DraftIssues {
  const issues: DraftIssues = { windows: draft.windows.map(windowIssues) };
  const name = draft.name.trim();
  if (name === "") issues.name = "schedules.form.name.required";
  else if ([...name].length > maxName) issues.name = "schedules.form.name.tooLong";
  if (draft.targets.length === 0) issues.targets = "schedules.form.targets.required";
  return issues;
}

export function hasIssues(issues: DraftIssues): boolean {
  return issues.name !== undefined || issues.targets !== undefined || issues.windows.some((w) => Object.keys(w).length > 0);
}

// ---- what stops a save, regardless of the fields ----

export interface SaveContext {
  /** The stored job already has the user's post consent. */
  consentRecorded: boolean;
  /** The installed CLI can post (CLIStatus.capabilities.posting). */
  posting: boolean;
}

/**
 * Why the Save button is disabled, or null. Post needs the consent text accepted
 * (once per job: an earlier recorded consent stands) and a CLI that can post.
 */
export function saveBlocker(draft: ScheduleDraft, ctx: SaveContext): "cli" | "consent" | null {
  if (draft.mode !== "post") return null;
  if (!ctx.posting) return "cli";
  return ctx.consentRecorded || draft.consented ? null : "consent";
}

/** The call input for a validated draft. `windows` are minutes from local midnight. */
export function toInput(draft: ScheduleDraft, id: number): BotJobInput {
  return {
    id,
    name: draft.name.trim(),
    mode: draft.mode as BotJobInput["mode"],
    enabled: draft.enabled,
    targets: draft.targets,
    includeDrafts: !draft.skipDrafts,
    postConsent: draft.mode === "post" && draft.consented,
    windows: draft.windows.map((w) => ({
      days: w.days,
      start: parseTimeInput(w.start, false) ?? 0,
      end: parseTimeInput(w.end, true) ?? MINUTES_PER_DAY,
      every: Number(w.every.trim()),
      tz: w.tz.trim(),
    })),
  };
}
