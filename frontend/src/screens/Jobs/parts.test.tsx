// SPDX-License-Identifier: GPL-3.0-or-later
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { I18nContext, translate, type Locale } from "../../i18n";
import { parseReport } from "./findings";
import { LogText } from "./LogPanel";
import { ModeLabel, ModeNotice, postOutcome, type PostItem } from "./parts";
import { splitLog } from "./logTail";

function render(element: React.ReactElement, locale: Locale = "en"): string {
  return renderToStaticMarkup(
    <I18nContext.Provider value={{ locale, setLocale: () => undefined, t: (key, params) => translate(locale, key, params) }}>
      {element}
    </I18nContext.Provider>,
  );
}
const t = (key: Parameters<typeof translate>[1], params?: Record<string, string | number>) => translate("en", key, params);
const remote = (r: Record<string, unknown>) => parseReport({ meta: {}, remote: r })?.remote ?? null;
const item = (r: PostItem["remote"], active = false): PostItem => ({ title: "acme/api#1", remote: r, active });

describe("LogText: the log is plain text", () => {
  it("never turns log lines into elements, even ones that look like markup or escape codes", () => {
    const lines = splitLog('<script>window.pwned = 1</script>\n<img src=x onerror=alert(1)>\n\u001b[31m<b>red</b>\u001b[0m\n');
    const html = render(<LogText lines={lines} trimmed={false} />);
    expect(html).not.toContain("<script");
    expect(html).not.toContain("<img");
    expect(html).not.toContain("<b>");
    expect(html).toContain("&lt;script&gt;window.pwned = 1&lt;/script&gt;");
    expect(html).toContain("&lt;b&gt;red&lt;/b&gt;");
    expect(html).not.toContain("\u001b");
  });

  it("says when older lines were left out", () => {
    expect(render(<LogText lines={["a"]} trimmed />)).toContain("Only the newest 5000 lines");
  });
});

describe("mode wording (UI-0008 section 5)", () => {
  it("shows an unknown mode as unknown, not as a preview", () => {
    const badge = render(<ModeLabel mode="unknown" />);
    expect(badge).toContain("Mode unknown");
    expect(badge).not.toContain("Preview");
    const notice = render(<ModeNotice mode="unknown" items={[]} />);
    expect(notice).toContain("Mode not recorded");
    expect(notice).not.toContain("Nothing was posted to GitHub");
  });

  it("only a preview says nothing was posted", () => {
    expect(render(<ModeNotice mode="preview" items={[]} />)).toContain("Nothing was posted to GitHub.");
    expect(render(<ModeNotice mode="post" items={[item(null)]} />)).not.toContain("Nothing was posted to GitHub.");
  });

  it("claims a post only for a submitted review", () => {
    const submitted = postOutcome(item(remote({ status: "submitted", verdict: "comment", sync: { posted: 2, updated: 1 } })), t);
    expect(submitted).toContain("Posted to GitHub under your account: 2 new, 1 updated");
    expect(submitted).toContain("Verdict: comment");
    for (const status of ["no_new_commits", "degraded", "too_volatile"]) {
      const text = postOutcome(item(remote({ status })), t);
      expect(text).toContain("Nothing was posted to GitHub (");
      expect(text).not.toContain("Posted to GitHub under");
    }
    const failed = postOutcome(item(remote({ status: "failed" })), t);
    expect(failed).toContain("Posting did not finish");
    expect(failed).not.toContain("Posted to GitHub under");
  });

  it("does not claim 'nothing was posted' for a posting task that left no report", () => {
    const text = postOutcome(item(null), t);
    expect(text).toContain("no result was recorded");
    expect(text).not.toContain("Nothing was posted");
    expect(postOutcome(item(null, true), t)).toContain("When it finishes");
  });
});
