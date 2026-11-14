// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, expect, it } from "vitest";
import { parsePRNumber, parseRange } from "./newJob";

describe("parseRange", () => {
  it("accepts ranges, names and hashes, split on whitespace", () => {
    expect(parseRange("main..HEAD")).toEqual({ ok: true, args: ["main..HEAD"] });
    expect(parseRange("  origin/main   feature/x ")).toEqual({ ok: true, args: ["origin/main", "feature/x"] });
    expect(parseRange("HEAD~3..HEAD^")).toEqual({ ok: true, args: ["HEAD~3..HEAD^"] });
    expect(parseRange("v1.2.0...v1.3.0")).toEqual({ ok: true, args: ["v1.2.0...v1.3.0"] });
  });

  it("refuses an empty range, too many arguments and anything git could read as an option", () => {
    expect(parseRange("   ")).toEqual({ ok: false, error: "jobs.new.range.required" });
    expect(parseRange("a b c d e")).toEqual({ ok: false, error: "jobs.new.range.tooMany" });
    expect(parseRange("--output=/tmp/x")).toEqual({ ok: false, error: "jobs.new.range.invalid" });
    expect(parseRange("-p")).toEqual({ ok: false, error: "jobs.new.range.invalid" });
    expect(parseRange("a;rm")).toEqual({ ok: false, error: "jobs.new.range.invalid" });
    expect(parseRange("a".repeat(257))).toEqual({ ok: false, error: "jobs.new.range.invalid" });
  });
});

describe("parsePRNumber", () => {
  it("accepts a positive whole number, with an optional leading #", () => {
    expect(parsePRNumber("128")).toEqual({ ok: true, value: 128 });
    expect(parsePRNumber(" #7 ")).toEqual({ ok: true, value: 7 });
  });

  it("refuses everything else", () => {
    expect(parsePRNumber("")).toEqual({ ok: false, error: "jobs.new.pr.required" });
    for (const bad of ["0", "-3", "1.5", "12a", "2147483648", "1e3"]) {
      expect(parsePRNumber(bad)).toEqual({ ok: false, error: "jobs.new.pr.invalid" });
    }
  });
});
