// SPDX-License-Identifier: GPL-3.0-or-later
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { I18nContext, translate, type Locale } from "../../i18n";
import { SchedulerNotice, type SchedulerNoticeProps } from "./SchedulerNotice";

function render(props: Partial<SchedulerNoticeProps>, locale: Locale = "en"): string {
  const full: SchedulerNoticeProps = {
    status: { paused: false, state: "running", lastTick: "2026-11-16T09:59:30Z", nextTick: "2026-11-16T10:00:00Z" } as SchedulerNoticeProps["status"],
    now: Date.parse("2026-11-16T10:00:00Z"),
    busy: false,
    onPause: () => undefined,
    onResume: () => undefined,
    ...props,
  };
  return renderToStaticMarkup(
    <I18nContext.Provider value={{ locale, setLocale: () => undefined, t: (key, params) => translate(locale, key, params) }}>
      <SchedulerNotice {...full} />
    </I18nContext.Provider>,
  );
}

describe("SchedulerNotice", () => {
  it("offers Pause while running and says schedules need the app", () => {
    const html = render({});
    expect(html).toContain("Schedules run only while CommitBrief UI is running");
    expect(html).toContain(">Pause<");
    expect(html).not.toContain(">Resume<");
    expect(html).toContain("Last tick");
  });

  it("is a warning with Resume when paused, and says manual runs still work", () => {
    const html = render({ status: { paused: true, state: "paused", lastTick: "2026-11-16T09:58:00Z", nextTick: null } as SchedulerNoticeProps["status"] });
    expect(html).toContain("Scheduler paused");
    expect(html).toContain(">Resume<");
    expect(html).not.toContain(">Pause<");
    expect(html).toContain("Runs you start by hand still work.");
    expect(html).toContain("Bot paused");
  });

  it("has no button until the status is read, and a Turkish wording", () => {
    expect(render({ status: null })).not.toContain("<button");
    expect(render({ status: { paused: true, state: "paused", lastTick: null, nextTick: null } as SchedulerNoticeProps["status"] }, "tr")).toContain("Zamanlayıcı duraklatıldı");
  });
});
