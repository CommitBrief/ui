// SPDX-License-Identifier: GPL-3.0-or-later
import type { Run, Task } from "../../api/jobs";
import type { RunStatus } from "../../components";
import { parseReport } from "./findings";

// Pure helpers that turn the backend's run and task records into what the screen
// shows. No Wails import, so they are unit-tested without a runtime.

/** The backend's status names (note the spelling: `canceled`; the badge says `cancelled`). */
export type StatusName = "queued" | "running" | "succeeded" | "failed" | "canceled" | "attention";

export function statusName(status: string): StatusName {
  switch (status) {
    case "running":
    case "succeeded":
    case "failed":
    case "canceled":
    case "attention":
      return status;
    default:
      return "queued";
  }
}

/** The badge component spells canceled with two l. */
export function badgeStatus(status: string): RunStatus {
  const name = statusName(status);
  return name === "canceled" ? "cancelled" : name;
}

/** A queued or running task or run can still be canceled and will still change. */
export function isActive(status: string): boolean {
  const name = statusName(status);
  return name === "queued" || name === "running";
}

/** What a run did on GitHub: `unknown` when the record does not say (never guessed as preview). */
export type JobMode = "preview" | "post" | "unknown";

/** The slice of a repository the screen needs to word a task. */
export interface RepoRef {
  id: number;
  owner: string;
  name: string;
  localPath: string;
}

export type RepoIndex = ReadonlyMap<number, RepoRef>;

export function indexRepos(repos: readonly RepoRef[]): RepoIndex {
  return new Map(repos.map((repo) => [repo.id, repo]));
}

/** What a task reviewed: the repository slug (null when it was deleted) and the pull request number (0 for a local review). */
export function taskTarget(task: Pick<Task, "repositoryId" | "prNumber">, repos: RepoIndex): { slug: string | null; pr: number } {
  const repo = repos.get(task.repositoryId);
  return { slug: repo ? `${repo.owner}/${repo.name}` : null, pr: task.prNumber };
}

type LocalMode = "staged" | "unstaged" | "range";

/** The local review mode a task was planned with, read from its spec; "" for a pull request or an unreadable spec. */
export function localReviewMode(task: Pick<Task, "spec" | "prNumber">): LocalMode | "" {
  if (task.prNumber > 0) return "";
  const spec: unknown = task.spec;
  if (typeof spec !== "object" || spec === null) return "";
  const mode = (spec as { mode?: unknown }).mode;
  return mode === "staged" || mode === "unstaged" || mode === "range" ? mode : "";
}

/** The revision arguments of a ranged local review, e.g. ["main..HEAD"]; empty for anything else. */
export function localReviewRange(task: Pick<Task, "spec" | "prNumber">): string[] {
  if (localReviewMode(task) !== "range") return [];
  const range = (task.spec as { range?: unknown }).range;
  return Array.isArray(range) ? range.filter((part): part is string => typeof part === "string") : [];
}

/**
 * Preview or post of one task (UI-0008 section 5), read from the task's own
 * record, never from the job as it is now (a job's mode can be changed later,
 * and the job can be gone):
 * - a local review has no way to write to GitHub: it is a preview by kind;
 * - a pull request task (manual preview, or the bot's) carries the mode it was
 *   planned with in `spec.mode`;
 * - anything else, or a spec that does not say, is `unknown`.
 * A stored report with a `remote` container is what a posting run writes, so it
 * wins when the task has one (a listing carries no reports; a detail does).
 */
export function taskMode(task: Pick<Task, "kind" | "spec"> & { report?: unknown }): JobMode {
  if (parseReport(task.report)?.remote) return "post";
  const kind: string = task.kind;
  if (kind === "review.local") return "preview";
  if (kind === "review.pr" || kind === "bot.review_requested") {
    const spec: unknown = task.spec;
    const mode = typeof spec === "object" && spec !== null ? (spec as { mode?: unknown }).mode : undefined;
    return mode === "preview" || mode === "post" ? mode : "unknown";
  }
  return "unknown";
}

/**
 * The mode of a run: post as soon as one task posts, preview only when every
 * task is a preview, otherwise unknown. A run without tasks is unknown.
 */
export function runMode(run: Pick<Run, "tasks">): JobMode {
  const tasks = run.tasks ?? [];
  if (tasks.length === 0) return "unknown";
  const modes = tasks.map(taskMode);
  if (modes.includes("post")) return "post";
  return modes.every((mode) => mode === "preview") ? "preview" : "unknown";
}

/** Sum of the stored findings of every task of a run. */
export function runFindingCount(run: Pick<Run, "tasks">): number {
  return (run.tasks ?? []).reduce((sum, task) => sum + task.findingCount, 0);
}

/** The error code of a run that did not succeed: its own, else the first failed task's. */
export function runErrorCode(run: Pick<Run, "errorCode" | "tasks">): string {
  if (run.errorCode !== "") return run.errorCode;
  return (run.tasks ?? []).find((task) => task.errorCode !== "")?.errorCode ?? "";
}

/** The verdicts of a run's tasks that have one, without repeats. */
export function runVerdicts(run: Pick<Run, "tasks">): string[] {
  return [...new Set((run.tasks ?? []).map((task) => task.verdict).filter((verdict) => verdict !== ""))];
}
