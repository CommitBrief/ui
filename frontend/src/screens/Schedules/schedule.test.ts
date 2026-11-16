// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, expect, it } from "vitest";
import { translate, type Locale, type MessageKey, type MessageParams } from "../../i18n";
import {
  EVERY_DAY,
  WEEKDAYS,
  consentRecorded,
  daysSummary,
  emptyDraft,
  everySummary,
  formatMinutes,
  hasIssues,
  parseTimeInput,
  saveBlocker,
  toDraft,
  toInput,
  toTimeInput,
  validateDraft,
  windowSummary,
  type ScheduleDraft,
  type WindowDraft,
} from "./schedule";

const tFor = (locale: Locale) => (key: MessageKey, params?: MessageParams) => translate(locale, key, params);
const en = tFor("en");
const tr = tFor("tr");

describe("windowSummary", () => {
  const weekdays = { days: WEEKDAYS, start: 540, end: 1080, every: 15 };

  it("words a weekday window in English", () => {
    expect(windowSummary(weekdays, en)).toBe("Mon–Fri 09:00–18:00, every 15 min");
  });

  it("words the same window in Turkish", () => {
    expect(windowSummary(weekdays, tr)).toBe("Pzt–Cum 09:00–18:00, 15 dk'da bir");
  });

  it("collapses runs of three or more days and lists the rest", () => {
    expect(daysSummary(1 | 2 | 4, en)).toBe("Mon–Wed");
    expect(daysSummary(1 | 4 | 16, en)).toBe("Mon, Wed, Fri");
    expect(daysSummary(1 | 2, tr)).toBe("Pzt, Sal");
    expect(daysSummary(32 | 64, tr)).toBe("Cmt, Paz");
    expect(daysSummary(1 | 2 | 4 | 32 | 64, en)).toBe("Mon–Wed, Sat, Sun");
    expect(daysSummary(EVERY_DAY, en)).toBe("Every day");
    expect(daysSummary(EVERY_DAY, tr)).toBe("Her gün");
  });

  it("says hours when the interval is a whole number of hours, and the end of the day as 24:00", () => {
    expect(everySummary(120, en)).toBe("every 2 h");
    expect(everySummary(120, tr)).toBe("2 saatte bir");
    expect(everySummary(90, en)).toBe("every 90 min");
    expect(windowSummary({ days: EVERY_DAY, start: 0, end: 1440, every: 60 }, en)).toBe("Every day 00:00–24:00, every 1 h");
  });
});

describe("times", () => {
  it("formats minutes and reads time inputs; an end of 00:00 is midnight", () => {
    expect(formatMinutes(570)).toBe("09:30");
    expect(formatMinutes(1440)).toBe("24:00");
    expect(toTimeInput(1440)).toBe("00:00");
    expect(parseTimeInput("09:30", false)).toBe(570);
    expect(parseTimeInput("00:00", false)).toBe(0);
    expect(parseTimeInput("00:00", true)).toBe(1440);
    expect(parseTimeInput("24:00", true)).toBeNull();
    expect(parseTimeInput("9:5", false)).toBeNull();
    expect(parseTimeInput("", true)).toBeNull();
  });
});

function firstWindow(draft: ScheduleDraft): WindowDraft {
  const window = draft.windows[0];
  if (!window) throw new Error("the draft has no window");
  return window;
}

