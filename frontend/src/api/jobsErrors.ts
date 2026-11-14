// SPDX-License-Identifier: GPL-3.0-or-later
import type { MessageKey, MessageParams } from "../i18n";

// Pure (no Wails import) so it can be unit-tested without a runtime. The
// backend sends no text. A failed call is {code, field?} with `code` from
// internal/domain/run (Code); a failed task carries an internal/domain/errcode
// Code on the task itself. Both closed sets are worded here, in the user's
// language, with the next step the user can take.

export const jobErrorCodes = [
  // internal/domain/run
  "job_invalid",
  "job_not_found",
  "job_kind_unknown",
  "run_repository_not_found",
  "run_local_path_missing",
  "run_queue_full",
  "run_not_found",
  "task_not_found",
  "task_log_unavailable",
  "run_shutting_down",
  "canceled",
  "internal",
  // internal/domain/errcode
  "cli_not_found",
  "cli_too_old",
  "gh_not_found",
  "gh_not_authenticated",
  "not_a_repo",
  "provider_error",
  "provider_not_api",
  "self_pr",
  "too_volatile",
  "secret_detected",
  "cost_aborted",
  "timeout",
  "degraded",
  "capability_missing",
  "unknown",
] as const;
export type JobErrorCode = (typeof jobErrorCodes)[number];

/** A classified jobs failure; `code` is a member of the backend's closed set (or "internal"). */
export class JobError extends Error {
  readonly code: string;
  /** What the error is about, e.g. "spec.range" or "targets". Empty when it is about the call. */
  readonly field: string;

  constructor(code: string, field = "") {
    super(code);
    this.name = "JobError";
    this.code = code;
    this.field = field;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

export function isJobErrorCode(code: string): code is JobErrorCode {
  return (jobErrorCodes as readonly string[]).includes(code);
}

/**
 * Reads {code, field} from a rejected binding call (Wails puts the marshalled
 * error in `cause`). Anything unrecognised, including a code that is not in the
 * closed set, becomes "internal", so no unknown text reaches the UI.
 */
export function toJobError(error: unknown): JobError {
  if (error instanceof JobError) return error;
  const cause = isRecord(error) ? error.cause : undefined;
  if (isRecord(cause) && typeof cause.code === "string" && isJobErrorCode(cause.code)) {
    return new JobError(cause.code, typeof cause.field === "string" ? cause.field : "");
  }
  return new JobError("internal");
}

/** The i18n key of an error sentence; an unknown code gets the generic one. */
export function jobErrorKey(code: string): MessageKey {
  return `jobs.error.${isJobErrorCode(code) ? code : "internal"}`;
}

export type Translate = (key: MessageKey, params?: MessageParams) => string;

export function describeJobError(error: Pick<JobError, "code">, t: Translate): string {
  return t(jobErrorKey(error.code));
}

/** Where a "fix it" link points. Both are screens of this app (hash routes). */
export type NextLink = { href: "#/settings" | "#/repos"; label: MessageKey };

/** What the user can do about a failure: a sentence, a link to the screen that fixes it, a command to run. */
export interface NextStep {
  hint: MessageKey | null;
  link: NextLink | null;
  /** A shell command the user runs themselves; the app never runs it. */
  command: string | null;
}

const toSettings = (label: MessageKey): NextLink => ({ href: "#/settings", label });
const toRepos = (label: MessageKey): NextLink => ({ href: "#/repos", label });

const nextSteps: Partial<Record<string, NextStep>> = {
  cli_not_found: { hint: "jobs.next.cli_not_found", link: toSettings("jobs.next.link.settings"), command: null },
  cli_too_old: { hint: "jobs.next.cli_too_old", link: toSettings("jobs.next.link.settings"), command: null },
  capability_missing: { hint: "jobs.next.capability_missing", link: toSettings("jobs.next.link.settings"), command: null },
  gh_not_found: { hint: "jobs.next.gh_not_found", link: null, command: null },
  gh_not_authenticated: { hint: "jobs.next.gh_not_authenticated", link: toSettings("jobs.next.link.settings"), command: "gh auth login" },
  not_a_repo: { hint: "jobs.next.not_a_repo", link: toRepos("jobs.next.link.repos"), command: null },
  run_local_path_missing: { hint: "jobs.next.run_local_path_missing", link: toRepos("jobs.next.link.repos"), command: null },
  run_repository_not_found: { hint: "jobs.next.run_repository_not_found", link: toRepos("jobs.next.link.repos"), command: null },
  provider_not_api: { hint: "jobs.next.provider_not_api", link: toSettings("jobs.next.link.settings"), command: null },
  provider_error: { hint: "jobs.next.provider_error", link: toSettings("jobs.next.link.settings"), command: null },
  too_volatile: { hint: "jobs.next.too_volatile", link: null, command: null },
  timeout: { hint: "jobs.next.timeout", link: null, command: null },
  secret_detected: { hint: "jobs.next.secret_detected", link: null, command: null },
  cost_aborted: { hint: "jobs.next.cost_aborted", link: toSettings("jobs.next.link.settings"), command: null },
  degraded: { hint: "jobs.next.degraded", link: null, command: null },
  unknown: { hint: "jobs.next.unknown", link: null, command: null },
};

/** The next step for an error code, or null when there is nothing the user can do. */
export function nextStepFor(code: string): NextStep | null {
  return nextSteps[code] ?? null;
}
