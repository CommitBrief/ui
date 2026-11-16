// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, expect, it } from "vitest";
import type { Run } from "../../api/schedules";
import { jobHealth } from "./health";

let nextId = 1;

interface TaskOverrides {
  prNumber?: number;
  headOid?: string;
  status?: string;
  errorCode?: string;
  repositoryId?: number;
}

function run(jobId: number, status: string, at: string, tasks: TaskOverrides[] = []): Run {
  const id = nextId++;
  return {
    id,
    jobId,
    trigger: "schedule",
    status,
    errorCode: "",
    costUsd: 0,
    startedAt: at,
    finishedAt: at,
    tasks: tasks.map((task, index) => ({
      id: id * 10 + index,
      runId: id,
      kind: "bot.review_requested",
      repositoryId: 1,
      prNumber: 0,
      spec: { mode: "preview" },
      status: "succeeded",
      errorCode: "",
      exitCode: 0,
      cliVersion: "1.22.0",
      headOid: "",
      verdict: "",
      findingCount: 0,
      hasLog: false,
      costUsd: 0,
      queuedAt: at,
      startedAt: at,
      finishedAt: at,
      ...task,
    })),
  } as unknown as Run;
}

const job = { id: 1, enabled: true, attentionCode: "" };

describe("jobHealth", () => {
  it("is healthy when the newest finished run succeeded, and remembers when", () => {
    const h = jobHealth(job, [run(1, "succeeded", "2026-11-14T10:15:00Z"), run(1, "failed", "2026-11-14T10:00:00Z")]);
    expect(h.state).toBe("healthy");
    expect(h.failStreak).toBe(0);
    expect(h.lastSuccessAt).toBe(Date.parse("2026-11-14T10:15:00Z"));
  });

  it("counts the failures in a row up to the first success, and ignores queued, running and canceled runs", () => {
    const h = jobHealth(job, [
      run(1, "running", "2026-11-14T10:45:00Z"),
      run(1, "failed", "2026-11-14T10:30:00Z"),
      run(1, "canceled", "2026-11-14T10:20:00Z"),
      run(1, "attention", "2026-11-14T10:15:00Z"),
      run(1, "succeeded", "2026-11-14T10:00:00Z"),
      run(1, "failed", "2026-11-14T09:45:00Z"),
    ]);
    expect(h.state).toBe("failing");
    expect(h.failStreak).toBe(2);
    expect(h.lastSuccessAt).toBe(Date.parse("2026-11-14T10:00:00Z"));
  });

  it("reads only the runs of its own job", () => {
    const h = jobHealth(job, [run(2, "failed", "2026-11-14T10:00:00Z")]);
    expect(h.state).toBe("idle");
    expect(h.lastSuccessAt).toBeNull();
  });

  it("puts attention before paused, and paused before the run history", () => {
    const failing = [run(1, "failed", "2026-11-14T10:00:00Z")];
    expect(jobHealth({ ...job, attentionCode: "gh_not_authenticated", enabled: false }, failing).state).toBe("attention");
    expect(jobHealth({ ...job, enabled: false }, failing).state).toBe("paused");
  });
});
