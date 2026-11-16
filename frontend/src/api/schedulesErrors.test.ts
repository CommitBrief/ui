// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, expect, it } from "vitest";
import { translate } from "../i18n";
import { describeScheduleError, errorTarget, scheduleErrorCodes, scheduleErrorKey, toScheduleError } from "./schedulesErrors";

describe("toScheduleError", () => {
  it("reads {code, field} from the cause of a rejected call, including the codes of its own", () => {
    const error = toScheduleError({ cause: { code: "schedule_invalid", field: "windows[1].end" } });
    expect(error.code).toBe("schedule_invalid");
    expect(error.field).toBe("windows[1].end");
    expect(toScheduleError({ cause: { code: "autostart_unsupported" } }).code).toBe("autostart_unsupported");
    expect(toScheduleError({ cause: { code: "gh_not_authenticated" } }).code).toBe("gh_not_authenticated");
  });

  it("turns anything unknown into internal, so no foreign text reaches the screen", () => {
    expect(toScheduleError({ cause: { code: "<b>x</b>" } }).code).toBe("internal");
    expect(toScheduleError(new Error("boom")).code).toBe("internal");
  });
});

describe("wording", () => {
  it("has a sentence in both languages for every code", () => {
    for (const code of scheduleErrorCodes) {
      expect(translate("en", scheduleErrorKey(code))).not.toBe("");
      expect(translate("tr", scheduleErrorKey(code))).not.toBe("");
    }
    expect(describeScheduleError({ code: "schedule_invalid" }, (key, params) => translate("en", key, params))).toBe("A time window is not valid.");
    expect(scheduleErrorKey("who_knows")).toBe("jobs.error.internal");
  });
});

describe("errorTarget", () => {
  it("points a window error at its window and field", () => {
    expect(errorTarget({ code: "schedule_invalid", field: "windows[2].tz" })).toEqual({ kind: "window", index: 2, field: "tz" });
    expect(errorTarget({ code: "schedule_invalid", field: "windows" })).toEqual({ kind: "windows" });
  });

  it("points a job error at the name or the repos, and leaves the rest to the form as a whole", () => {
    expect(errorTarget({ code: "job_invalid", field: "name" })).toEqual({ kind: "name" });
    expect(errorTarget({ code: "job_invalid", field: "targets" })).toEqual({ kind: "targets" });
    expect(errorTarget({ code: "run_repository_not_found", field: "targets" })).toBeNull();
    expect(errorTarget({ code: "job_invalid", field: "spec.postConsentAt" })).toBeNull();
    expect(errorTarget({ code: "internal", field: "" })).toBeNull();
  });
});