describe("validateDraft", () => {
  const good = () => ({ ...emptyDraft("Europe/Istanbul"), name: "Team bot", targets: [1] });

  it("accepts a filled draft", () => {
    expect(hasIssues(validateDraft(good(), 200))).toBe(false);
  });

  it("asks for a name, a repo and a day", () => {
    const draft = { ...emptyDraft("UTC"), windows: [{ ...firstWindow(emptyDraft("UTC")), days: 0 }] };
    const issues = validateDraft(draft, 200);
    expect(issues.name).toBe("schedules.form.name.required");
    expect(issues.targets).toBe("schedules.form.targets.required");
    expect(issues.windows[0]?.days).toBe("schedules.form.days.required");
  });

  it("refuses an end that is not after the start, an odd interval and an unknown zone", () => {
    const base = good();
    const w = firstWindow(base);
    expect(validateDraft({ ...base, windows: [{ ...w, start: "18:00", end: "09:00" }] }, 200).windows[0]?.end).toBe("schedules.form.end.afterStart");
    expect(validateDraft({ ...base, windows: [{ ...w, every: "0" }] }, 200).windows[0]?.every).toBe("schedules.form.every.invalid");
    expect(validateDraft({ ...base, windows: [{ ...w, every: "1.5" }] }, 200).windows[0]?.every).toBe("schedules.form.every.invalid");
    expect(validateDraft({ ...base, windows: [{ ...w, every: "1441" }] }, 200).windows[0]?.every).toBe("schedules.form.every.invalid");
    expect(validateDraft({ ...base, windows: [{ ...w, tz: "Mars/Olympus" }] }, 200).windows[0]?.tz).toBe("schedules.form.tz.invalid");
    expect(validateDraft({ ...base, windows: [{ ...w, tz: "Local" }] }, 200).windows[0]?.tz).toBe("schedules.form.tz.invalid");
    // A window that ends at midnight is fine.
    expect(validateDraft({ ...base, windows: [{ ...w, start: "20:00", end: "00:00" }] }, 200).windows[0]?.end).toBeUndefined();
  });

  it("limits the name length", () => {
    expect(validateDraft({ ...good(), name: "x".repeat(201) }, 200).name).toBe("schedules.form.name.tooLong");
  });
});

describe("saveBlocker", () => {
  const preview = emptyDraft("UTC");
  const post = { ...preview, mode: "post" as const };

  it("never blocks a preview job, whatever the CLI can do", () => {
    expect(saveBlocker(preview, { consentRecorded: false, posting: false })).toBeNull();
  });

  it("blocks a post job until the consent is given in this session or was recorded earlier", () => {
    expect(saveBlocker(post, { consentRecorded: false, posting: true })).toBe("consent");
    expect(saveBlocker({ ...post, consented: true }, { consentRecorded: false, posting: true })).toBeNull();
    expect(saveBlocker(post, { consentRecorded: true, posting: true })).toBeNull();
  });

  it("blocks a post job when the CLI cannot post, even with the consent", () => {
    expect(saveBlocker({ ...post, consented: true }, { consentRecorded: true, posting: false })).toBe("cli");
  });
});

describe("toInput", () => {
  it("sends the consent only for a post job, and the window as minutes", () => {
    const draft = { ...emptyDraft("Europe/Istanbul"), name: " Bot ", targets: [2, 3], mode: "post" as const, consented: true };
    const input = toInput(draft, 7);
    expect(input).toMatchObject({ id: 7, name: "Bot", mode: "post", targets: [2, 3], includeDrafts: false, postConsent: true, enabled: true });
    expect(input.windows).toEqual([{ days: WEEKDAYS, start: 540, end: 1080, every: 15, tz: "Europe/Istanbul" }]);
    expect(toInput({ ...draft, mode: "preview" }, 0).postConsent).toBe(false);
  });
});

describe("toDraft and consentRecorded", () => {
  const job = {
    id: 4,
    name: "Bot",
    mode: "post",
    enabled: false,
    targets: [1],
    includeDrafts: true,
    postConsentAt: "2026-11-01T10:00:00Z",
    windows: [{ days: 5, start: 600, end: 1440, every: 30, tz: "UTC" }],
    nextDue: null,
    attentionCode: "",
    attentionAt: null,
    createdAt: "2026-11-01T09:00:00Z",
  } as unknown as Parameters<typeof toDraft>[0];

  it("opens a stored job as it is, the end of the day as 00:00", () => {
    const draft = toDraft(job, "UTC");
    expect(draft).toMatchObject({ name: "Bot", mode: "post", enabled: false, skipDrafts: false, targets: [1], consented: false });
    expect(draft.windows[0]).toMatchObject({ days: 5, start: "10:00", end: "00:00", every: "30", tz: "UTC" });
    expect(consentRecorded(job)).toBe(true);
    expect(consentRecorded({ ...job, mode: "preview" } as typeof job)).toBe(false);
    expect(consentRecorded(null)).toBe(false);
  });
});
