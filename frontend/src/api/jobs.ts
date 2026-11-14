// SPDX-License-Identifier: GPL-3.0-or-later
import { Events } from "@wailsio/runtime";
import { JobsService } from "../../bindings/github.com/CommitBrief/ui/internal/adapter/desktop";
import type {
  LocalReviewInput,
  PRPreviewInput,
  RunStatusEvent,
  TaskLogEvent,
  TaskLogView,
  TaskStatusEvent,
} from "../../bindings/github.com/CommitBrief/ui/internal/app/models.js";
import type { Run, Task } from "../../bindings/github.com/CommitBrief/ui/internal/domain/run/models.js";
import { toJobError } from "./jobsErrors";

// The Jobs screen's only door to the backend. A failed call is rethrown as a
// JobError ({code, field}); the backend never sends text. Live progress comes
// as the events task:log, task:status and run:status, not as call results.

export { JobError, describeJobError, jobErrorKey, nextStepFor, toJobError } from "./jobsErrors";
export type { NextStep } from "./jobsErrors";
export type { LocalReviewInput, PRPreviewInput, Run, RunStatusEvent, Task, TaskLogEvent, TaskLogView, TaskStatusEvent };

async function call<T>(promise: Promise<T>): Promise<T> {
  try {
    return await promise;
  } catch (error) {
    throw toJobError(error);
  }
}

/** Newest first, each run with its tasks but without findings and report. */
export async function listRuns(limit = 100): Promise<Run[]> {
  return (await call(JobsService.ListRuns(limit))) ?? [];
}

/** One run with its tasks, findings and report. */
export function getRun(id: number): Promise<Run> {
  return call(JobsService.GetRun(id));
}

export function startLocalReview(input: LocalReviewInput): Promise<Run> {
  return call(JobsService.StartLocalReview(input));
}

/** A preview: the review runs with --no-post and nothing is written to GitHub. */
export function startPRPreview(input: PRPreviewInput): Promise<Run> {
  return call(JobsService.StartPRPreview(input));
}

export function cancelRun(id: number): Promise<void> {
  return call(JobsService.CancelRun(id));
}

/**
 * The masked stderr tail of a task. A task that has no log (not started yet, or
 * pruned by the retention policy) is not an error: it resolves with null.
 */
export async function getTaskLog(taskId: number): Promise<TaskLogView | null> {
  try {
    return await JobsService.TaskLog(taskId);
  } catch (error) {
    const failure = toJobError(error);
    if (failure.code === "task_log_unavailable") return null;
    throw failure;
  }
}

/** Subscribes to log lines (already masked by the backend). Returns the unsubscribe function. */
export function onTaskLog(callback: (event: TaskLogEvent) => void): () => void {
  return Events.On("task:log", (event) => callback(event.data));
}

export function onTaskStatus(callback: (event: TaskStatusEvent) => void): () => void {
  return Events.On("task:status", (event) => callback(event.data));
}

export function onRunStatus(callback: (event: RunStatusEvent) => void): () => void {
  return Events.On("run:status", (event) => callback(event.data));
}
