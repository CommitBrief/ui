// SPDX-License-Identifier: GPL-3.0-or-later
import { Events } from "@wailsio/runtime";
import { SchedulesService } from "../../bindings/github.com/CommitBrief/ui/internal/adapter/desktop";
import type { BotJobInput, BudgetStatus, ScheduledJob, SchedulerStatus, TrackedPRView } from "../../bindings/github.com/CommitBrief/ui/internal/app/models.js";
import { Mode } from "../../bindings/github.com/CommitBrief/ui/internal/domain/job/models.js";
import { Days } from "../../bindings/github.com/CommitBrief/ui/internal/domain/schedule/models.js";
import type { Window } from "../../bindings/github.com/CommitBrief/ui/internal/domain/schedule/models.js";
import type { Run } from "../../bindings/github.com/CommitBrief/ui/internal/domain/run/models.js";
import { toScheduleError } from "./schedulesErrors";

// The Schedules screen's only door to the backend. A failed call is rethrown as
// a ScheduleError ({code, field}); the backend never sends text.

export { ScheduleError, describeScheduleError, errorTarget, scheduleErrorKey, toScheduleError } from "./schedulesErrors";
export type { ErrorTarget } from "./schedulesErrors";
export type { BotJobInput, BudgetStatus, Run, ScheduledJob, SchedulerStatus, TrackedPRView, Window };
export { Days, Mode };

/** The most windows one job may have (internal/app MaxWindows). */
export const MAX_WINDOWS = 8;
/** The longest job name (internal/domain/job MaxNameLen). */
export const MAX_NAME_LENGTH = 200;

async function call<T>(promise: Promise<T>): Promise<T> {
  try {
    return await promise;
  } catch (error) {
    throw toScheduleError(error);
  }
}

const CHANGED_EVENT = "commitbrief-ui:schedules-changed";

/** A write that succeeded: the status bar (a different part of the window) reads again instead of waiting for its timer. */
async function write<T>(promise: Promise<T>): Promise<T> {
  const result = await call(promise);
  window.dispatchEvent(new Event(CHANGED_EVENT));
  return result;
}

/** Subscribes to "a schedule was saved, switched, resumed, run or deleted". Returns the unsubscribe function. */
export function onSchedulesChanged(callback: () => void): () => void {
  window.addEventListener(CHANGED_EVENT, callback);
  return () => window.removeEventListener(CHANGED_EVENT, callback);
}

export async function listBotJobs(): Promise<ScheduledJob[]> {
  return (await call(SchedulesService.ListBotJobs())) ?? [];
}

/** Creates (id 0) or replaces a bot job. A post job is saved only with `postConsent` (or an earlier recorded consent). */
export function saveBotJob(input: BotJobInput): Promise<ScheduledJob> {
  return write(SchedulesService.SaveBotJob(input));
}

export function setBotJobEnabled(id: number, enabled: boolean): Promise<ScheduledJob> {
  return write(SchedulesService.SetBotJobEnabled(id, enabled));
}

export function resumeBotJob(id: number): Promise<ScheduledJob> {
  return write(SchedulesService.ResumeBotJob(id));
}

export function deleteBotJob(id: number): Promise<void> {
  return write(SchedulesService.DeleteBotJob(id));
}

export function runBotJobNow(id: number): Promise<Run> {
  return write(SchedulesService.RunBotJobNow(id));
}

export function budgetStatus(): Promise<BudgetStatus> {
  return call(SchedulesService.BudgetStatus());
}

/** Paused or running, the last and the next look at the clock. */
export function schedulerStatus(): Promise<SchedulerStatus> {
  return call(SchedulesService.SchedulerStatus());
}

/** Stops the scheduler from starting runs on its own, until it is resumed; the pause survives a restart. Manual runs are not affected. */
export function pauseScheduler(): Promise<SchedulerStatus> {
  return call(SchedulesService.PauseScheduler());
}

export function resumeScheduler(): Promise<SchedulerStatus> {
  return call(SchedulesService.ResumeScheduler());
}

/** The open pull requests the bot follows, one row per schedule. */
export async function trackedPRs(): Promise<TrackedPRView[]> {
  return (await call(SchedulesService.TrackedPRs())) ?? [];
}

/** Subscribes to pause and resume, from this screen or from the tray. Returns the unsubscribe function. */
export function onSchedulerStatus(callback: (status: SchedulerStatus) => void): () => void {
  return Events.On("scheduler:status", (event) => callback(event.data as SchedulerStatus));
}

export function autostartEnabled(): Promise<boolean> {
  return call(SchedulesService.AutostartEnabled());
}

export function setAutostart(enabled: boolean): Promise<void> {
  return call(SchedulesService.SetAutostart(enabled));
}

/** Subscribes to run status changes (the scheduler's runs included). Returns the unsubscribe function. */
export function onRunChanged(callback: () => void): () => void {
  return Events.On("run:status", () => callback());
}
