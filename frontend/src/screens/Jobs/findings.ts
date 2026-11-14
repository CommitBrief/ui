// SPDX-License-Identifier: GPL-3.0-or-later
import { severities, type Severity } from "../../components";

// Findings are model output about code that may come from a pull request, so
// they are untrusted: the model can be steered by what it reads. Everything
// here treats them as data only. Fields are coerced to plain strings and
// numbers, and the UI renders them as text nodes (never as HTML, never as
// Markdown, never as links).

export interface Finding {
  severity: Severity;
  file: string;
  line: number;
  lineEnd: number;
  title: string;
  description: string;
  suggestion: string;
  snippet: string;
  ruleId: string;
  fingerprint: string;
}

function text(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function lineNumber(value: unknown): number {
  return typeof value === "number" && Number.isInteger(value) && value > 0 ? value : 0;
}

function normalizeSeverity(value: unknown): Severity {
  const lowered = typeof value === "string" ? value.toLowerCase() : "";
  return (severities as readonly string[]).includes(lowered) ? (lowered as Severity) : "info";
}

/** Reads the stored findings of a task (a JSON array). Anything that is not an object with some text is dropped. */
export function parseFindings(raw: unknown): Finding[] {
  if (!Array.isArray(raw)) return [];
  const out: Finding[] = [];
  for (const item of raw as unknown[]) {
    if (typeof item !== "object" || item === null) continue;
    const record = item as Record<string, unknown>;
    const finding: Finding = {
      severity: normalizeSeverity(record.severity),
      file: text(record.file),
      line: lineNumber(record.line),
      lineEnd: lineNumber(record.lineEnd),
      title: text(record.title),
      description: text(record.description),
      suggestion: text(record.suggestion),
      snippet: text(record.snippet),
      ruleId: text(record.ruleId),
      fingerprint: text(record.fingerprint),
    };
    if (finding.title === "" && finding.description === "") continue;
    out.push(finding);
  }
  return out;
}

const rank = (severity: Severity): number => severities.indexOf(severity);

/** Critical first, info last; inside a level by file and line, so the order is stable between refreshes. */
export function sortFindings(findings: readonly Finding[]): Finding[] {
  return [...findings].sort(
    (a, b) => rank(a.severity) - rank(b.severity) || a.file.localeCompare(b.file) || a.line - b.line || a.title.localeCompare(b.title),
  );
}

export function countBySeverity(findings: readonly Finding[]): Record<Severity, number> {
  const counts: Record<Severity, number> = { critical: 0, high: 0, medium: 0, low: 0, info: 0 };
  for (const finding of findings) counts[finding.severity]++;
  return counts;
}

/** `src/a.go:12`, `src/a.go:12-18` or just `src/a.go`; empty when the finding has no file. */
export function findingLocation(finding: Pick<Finding, "file" | "line" | "lineEnd">): string {
  if (finding.file === "") return "";
  if (finding.line === 0) return finding.file;
  return finding.lineEnd > finding.line ? `${finding.file}:${finding.line}-${finding.lineEnd}` : `${finding.file}:${finding.line}`;
}

/** What a run's stored report says; only what the screen shows. */
export interface ReportView {
  /** Raw model text, set by the CLI only when the answer could not be read as findings. */
  content: string;
  degradeReason: string;
  provider: string;
  model: string;
  cached: boolean;
  remote: RemoteView | null;
}

/** The outcome of a posting run (the CLI's `remote` container). */
export interface RemoteView {
  status: string;
  verdict: string;
  posted: number;
  updated: number;
  unchanged: number;
  resolved: number;
  postFailed: number;
  warnings: number;
  /** The comment-sync step ran (the report has a `sync` container). */
  hasSync: boolean;
}

function count(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) && value > 0 ? Math.floor(value) : 0;
}

function record(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Record<string, unknown>) : null;
}

/** Reads the stored report of a task (a JSON object); null when there is none. */
export function parseReport(raw: unknown): ReportView | null {
  const report = record(raw);
  if (!report) return null;
  const meta = record(report.meta) ?? {};
  const remote = record(report.remote);
  const sync = remote ? (record(remote.sync) ?? {}) : {};
  return {
    content: text(report.content),
    degradeReason: text(meta.degradeReason),
    provider: text(meta.provider),
    model: text(meta.model),
    cached: meta.cached === true,
    remote: remote
      ? {
          status: text(remote.status),
          verdict: text(remote.verdict),
          posted: count(sync.posted),
          updated: count(sync.updated),
          unchanged: count(sync.unchanged),
          resolved: count(sync.resolved),
          postFailed: count(sync.postFailed),
          warnings: Array.isArray(remote.warnings) ? remote.warnings.length : 0,
          hasSync: record(remote.sync) !== null,
        }
      : null,
  };
}

/**
 * How a snippet is drawn. When every line of it starts with `+`, `-` or a space
 * (and at least one is a change) it is a diff and added and removed lines get
 * their own color; any other code is drawn as is.
 */
export type SnippetLine = { text: string; kind: "add" | "del" | "plain" };

export function snippetLines(snippet: string): SnippetLine[] {
  const lines = snippet.replace(/\r\n?/g, "\n").replace(/\n+$/, "").split("\n");
  const nonEmpty = lines.filter((line) => line !== "");
  const isDiff = nonEmpty.length > 0 && nonEmpty.every((line) => /^[+\- ]/.test(line)) && nonEmpty.some((line) => /^[+-]/.test(line));
  return lines.map((line) => ({
    text: line,
    kind: isDiff && line.startsWith("+") ? "add" : isDiff && line.startsWith("-") ? "del" : "plain",
  }));
}
