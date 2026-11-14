// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, expect, it } from "vitest";
import { badgeStatus, indexRepos, isActive, localReviewMode, localReviewRange, runErrorCode, runMode, runVerdicts, statusName, taskMode, taskTarget } from "./runView";

describe("status mapping", () => {
  it("spells the backend's canceled the way the badge does", () => {
    expect(badgeStatus("canceled")).toBe("cancelled");
    expect(badgeStatus("attention")).toBe("attention");
    expect(badgeStatus("")).toBe("queued");
    expect(statusName("nonsense")).toBe("queued");
  });

  it("knows which statuses still change", () => {
    expect(isActive("queued")).toBe(true);
    expect(isActive("running")).toBe(true);
    for (const done of ["succeeded", "failed", "canceled", "attention"]) expect(isActive(done)).toBe(false);
  });
});

describe("taskMode and runMode: the mode comes from the task's own record", () => {
  const local = { kind: "review.local", spec: { mode: "staged" } } as never;
  const prPreview = { kind: "review.pr", spec: { number: 3, mode: "preview" } } as never;
  const botPost = { kind: "bot.review_requested", spec: { number: 3, mode: "post", jobId: 1 } } as never;
  const botNoSpec = { kind: "bot.review_requested", spec: { failure: "gh_not_authenticated" } } as never;

  it("a local review is a preview by kind (it cannot write to GitHub), its spec.mode is the diff mode", () => {
    expect(taskMode(local)).toBe("preview");
  });

  it("a pull request task carries the mode it was planned with", () => {
    expect(taskMode(prPreview)).toBe("preview");
    expect(taskMode(botPost)).toBe("post");
  });

  it("never guesses a preview: a spec that does not say, a stranger kind and a missing spec are unknown", () => {
    expect(taskMode(botNoSpec)).toBe("unknown");
    expect(taskMode({ kind: "review.pr", spec: null } as never)).toBe("unknown");
    expect(taskMode({ kind: "review.pr", spec: { mode: "<img>" } } as never)).toBe("unknown");
    expect(taskMode({ kind: "something.new", spec: { mode: "preview" } } as never)).toBe("unknown");
  });

  it("a report with a remote container is a post, whatever the spec says", () => {
    expect(taskMode({ kind: "review.pr", spec: null, report: { meta: {}, remote: { status: "submitted" } } } as never)).toBe("post");
  });

  it("a run is post when any task posts, preview only when every task is a preview, else unknown", () => {
    expect(runMode({ tasks: [local, prPreview] } as never)).toBe("preview");
    expect(runMode({ tasks: [prPreview, botPost] } as never)).toBe("post");
    expect(runMode({ tasks: [prPreview, botNoSpec] } as never)).toBe("unknown");
    expect(runMode({ tasks: [botNoSpec, botPost] } as never)).toBe("post");
    expect(runMode({ tasks: null } as never)).toBe("unknown");
    expect(runMode({ tasks: [] } as never)).toBe("unknown");
  });
});

describe("task helpers", () => {
  const repos = indexRepos([{ id: 1, owner: "acme", name: "api", localPath: "/x" }]);
  it("names the repo, or none when it was deleted", () => {
    expect(taskTarget({ repositoryId: 1, prNumber: 12 }, repos)).toEqual({ slug: "acme/api", pr: 12 });
    expect(taskTarget({ repositoryId: 2, prNumber: 0 }, repos)).toEqual({ slug: null, pr: 0 });
  });

  it("reads the local review mode and range from the task spec", () => {
    expect(localReviewMode({ spec: { mode: "staged", path: "/x" }, prNumber: 0 })).toBe("staged");
    expect(localReviewMode({ spec: { mode: "range", range: ["main..HEAD", 5] }, prNumber: 0 })).toBe("range");
    expect(localReviewRange({ spec: { mode: "range", range: ["main..HEAD", 5] }, prNumber: 0 })).toEqual(["main..HEAD"]);
    expect(localReviewMode({ spec: { mode: "staged" }, prNumber: 3 })).toBe("");
    expect(localReviewMode({ spec: null, prNumber: 0 })).toBe("");
    expect(localReviewMode({ spec: { mode: "<img>" }, prNumber: 0 })).toBe("");
  });
});

describe("run summaries", () => {
  it("takes the run's error code, else the first failed task's", () => {
    expect(runErrorCode({ errorCode: "timeout" as never, tasks: [] })).toBe("timeout");
    expect(runErrorCode({ errorCode: "" as never, tasks: [{ errorCode: "" } as never, { errorCode: "not_a_repo" } as never] })).toBe("not_a_repo");
    expect(runErrorCode({ errorCode: "" as never, tasks: null })).toBe("");
  });

  it("lists each verdict once", () => {
    expect(runVerdicts({ tasks: [{ verdict: "comment" } as never, { verdict: "" } as never, { verdict: "comment" } as never] })).toEqual(["comment"]);
  });
});
