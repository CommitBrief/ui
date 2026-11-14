// SPDX-License-Identifier: GPL-3.0-or-later
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import en from "../i18n/en.json";
import tr from "../i18n/tr.json";
import {
  SettingsError,
  describeSettingsError,
  settingsErrorCodes,
  settingsErrorKey,
  toSettingsError,
  type Translate,
} from "./settingsErrors";

// A stand-in translator that shows the key.
const t: Translate = (key) => key;

describe("toSettingsError", () => {
  it("reads {code, field} from the cause of a rejected call", () => {
    const error = toSettingsError({ message: "x", cause: { code: "settings_value_invalid", field: "cliPath" } });
    expect(error).toBeInstanceOf(SettingsError);
    expect([error.code, error.field]).toEqual(["settings_value_invalid", "cliPath"]);
  });

  it("passes a SettingsError through", () => {
    const original = new SettingsError("cli_not_found");
    expect(toSettingsError(original)).toBe(original);
  });

  it("turns anything unrecognised into internal, so no stray text reaches the UI", () => {
    expect(toSettingsError(new Error("boom")).code).toBe("internal");
    expect(toSettingsError({ cause: { code: "not_in_the_set" } }).code).toBe("internal");
    expect(toSettingsError({ cause: "string cause" }).code).toBe("internal");
    expect(toSettingsError(null).code).toBe("internal");
  });
});

describe("error messages", () => {
  it("has a sentence in both catalogs for every code in the closed set", () => {
    for (const code of settingsErrorCodes) {
      const key = settingsErrorKey(code);
      expect(en, `en.json is missing ${key}`).toHaveProperty([key]);
      expect(tr, `tr.json is missing ${key}`).toHaveProperty([key]);
    }
  });

  it("gives an unknown code the generic sentence", () => {
    expect(describeSettingsError({ code: "whatever" }, t)).toBe("settings.error.internal");
  });
});

// The backend sends codes, never text. If a code is added to or removed from
// the Go sets without this list moving with it, the user would see the generic
// message and CI would stay green. This compares the two, read from the sources.
describe("code set", () => {
  const goCodes = (file: string): string[] => {
    const source = readFileSync(new URL(`../../../internal/domain/${file}`, import.meta.url), "utf8");
    return [...source.matchAll(/^\s*[A-Za-z]+\s+Code\s*=\s*"([a-z_]+)"/gm)].map((match) => match[1] ?? "");
  };

  it("equals the union of the settings and errcode sets of the Go side", () => {
    const go = new Set([...goCodes("settings/settings.go"), ...goCodes("errcode/errcode.go")]);
    // The two Go sets share "canceled"; the union collapses it.
    expect([...go].sort()).toEqual([...settingsErrorCodes].sort());
  });
});
