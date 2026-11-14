// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, expect, it } from "vitest";
import { translate } from "../i18n";
import { describeJobError, jobErrorCodes, jobErrorKey, nextStepFor, toJobError } from "./jobsErrors";

describe("toJobError", () => {
  it("reads {code, field} from the cause of a rejected call", () => {
    const error = toJobError({ cause: { code: "job_invalid", field: "spec.range" } });
    expect(error.code).toBe("job_invalid");
    expect(error.field).toBe("spec.range");
  });

  it("turns anything unknown into internal, so no foreign text reaches the screen", () => {
    expect(toJobError({ cause: { code: "<img onerror=x>" } }).code).toBe("internal");
    expect(toJobError(new Error("boom")).code).toBe("internal");
    expect(toJobError("x").code).toBe("internal");
  });
});

describe("error wording", () => {
  it("has a sentence in both languages for every code, and a generic one for a stranger", () => {
    for (const code of jobErrorCodes) {
      expect(translate("en", jobErrorKey(code))).not.toBe("");
      expect(translate("tr", jobErrorKey(code))).not.toBe("");
    }
    expect(jobErrorKey("who_knows")).toBe("jobs.error.internal");
    expect(describeJobError({ code: "cli_not_found" }, (key, params) => translate("en", key, params))).toBe("The commitbrief CLI was not found.");
  });
});

describe("nextStepFor", () => {
  it("points a missing gh login at the command, and a missing CLI at Settings", () => {
    expect(nextStepFor("gh_not_authenticated")?.command).toBe("gh auth login");
    expect(nextStepFor("cli_not_found")?.link?.href).toBe("#/settings");
    expect(nextStepFor("cli_too_old")?.hint).toBe("jobs.next.cli_too_old");
    expect(nextStepFor("not_a_repo")?.link?.href).toBe("#/repos");
  });

  it("has nothing to offer for a cancel", () => {
    expect(nextStepFor("canceled")).toBeNull();
  });
});
