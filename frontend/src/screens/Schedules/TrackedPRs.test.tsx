// SPDX-License-Identifier: GPL-3.0-or-later
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { I18nContext, translate } from "../../i18n";
import { TrackedPRs } from "./TrackedPRs";
import type { TrackedPRView } from "../../api/schedules";

function render(rows: unknown[]): string {
  return renderToStaticMarkup(
    <I18nContext.Provider value={{ locale: "en", setLocale: () => undefined, t: (key, params) => translate("en", key, params) }}>
      <TrackedPRs rows={rows as TrackedPRView[]} now={Date.parse("2026-11-16T10:00:00Z")} />
    </I18nContext.Provider>,
  );
}

const row = {
  repositoryId: 1,
  repository: "acme/api",
  number: 214,
  jobId: 3,
  jobName: "Team bot",
  mode: "preview",
  jobEnabled: true,
  state: "open",
  firstSeenAt: "2026-11-16T08:00:00Z",
  lastHeadOid: "9f2c1d07aa11bb22cc33dd44ee55ff6677889900",
  lastRunAt: "2026-11-16T09:50:00Z",
};

describe("TrackedPRs", () => {
  it("lists the real tracked pull requests: repo#N, short head, last run, open, schedule", () => {
    const html = render([row]);
    expect(html).toContain("acme/");
    expect(html).toContain("api#214");
    expect(html).toContain("9f2c1d0");
    expect(html).not.toContain("9f2c1d07aa");
    expect(html).toContain("10 minutes ago");
    expect(html).toContain("Open");
    expect(html).toContain("Team bot");
    expect(html).toContain("closed or merged");
  });

  it("says a pull request has not been reviewed yet, and shows a paused schedule", () => {
    const html = render([{ ...row, lastHeadOid: "", lastRunAt: null, jobEnabled: false }]);
    expect(html).toContain("not reviewed yet");
    expect(html).toContain("(Off)");
  });

  it("shows an empty list in words, and names as plain text", () => {
    expect(render([])).toContain("The bot is not following any pull request yet.");
    const html = render([{ ...row, jobName: "<script>alert(1)</script>" }]);
    expect(html).not.toContain("<script>");
    expect(html).toContain("&lt;script&gt;");
  });
});
