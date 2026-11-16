// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, expect, it } from "vitest";
import { translate, type Locale, type MessageKey, type MessageParams } from "../i18n";
import { schedulerLine } from "./schedulerText";

const tFor = (locale: Locale) => (key: MessageKey, params?: MessageParams) => translate(locale, key, params);
const now = Date.parse("2026-11-16T10:00:00Z");

describe("schedulerLine", () => {
  it("says paused, with the last look, in both languages", () => {
    const status = { paused: true, state: "paused" as const, lastTick: "2026-11-16T09:58:00Z", nextTick: null };
    expect(schedulerLine(status, now, "en", tFor("en"))).toEqual({ text: "Bot paused · Last tick 2 minutes ago", paused: true });
    expect(schedulerLine(status, now, "tr", tFor("tr"))).toEqual({ text: "Bot duraklatıldı · Son tick 2 dakika önce", paused: true });
  });

  it("gives the last and the next look while running", () => {
    const status = { paused: false, state: "running" as const, lastTick: "2026-11-16T09:59:30Z", nextTick: "2026-11-16T10:00:00Z" };
    const line = schedulerLine(status, now, "en", tFor("en"));
    expect(line.paused).toBe(false);
    expect(line.text).toBe("Last tick 30 seconds ago · next now");
    const later = schedulerLine({ ...status, nextTick: "2026-11-16T10:00:20Z" }, now, "en", tFor("en"));
    expect(later.text).toBe("Last tick 30 seconds ago · next in 20 seconds");
  });

  it("says when the time windows are all closed, and when there was no tick yet", () => {
    const outside = schedulerLine({ paused: false, state: "outside_window" as const, lastTick: null, nextTick: null }, now, "en", tFor("en"));
    expect(outside.text).toBe("No tick yet · outside the time windows");
  });
});
