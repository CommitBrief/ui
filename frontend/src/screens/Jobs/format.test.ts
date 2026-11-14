// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, expect, it } from "vitest";
import { elapsed, formatCost, formatDuration, formatRelative, parseTime, shortOid } from "./format";

describe("formatDuration", () => {
  it("reads in seconds, minutes and hours", () => {
    expect(formatDuration(400)).toBe("<1 s");
    expect(formatDuration(42_000)).toBe("42 s");
    expect(formatDuration(185_000)).toBe("3 min 05 s");
    expect(formatDuration(7_620_000)).toBe("2 h 07 min");
    expect(formatDuration(-1)).toBe("");
    expect(formatDuration(185_000, "tr")).toBe("3 dk 05 sn");
    expect(formatDuration(7_620_000, "tr")).toBe("2 sa 07 dk");
  });
});

describe("formatCost", () => {
  it("shows sub-cent costs with four decimals", () => {
    expect(formatCost(0)).toBe("$0.00");
    expect(formatCost(0.0123)).toBe("$0.01");
    expect(formatCost(0.0042)).toBe("$0.0042");
    expect(formatCost(1.5)).toBe("$1.50");
    expect(formatCost(Number.NaN)).toBe("$0.00");
  });
});

describe("parseTime and elapsed", () => {
  it("treats a Go zero time as no time", () => {
    expect(parseTime("0001-01-01T00:00:00Z")).toBeNull();
    expect(parseTime("")).toBeNull();
    expect(parseTime(null)).toBeNull();
    expect(parseTime("2026-11-14T10:00:00Z")).toBe(Date.parse("2026-11-14T10:00:00Z"));
  });

  it("counts a running task up to now", () => {
    const now = Date.parse("2026-11-14T10:00:30Z");
    expect(elapsed("2026-11-14T10:00:00Z", null, now)).toBe(30_000);
    expect(elapsed("2026-11-14T10:00:00Z", "2026-11-14T10:00:10Z", now)).toBe(10_000);
    expect(elapsed(null, null, now)).toBeNull();
  });
});

describe("formatRelative", () => {
  const now = Date.parse("2026-11-14T12:00:00Z");
  it("follows the interface language", () => {
    expect(formatRelative(now - 5 * 60_000, now, "en")).toBe("5 minutes ago");
    expect(formatRelative(now - 5 * 60_000, now, "tr")).toBe("5 dakika önce");
    expect(formatRelative(now - 10_000, now, "en")).toBe("now");
  });
});

describe("shortOid", () => {
  it("keeps the first seven characters", () => {
    expect(shortOid("0123456789abcdef")).toBe("0123456");
  });
});
