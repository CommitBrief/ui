// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, expect, it } from "vitest";
import { countBySeverity, findingLocation, parseFindings, parseReport, snippetLines, sortFindings } from "./findings";

describe("parseFindings", () => {
  it("coerces untrusted fields to plain strings and numbers", () => {
    const [finding] = parseFindings([
      { severity: { toString: "x" }, file: 5, line: "12", lineEnd: -3, title: ["t"], description: "d", suggestion: null, snippet: {}, ruleId: 1, fingerprint: true },
    ]);
    expect(finding).toEqual({
      severity: "info",
      file: "",
      line: 0,
      lineEnd: 0,
      title: "",
      description: "d",
      suggestion: "",
      snippet: "",
      ruleId: "",
      fingerprint: "",
    });
  });

  it("maps an unknown severity to info and accepts any letter case of a known one", () => {
    const list = parseFindings([
      { severity: "HIGH", title: "a" },
      { severity: "catastrophic", title: "b" },
    ]);
    expect(list.map((f) => f.severity)).toEqual(["high", "info"]);
  });

  it("drops what is not a finding and anything that says nothing", () => {
    expect(parseFindings(null)).toEqual([]);
    expect(parseFindings({})).toEqual([]);
    expect(parseFindings("[]")).toEqual([]);
    expect(parseFindings([null, 3, "x", [], {}, { severity: "low" }, { title: "ok" }])).toHaveLength(1);
  });
});

describe("sortFindings and countBySeverity", () => {
  const list = parseFindings([
    { severity: "info", file: "z.go", line: 1, title: "i" },
    { severity: "critical", file: "b.go", line: 20, title: "c2" },
    { severity: "critical", file: "b.go", line: 3, title: "c1" },
    { severity: "high", file: "a.go", line: 1, title: "h" },
  ]);

  it("orders critical to info, then by file and line, without touching the input", () => {
    expect(sortFindings(list).map((f) => f.title)).toEqual(["c1", "c2", "h", "i"]);
    expect(list[0]?.title).toBe("i");
  });

  it("counts every level", () => {
    expect(countBySeverity(list)).toEqual({ critical: 2, high: 1, medium: 0, low: 0, info: 1 });
  });
});

describe("findingLocation", () => {
  it("writes file:line and file:start-end", () => {
    expect(findingLocation({ file: "a.go", line: 4, lineEnd: 0 })).toBe("a.go:4");
    expect(findingLocation({ file: "a.go", line: 4, lineEnd: 9 })).toBe("a.go:4-9");
    expect(findingLocation({ file: "a.go", line: 0, lineEnd: 0 })).toBe("a.go");
    expect(findingLocation({ file: "", line: 4, lineEnd: 0 })).toBe("");
  });
});

describe("parseReport", () => {
  it("reads the degraded state and the model", () => {
    const report = parseReport({ content: "raw", findings: [], meta: { degradeReason: "bad json", provider: "openai", model: "gpt", cached: true } });
    expect(report).toMatchObject({ content: "raw", degradeReason: "bad json", provider: "openai", model: "gpt", cached: true, remote: null });
  });

  it("reads what a posting run did", () => {
    const report = parseReport({
      meta: {},
      remote: { status: "submitted", verdict: "comment", sync: { posted: 2, updated: 1, resolved: 3, postFailed: 0 }, warnings: [{}, {}] },
    });
    expect(report?.remote).toEqual({ status: "submitted", verdict: "comment", posted: 2, updated: 1, unchanged: 0, resolved: 3, postFailed: 0, warnings: 2, hasSync: true });
  });

  it("is null for anything that is not an object", () => {
    expect(parseReport(null)).toBeNull();
    expect(parseReport([])).toBeNull();
    expect(parseReport("x")).toBeNull();
  });
});

describe("snippetLines", () => {
  it("marks added and removed lines of a diff", () => {
    expect(snippetLines("-old\n+new\n same")).toEqual([
      { text: "-old", kind: "del" },
      { text: "+new", kind: "add" },
      { text: " same", kind: "plain" },
    ]);
  });

  it("leaves ordinary code uncolored, even a YAML list", () => {
    expect(snippetLines("- a\nkey: value").every((line) => line.kind === "plain")).toBe(true);
    expect(snippetLines("func main() {}").every((line) => line.kind === "plain")).toBe(true);
  });
});
