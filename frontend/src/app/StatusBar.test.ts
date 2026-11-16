// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, expect, it } from "vitest";
import { summarize } from "./StatusBar";

describe("summarize", () => {
  it("counts the jobs the scheduler will run and the soonest next slot", () => {
    const out = summarize([
      { enabled: true, attentionCode: "", nextDue: "2026-11-14T10:30:00Z" },
      { enabled: true, attentionCode: "", nextDue: "2026-11-14T10:15:00Z" },
      { enabled: false, attentionCode: "", nextDue: null },
    ]);
    expect(out).toEqual({ active: 2, attention: 0, nextDueAt: Date.parse("2026-11-14T10:15:00Z") });
  });

  it("counts a job that waits for the user as attention, not as active", () => {
    const out = summarize([
      { enabled: true, attentionCode: "gh_not_authenticated", nextDue: null },
      { enabled: false, attentionCode: "self_pr", nextDue: null },
    ]);
    expect(out).toEqual({ active: 0, attention: 2, nextDueAt: null });
  });
});
