// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, expect, it } from "vitest";
import en from "../i18n/en.json";
import tr from "../i18n/tr.json";
import {
  RepoError,
  describeRepoError,
  errorFormField,
  repoErrorCodes,
  repoErrorKey,
  toRepoError,
  type Translate,
} from "./repoErrors";

// A stand-in translator that shows the key and its parameters.
const t: Translate = (key, params) => (params ? `${key}:${JSON.stringify(params)}` : key);

describe("toRepoError", () => {
  it("reads {code, field, entry} from the cause of a rejected call", () => {
    const error = toRepoError({ message: "x", cause: { code: "repo_owner_invalid", field: "owner", entry: 3 } });
    expect(error).toBeInstanceOf(RepoError);
    expect([error.code, error.field, error.entry]).toEqual(["repo_owner_invalid", "owner", 3]);
  });

  it("passes a RepoError through", () => {
    const original = new RepoError("repo_duplicate");
    expect(toRepoError(original)).toBe(original);
  });

  it("turns anything unrecognised into internal, so no stray text reaches the UI", () => {
    for (const value of [null, undefined, "boom", new Error("secret /path"), { cause: "text" }, { cause: { code: 5 } }, { cause: { code: "made_up" } }]) {
      expect(toRepoError(value).code).toBe("internal");
    }
  });
});

describe("error code to message mapping", () => {
  it("gives every backend code a message in both catalogs", () => {
    for (const code of repoErrorCodes) {
      const key = repoErrorKey(code);
      expect(en).toHaveProperty([key]);
      expect(tr).toHaveProperty([key]);
    }
  });

  it("falls back to the generic message for an unknown code", () => {
    expect(repoErrorKey("nope")).toBe("repos.error.internal");
  });

  it("maps the codes of the closed set to their own sentences", () => {
    expect(repoErrorKey("repo_duplicate")).toBe("repos.error.repo_duplicate");
    expect(repoErrorKey("repo_local_path_network")).toBe("repos.error.repo_local_path_network");
    expect(repoErrorKey("repo_owner_invalid")).toBe("repos.validation.owner_invalid");
    expect(repoErrorKey("repo_import_too_large")).toBe("repos.error.repo_import_too_large");
  });

  it("puts the entry number of an import error in front of the sentence", () => {
    expect(describeRepoError({ code: "repo_name_invalid", entry: 2 }, t)).toBe(
      'repos.error.entry:{"entry":2,"message":"repos.validation.name_invalid"}',
    );
    expect(describeRepoError({ code: "repo_duplicate", entry: 0 }, t)).toBe("repos.error.repo_duplicate");
  });
});

describe("errorFormField", () => {
  it("routes owner and name errors to the one owner/name input", () => {
    expect(errorFormField({ code: "repo_owner_invalid", field: "owner" })).toBe("slug");
    expect(errorFormField({ code: "repo_name_invalid", field: "name" })).toBe("slug");
  });

  it("routes path errors to the path field and the rest to the form", () => {
    expect(errorFormField({ code: "repo_local_path_missing", field: "localPath" })).toBe("localPath");
    expect(errorFormField({ code: "repo_duplicate", field: "" })).toBeNull();
    expect(errorFormField({ code: "internal", field: "" })).toBeNull();
  });
});
