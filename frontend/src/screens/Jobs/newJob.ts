// SPDX-License-Identifier: GPL-3.0-or-later
import type { MessageKey } from "../../i18n";

// Validation of the "New job" form, mirroring the backend (internal/domain/job
// spec.go): the backend re-checks everything, this only tells the user sooner.

const MAX_RANGE_ARGS = 4;
const MAX_REVISION_LENGTH = 256;
/** The shape of one `git diff` revision argument; the first character is never `-`, so it cannot be read as an option. */
const revision = /^[A-Za-z0-9_][A-Za-z0-9._/@^~:{}+-]*$/;
/** Pull request numbers are positive 32-bit integers (job.MaxPRNumber). */
export const MAX_PR_NUMBER = 2 ** 31 - 1;

export type RangeParse = { ok: true; args: string[] } | { ok: false; error: MessageKey };

/** Splits the range text on whitespace into `git diff` revision arguments. */
export function parseRange(text: string): RangeParse {
  const args = text.split(/\s+/).filter((part) => part !== "");
  if (args.length === 0) return { ok: false, error: "jobs.new.range.required" };
  if (args.length > MAX_RANGE_ARGS) return { ok: false, error: "jobs.new.range.tooMany" };
  if (args.some((arg) => arg.length > MAX_REVISION_LENGTH || !revision.test(arg))) {
    return { ok: false, error: "jobs.new.range.invalid" };
  }
  return { ok: true, args };
}

export type NumberParse = { ok: true; value: number } | { ok: false; error: MessageKey };

export function parsePRNumber(text: string): NumberParse {
  const trimmed = text.trim().replace(/^#/, "");
  if (trimmed === "") return { ok: false, error: "jobs.new.pr.required" };
  if (!/^\d+$/.test(trimmed)) return { ok: false, error: "jobs.new.pr.invalid" };
  const value = Number(trimmed);
  if (value < 1 || value > MAX_PR_NUMBER) return { ok: false, error: "jobs.new.pr.invalid" };
  return { ok: true, value };
}
