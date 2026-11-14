// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, expect, it } from "vitest";
import { Kind } from "../../../bindings/github.com/CommitBrief/ui/internal/domain/settings/models.js";
import type { ConfigEntry, ConfigField } from "../../api/settings";
import en from "../../i18n/en.json";
import tr from "../../i18n/tr.json";
import {
  groupEntries,
  groupTitleKeys,
  isDirty,
  keyHintKeys,
  omitKey,
  sameValue,
  shownValue,
  validateDraft,
} from "./configFields";

function field(overrides: Partial<ConfigField> & Pick<ConfigField, "key" | "kind">): ConfigField {
  return { group: "cache", fileOnly: false, secret: false, perProvider: false, ...overrides };
}

function entry(f: ConfigField, value = "", error = ""): ConfigEntry {
  return { field: f, value, error };
}

const ttl = field({ key: "cache.ttl_days", kind: Kind.KindInt, min: 0 });
const cost = field({ key: "cost.warn_threshold_usd", group: "cost", kind: Kind.KindFloat, min: 0 });
const color = field({ key: "output.color", group: "output", kind: Kind.KindEnum, enum: ["auto", "always", "never"] });
const flaky = field({ key: "review.flaky", group: "review", kind: Kind.KindBool });

describe("groupEntries", () => {
  it("lists groups in display order and drops empty ones", () => {
    const groups = groupEntries([entry(flaky), entry(ttl), entry(color)]);
    expect(groups.map((group) => group.group)).toEqual(["output", "cache", "review"]);
  });

  it("hides the active provider (the Providers section owns it) and the vestigial stream switch", () => {
    const provider = field({ key: "provider", group: "provider", kind: Kind.KindString });
    const stream = field({ key: "output.stream", group: "output", kind: Kind.KindBool });
    expect(groupEntries([entry(provider), entry(stream)])).toEqual([]);
  });

  it("never lists a secret, even if a value came with it", () => {
    const secret = field({ key: "providers.<provider>.api_key", group: "provider", kind: Kind.KindString, secret: true, perProvider: true });
    const grouped = groupEntries([entry(secret, "sk-live-should-never-show"), entry(ttl, "7")]);
    const keys = grouped.flatMap((group) => group.entries.map((item) => item.field.key));
    expect(keys).toEqual(["cache.ttl_days"]);
    expect(JSON.stringify(grouped)).not.toContain("sk-live-should-never-show");
  });

  it("keeps a group this build does not know, last", () => {
    const odd = field({ key: "future.key", group: "future", kind: Kind.KindString });
    expect(groupEntries([entry(odd), entry(ttl)]).map((group) => group.group)).toEqual(["cache", "other"]);
  });
});

describe("catalog coverage", () => {
  it("has a title for every group and a one-line hint for every key in both catalogs", () => {
    for (const key of Object.values(groupTitleKeys)) {
      expect(en).toHaveProperty([key]);
      expect(tr).toHaveProperty([key]);
    }
    for (const key of Object.values(keyHintKeys)) {
      expect(en).toHaveProperty([key]);
      expect(tr).toHaveProperty([key]);
    }
  });
});

describe("dirty tracking", () => {
  it("compares values, not spellings", () => {
    expect(sameValue(cost, "0.50", "0.5")).toBe(true);
    expect(sameValue(ttl, "07", "7")).toBe(true);
    expect(sameValue(flaky, "True", "true")).toBe(true);
    expect(sameValue(ttl, "8", "7")).toBe(false);
    expect(sameValue(ttl, "", "7")).toBe(false);
  });

  it("is dirty in the user scope only when the value changed", () => {
    const e = entry(ttl, "7");
    expect(isDirty(e, {}, false)).toBe(false);
    expect(isDirty(e, { "cache.ttl_days": "7" }, false)).toBe(false);
    expect(isDirty(e, { "cache.ttl_days": "14" }, false)).toBe(true);
  });

  it("treats an empty repository field as 'inherit' and writes nothing for it", () => {
    const e = entry(ttl, "7");
    expect(isDirty(e, { "cache.ttl_days": "" }, true)).toBe(false);
    expect(isDirty(e, { "cache.ttl_days": "3" }, true)).toBe(true);
    // The inherited value is not the draft: the field starts empty in a repository scope.
    expect(shownValue(e, {}, true)).toBe("");
    expect(shownValue(e, {}, false)).toBe("7");
  });

  it("never writes a list key or a key that could not be read", () => {
    const list = field({ key: "review.sandbox_command", group: "review", kind: Kind.KindString, fileOnly: true });
    expect(isDirty(entry(list, "a b"), { "review.sandbox_command": "x" }, false)).toBe(false);
    expect(isDirty(entry(ttl, "", "cli_not_found"), { "cache.ttl_days": "3" }, false)).toBe(false);
  });
});

describe("validateDraft", () => {
  it("accepts numbers in range and rejects the rest", () => {
    expect(validateDraft(ttl, "14", false)).toBeNull();
    expect(validateDraft(ttl, "-1", false)).toMatchObject({ key: "settings.config.error.min", params: { min: 0 } });
    expect(validateDraft(ttl, "1.5", false)).toMatchObject({ key: "settings.config.error.int" });
    expect(validateDraft(ttl, "abc", false)).toMatchObject({ key: "settings.config.error.int" });
    expect(validateDraft(ttl, "", false)).toMatchObject({ key: "settings.config.error.number" });
    expect(validateDraft(cost, "0.75", false)).toBeNull();
    expect(validateDraft(cost, "1e3", false)).toMatchObject({ key: "settings.config.error.number" });
  });

  it("only offers the choices the CLI accepts", () => {
    expect(validateDraft(color, "never", false)).toBeNull();
    expect(validateDraft(color, "rainbow", false)).toMatchObject({ key: "settings.config.error.invalid" });
    expect(validateDraft(flaky, "maybe", false)).toMatchObject({ key: "settings.config.error.invalid" });
  });

  it("lets a repository scope leave a field empty", () => {
    expect(validateDraft(ttl, "", true)).toBeNull();
    expect(validateDraft(ttl, "x", true)).not.toBeNull();
  });
});

describe("omitKey", () => {
  it("returns a copy without the key", () => {
    const original = { a: "1", b: "2" };
    expect(omitKey(original, "a")).toEqual({ b: "2" });
    expect(original).toEqual({ a: "1", b: "2" });
  });
});
