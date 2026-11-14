// SPDX-License-Identifier: GPL-3.0-or-later
import type { ConfigEntry, ConfigField } from "../../api/settings";
import type { MessageKey, MessageParams } from "../../i18n";

// Pure helpers of the CLI configuration form (no React, no Wails), so the rules
// are unit-tested. The backend (internal/domain/settings catalog) validates
// every write again; this is the early, friendlier half of that check.

/**
 * Keys the form does not list. `provider` is the active provider, which the
 * Providers section owns; `output.stream` is vestigial in the CLI (review is
 * not streamed), so showing a switch for it would promise something false.
 */
export const hiddenKeys: readonly string[] = ["provider", "output.stream"];

/** Groups in the order the form shows them (the backend catalog's order). */
export const groupOrder = ["output", "cache", "guard", "cost", "command", "review"] as const;
export type ConfigGroup = (typeof groupOrder)[number] | "other";

export const groupTitleKeys: Record<ConfigGroup, MessageKey> = {
  output: "settings.config.group.output",
  cache: "settings.config.group.cache",
  guard: "settings.config.group.guard",
  cost: "settings.config.group.cost",
  command: "settings.config.group.command",
  review: "settings.config.group.review",
  other: "settings.config.group.other",
};

/** One sentence per key, in both languages (i18n catalogs). */
export const keyHintKeys: Record<string, MessageKey> = {
  "output.lang": "settings.config.key.output.lang",
  "output.color": "settings.config.key.output.color",
  "cache.enabled": "settings.config.key.cache.enabled",
  "cache.ttl_days": "settings.config.key.cache.ttl_days",
  "cache.max_size_mb": "settings.config.key.cache.max_size_mb",
  "guard.secret_scan": "settings.config.key.guard.secret_scan",
  "guard.token_preflight": "settings.config.key.guard.token_preflight",
  "guard.injection_scan": "settings.config.key.guard.injection_scan",
  "guard.secret_patterns": "settings.config.key.guard.secret_patterns",
  "cost.warn_threshold_usd": "settings.config.key.cost.warn_threshold_usd",
  "command.default": "settings.config.key.command.default",
  "review.flaky": "settings.config.key.review.flaky",
  "review.baseline": "settings.config.key.review.baseline",
  "review.architecture": "settings.config.key.review.architecture",
  "review.architecture_file": "settings.config.key.review.architecture_file",
  "review.sandbox_rerun": "settings.config.key.review.sandbox_rerun",
  "review.sandbox_command": "settings.config.key.review.sandbox_command",
  "review.timeout": "settings.config.key.review.timeout",
};

export interface GroupedEntries {
  group: ConfigGroup;
  entries: ConfigEntry[];
}

/** The entries the form lists, per group in display order; empty groups are dropped, secrets never appear. */
export function groupEntries(entries: readonly ConfigEntry[]): GroupedEntries[] {
  const shown = entries.filter((entry) => !hiddenKeys.includes(entry.field.key) && !entry.field.secret);
  const out: GroupedEntries[] = [];
  for (const group of groupOrder) {
    const inGroup = shown.filter((entry) => entry.field.group === group);
    if (inGroup.length > 0) out.push({ group, entries: inGroup });
  }
  // A group the backend added but this build does not know still shows, last.
  const rest = shown.filter((entry) => !(groupOrder as readonly string[]).includes(entry.field.group));
  if (rest.length > 0) out.push({ group: "other", entries: rest });
  return out;
}

/** What the user typed, by key. A key with no entry has not been touched. */
export type Drafts = Readonly<Record<string, string>>;

function sameNumber(a: string, b: string): boolean {
  const x = Number(a.trim());
  const y = Number(b.trim());
  return a.trim() !== "" && b.trim() !== "" && Number.isFinite(x) && Number.isFinite(y) && x === y;
}

/** Whether two spellings of a value are the same value (`0.50` and `0.5`, `True` and `true`). */
export function sameValue(field: ConfigField, a: string, b: string): boolean {
  if (a === b) return true;
  if (field.kind === "int" || field.kind === "float") return sameNumber(a, b);
  if (field.kind === "bool") return a.trim().toLowerCase() === b.trim().toLowerCase();
  return a.trim() === b.trim();
}

/**
 * The value a field shows. In the user scope that is the typed draft, else the
 * value the CLI reported. In a repository scope the reported value is only what
 * the repository inherits, so the field starts empty ("inherit").
 */
export function shownValue(entry: ConfigEntry, drafts: Drafts, repoScope: boolean): string {
  const draft = drafts[entry.field.key];
  if (draft !== undefined) return draft;
  return repoScope ? "" : entry.value;
}

/** Whether saving would write this key. */
export function isDirty(entry: ConfigEntry, drafts: Drafts, repoScope: boolean): boolean {
  const draft = drafts[entry.field.key];
  if (draft === undefined || entry.field.fileOnly || entry.error !== "") return false;
  // In a repository scope an empty field means "inherit": nothing to write.
  if (repoScope) return draft.trim() !== "";
  return !sameValue(entry.field, draft, entry.value);
}

export interface FieldIssue {
  key: MessageKey;
  params?: MessageParams;
}

/** Early validation of a typed value; null when it is acceptable (or, in a repository scope, empty = inherit). */
export function validateDraft(field: ConfigField, draft: string, repoScope: boolean): FieldIssue | null {
  const value = draft.trim();
  if (repoScope && value === "") return null;
  if (field.kind === "int" || field.kind === "float") {
    if (value === "") return { key: "settings.config.error.number" };
    if (field.kind === "int" ? !/^-?\d+$/.test(value) : !/^-?(\d+\.?\d*|\.\d+)$/.test(value)) {
      return { key: field.kind === "int" ? "settings.config.error.int" : "settings.config.error.number" };
    }
    const n = Number(value);
    if (!Number.isFinite(n)) return { key: "settings.config.error.number" };
    if (field.min != null && n < field.min) return { key: "settings.config.error.min", params: { min: field.min } };
    if (field.max != null && n > field.max) return { key: "settings.config.error.max", params: { max: field.max } };
  }
  if (field.kind === "enum" && !(field.enum ?? []).includes(value)) return { key: "settings.config.error.invalid" };
  if (field.kind === "bool" && value !== "true" && value !== "false") return { key: "settings.config.error.invalid" };
  return null;
}

/** The value handed to `config set`: bools and enums as chosen, text trimmed. */
export function valueToWrite(draft: string): string {
  return draft.trim();
}

/** A copy of `record` without `key` (a plain delete on a computed key is banned by the lint rules). */
export function omitKey<V>(record: Readonly<Record<string, V>>, key: string): Record<string, V> {
  return Object.fromEntries(Object.entries(record).filter(([name]) => name !== key));
}
