// SPDX-License-Identifier: GPL-3.0-or-later
import type { Run } from "../../api/schedules";
import { parseTime } from "../Jobs/format";
import { statusName } from "../Jobs/runView";

// What the Schedules screen says about a bot job's health, read from the run
// history. Pure and tested.

export type HealthState = "attention" | "paused" | "failing" | "healthy" | "idle";

export interface Health {
  state: HealthState;
  /** Failed or attention runs in a row, newest first, until the first success. */
  failStreak: number;
  /** When the newest successful run ended (ms); null when there is none in the history read. */
  lastSuccessAt: number | null;
  /** When the newest finished or running run started (ms); null when the job has no run in the history read. */
  lastRunAt: number | null;
}

/** The runs of one job, newest first (the order `listRuns` gives). */
export function runsOfJob(runs: readonly Run[], jobId: number): Run[] {
  return runs.filter((run) => run.jobId === jobId);
}

/**
 * Health of a job:
 * - `attention`: the scheduler stopped it until the user acts (its attention code);
 * - `paused`: switched off;
 * - `failing`: the newest runs that finished did not succeed;
 * - `healthy`: the newest finished run succeeded;
 * - `idle`: no finished run in the history read.
 * A run that is queued, running or canceled neither breaks nor extends a streak.
 */
export function jobHealth(job: { id: number; enabled: boolean; attentionCode: string }, runs: readonly Run[]): Health {
  const own = runsOfJob(runs, job.id);
  let failStreak = 0;
  let streakOpen = true;
  let lastSuccessAt: number | null = null;
  let newest: "succeeded" | "bad" | null = null;
  for (const run of own) {
    const status = statusName(run.status);
    if (status === "succeeded") {
      newest ??= "succeeded";
      streakOpen = false;
      lastSuccessAt ??= parseTime(run.finishedAt) ?? parseTime(run.startedAt);
    } else if (status === "failed" || status === "attention") {
      newest ??= "bad";
      if (streakOpen) failStreak++;
    }
  }
  const lastRunAt = own.length > 0 ? parseTime(own[0]?.startedAt) : null;

  let state: HealthState;
  if (job.attentionCode !== "") state = "attention";
  else if (!job.enabled) state = "paused";
  else if (newest === "bad") state = "failing";
  else if (newest === "succeeded") state = "healthy";
  else state = "idle";
  return { state, failStreak, lastSuccessAt, lastRunAt };
}
