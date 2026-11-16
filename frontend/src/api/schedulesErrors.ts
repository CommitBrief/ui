// SPDX-License-Identifier: GPL-3.0-or-later
import type { MessageKey, MessageParams } from "../i18n";
import { jobErrorCodes, jobErrorKey } from "./jobsErrors";

// Pure (no Wails import). The scheduled-bot service answers with the same
// {code, field} shape as the jobs service, from the same closed set plus three
// codes of its own (internal/domain/run): a time window that is not acceptable
// and the two login-item failures. The backend sends no text.

const ownCodes = ["schedule_invalid", "autostart_failed", "autostart_unsupported"] as const;
export const scheduleErrorCodes: readonly string[] = [...jobErrorCodes, ...ownCodes];

/** A classified schedules failure; `code` is a member of the backend's closed set (or "internal"). */
export class ScheduleError extends Error {
  readonly code: string;
  /** What the error is about, e.g. "windows[1].end", "targets" or "name". Empty when it is about the call. */
  readonly field: string;

  constructor(code: string, field = "") {
    super(code);
    this.name = "ScheduleError";
    this.code = code;
    this.field = field;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

export function isScheduleErrorCode(code: string): boolean {
  return scheduleErrorCodes.includes(code);
}

/**
 * Reads {code, field} from a rejected binding call (Wails puts the marshalled
 * error in `cause`). Anything unrecognised becomes "internal", so no unknown
 * text reaches the UI.
 */
export function toScheduleError(error: unknown): ScheduleError {
  if (error instanceof ScheduleError) return error;
  const cause = isRecord(error) ? error.cause : undefined;
  if (isRecord(cause) && typeof cause.code === "string" && isScheduleErrorCode(cause.code)) {
    return new ScheduleError(cause.code, typeof cause.field === "string" ? cause.field : "");
  }
  return new ScheduleError("internal");
}

/** The i18n key of an error sentence; an unknown code gets the generic one. */
export function scheduleErrorKey(code: string): MessageKey {
  if ((ownCodes as readonly string[]).includes(code)) return `schedules.error.${code as (typeof ownCodes)[number]}`;
  return jobErrorKey(code);
}

export type Translate = (key: MessageKey, params?: MessageParams) => string;

export function describeScheduleError(error: Pick<ScheduleError, "code">, t: Translate): string {
  return t(scheduleErrorKey(error.code));
}

/** A field a backend error points at: the job-level one, or a window (index) and its field. */
export type ErrorTarget = { kind: "name" } | { kind: "targets" } | { kind: "window"; index: number; field: string } | { kind: "windows" };

/** Where a rejected save should be shown, read from the error's field; null means "the form as a whole". */
export function errorTarget(error: Pick<ScheduleError, "code" | "field">): ErrorTarget | null {
  if (error.code === "schedule_invalid") {
    const match = /^windows\[(\d+)\]\.(\w+)$/.exec(error.field);
    if (match) return { kind: "window", index: Number(match[1]), field: match[2] as string };
    return error.field === "windows" ? { kind: "windows" } : null;
  }
  if (error.code !== "job_invalid") return null;
  if (error.field === "name") return { kind: "name" };
  if (error.field === "targets") return { kind: "targets" };
  return null;
}
