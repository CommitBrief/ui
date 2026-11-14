// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, expect, it } from "vitest";
import { normalizeApiKey } from "./apiKey";

describe("normalizeApiKey", () => {
  it("treats an empty or blank field as nothing typed, so a stored key is not replaced", () => {
    expect(normalizeApiKey("")).toBeNull();
    expect(normalizeApiKey(" ")).toBeNull();
    expect(normalizeApiKey(" \t\n ")).toBeNull();
  });

  it("drops spaces around a pasted key and keeps the rest as it is", () => {
    expect(normalizeApiKey("sk-abc ")).toBe("sk-abc");
    expect(normalizeApiKey("  sk-abc\n")).toBe("sk-abc");
    expect(normalizeApiKey("sk-a b-c")).toBe("sk-a b-c");
  });
});
