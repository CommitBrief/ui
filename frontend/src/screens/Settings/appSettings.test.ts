// SPDX-License-Identifier: GPL-3.0-or-later
import { afterEach, describe, expect, it, vi } from "vitest";
import type { UI } from "../../api/settings";
import { errorField, isAppDirty, resolveLocale, toDraft, toUi, validateApp } from "./appSettings";

const saved: UI = { language: "system", cliPath: "", workers: 1, dailyBudgetUsd: 0, logRetentionDays: 30 };

describe("validateApp", () => {
  it("accepts the stored settings", () => {
    expect(validateApp(toDraft(saved))).toEqual({});
  });

  it("holds every number to the backend's bounds", () => {
    expect(validateApp({ ...toDraft(saved), workers: "0" })).toHaveProperty("workers");
    expect(validateApp({ ...toDraft(saved), workers: "5" })).toHaveProperty("workers");
    expect(validateApp({ ...toDraft(saved), workers: "4" })).toEqual({});
    expect(validateApp({ ...toDraft(saved), dailyBudgetUsd: "-1" })).toHaveProperty("dailyBudgetUsd");
    expect(validateApp({ ...toDraft(saved), dailyBudgetUsd: "10001" })).toHaveProperty("dailyBudgetUsd");
    expect(validateApp({ ...toDraft(saved), dailyBudgetUsd: "12.5" })).toEqual({});
    expect(validateApp({ ...toDraft(saved), logRetentionDays: "0" })).toHaveProperty("logRetentionDays");
    expect(validateApp({ ...toDraft(saved), logRetentionDays: "366" })).toHaveProperty("logRetentionDays");
    expect(validateApp({ ...toDraft(saved), logRetentionDays: "abc" })).toHaveProperty("logRetentionDays");
  });
});

describe("toUi / isAppDirty", () => {
  it("turns the draft back into numbers and trims the path", () => {
    expect(toUi({ ...toDraft(saved), workers: " 3 ", dailyBudgetUsd: "2.50", cliPath: " /opt/cb " })).toEqual({
      language: "system",
      cliPath: "/opt/cb",
      workers: 3,
      dailyBudgetUsd: 2.5,
      logRetentionDays: 30,
    });
  });

  it("is dirty only when something differs", () => {
    expect(isAppDirty(toDraft(saved), saved)).toBe(false);
    expect(isAppDirty({ ...toDraft(saved), language: "tr" }, saved)).toBe(true);
    expect(isAppDirty({ ...toDraft(saved), cliPath: "  " }, saved)).toBe(false);
  });
});

describe("resolveLocale", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("applies an explicit language as is", () => {
    expect(resolveLocale("tr")).toBe("tr");
    expect(resolveLocale("en")).toBe("en");
  });

  it("follows the webview language for 'system', and falls back to English", () => {
    vi.stubGlobal("navigator", { language: "tr-TR" });
    expect(resolveLocale("system")).toBe("tr");
    vi.stubGlobal("navigator", { language: "de-DE" });
    expect(resolveLocale("system")).toBe("en");
  });
});

describe("errorField", () => {
  it("recognises the form fields and nothing else", () => {
    expect(errorField("cliPath")).toBe("cliPath");
    expect(errorField("workers")).toBe("workers");
    expect(errorField("cache.ttl_days")).toBeNull();
    expect(errorField("")).toBeNull();
  });
});
