// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, expect, it } from "vitest";
import { appendCapped, cleanLogLine, mergeLogTail, splitLog } from "./logTail";

describe("cleanLogLine", () => {
  it("drops terminal escape sequences and control characters", () => {
    expect(cleanLogLine("\u001b[31mred\u001b[0m text")).toBe("red text");
    expect(cleanLogLine("\u001b]0;window title\u0007visible")).toBe("visible");
    expect(cleanLogLine("a\u0000b\u0007c\u007fd")).toBe("abcd");
    expect(cleanLogLine("tab\tkept")).toBe("tab\tkept");
  });

  it("keeps only the last state of a progress line that rewrites itself", () => {
    expect(cleanLogLine("10%\r50%\r100%")).toBe("100%");
    expect(cleanLogLine("line\r")).toBe("line");
  });

  it("leaves markup characters alone: the log is plain text, escaped where it is shown", () => {
    expect(cleanLogLine("<script>x</script>")).toBe("<script>x</script>");
  });
});

describe("splitLog", () => {
  it("splits on newlines and ignores the final one", () => {
    expect(splitLog("a\nb\n")).toEqual(["a", "b"]);
    expect(splitLog("a\n\nb")).toEqual(["a", "", "b"]);
    expect(splitLog("")).toEqual([]);
  });
});

describe("appendCapped", () => {
  it("keeps the newest lines and says when it dropped some", () => {
    expect(appendCapped(["a", "b"], ["c"], 5)).toEqual({ lines: ["a", "b", "c"], dropped: false });
    expect(appendCapped(["a", "b"], ["c", "d"], 3)).toEqual({ lines: ["b", "c", "d"], dropped: true });
  });
});

describe("mergeLogTail", () => {
  it("counts a line once when it is in the file and also arrived as an event", () => {
    expect(mergeLogTail(["a", "b", "c"], ["c", "d"])).toEqual(["a", "b", "c", "d"]);
    expect(mergeLogTail(["a", "b", "c"], ["b", "c", "d"])).toEqual(["a", "b", "c", "d"]);
  });

  it("appends everything when nothing overlaps, and handles empty sides", () => {
    expect(mergeLogTail(["a"], ["b"])).toEqual(["a", "b"]);
    expect(mergeLogTail([], ["b"])).toEqual(["b"]);
    expect(mergeLogTail(["a"], [])).toEqual(["a"]);
  });
});
