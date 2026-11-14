// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, expect, it } from "vitest";
import { joinSlug, sameSlug, splitSlug, validateDraft, validateName, validateOwner } from "./validation";

const draft = (slug: string) => ({ slug, localPath: "" });

describe("splitSlug", () => {
  it("splits exactly one slash and trims", () => {
    expect(splitSlug(" CommitBrief / ui ")).toEqual({ owner: "CommitBrief", name: "ui" });
  });

  it.each(["", "ui", "a/b/c", "/ui", "owner/", " / "])("rejects %j", (value) => {
    expect(splitSlug(value)).toBeNull();
  });
});

describe("validateDraft", () => {
  it("accepts a valid owner/name", () => {
    expect(validateDraft(draft("CommitBrief/ui"))).toEqual({});
    expect(validateDraft(draft("my-org/repo.name_v2"))).toEqual({});
  });

  it("asks for a value when empty", () => {
    expect(validateDraft(draft("   "))).toEqual({ slug: "repos.validation.slug_required" });
  });

  it("asks for the owner/name form without exactly one slash", () => {
    expect(validateDraft(draft("justname"))).toEqual({ slug: "repos.validation.slug_format" });
    expect(validateDraft(draft("a/b/c"))).toEqual({ slug: "repos.validation.slug_format" });
  });

  it("rejects an owner the backend would reject", () => {
    expect(validateDraft(draft("bad_owner/repo"))).toEqual({ slug: "repos.validation.owner_invalid" });
    expect(validateDraft(draft(`${"a".repeat(101)}/repo`))).toEqual({ slug: "repos.validation.owner_invalid" });
  });

  it("rejects a name the backend would reject", () => {
    expect(validateDraft(draft("owner/bad name"))).toEqual({ slug: "repos.validation.name_invalid" });
    expect(validateDraft(draft("owner/.."))).toEqual({ slug: "repos.validation.name_invalid" });
    expect(validateDraft(draft(`owner/${"a".repeat(101)}`))).toEqual({ slug: "repos.validation.name_invalid" });
  });
});

describe("field rules", () => {
  it("mirrors the Go patterns at their edges", () => {
    expect(validateOwner("a".repeat(100))).toBe(true);
    expect(validateOwner("a".repeat(101))).toBe(false);
    expect(validateOwner("-leading-dash-is-allowed-by-go")).toBe(true);
    expect(validateName(".")).toBe(false);
    expect(validateName("..")).toBe(false);
    expect(validateName("...")).toBe(true);
  });
});

describe("slug helpers", () => {
  it("joins and compares case-insensitively", () => {
    expect(joinSlug("a", "b")).toBe("a/b");
    expect(sameSlug("CommitBrief/UI", " commitbrief/ui ")).toBe(true);
    expect(sameSlug("a/b", "a/c")).toBe(false);
  });
});
