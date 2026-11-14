// SPDX-License-Identifier: GPL-3.0-or-later
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { I18nContext, translate, type Locale } from "../../i18n";
import { FindingsTable } from "./FindingsTable";
import { parseFindings, parseReport } from "./findings";

function render(element: React.ReactElement, locale: Locale = "en"): string {
  return renderToStaticMarkup(
    <I18nContext.Provider value={{ locale, setLocale: () => undefined, t: (key, params) => translate(locale, key, params) }}>
      {element}
    </I18nContext.Provider>,
  );
}

// What a hostile pull request can make a model write into a finding.
const script = "<script>window.pwned = 1</script>";
const img = "<img src=x onerror=alert(1)>";
const link = "[click me](javascript:alert(1)) <a href=\"https://evil.example\">x</a>";
const breakout = '" onmouseover="alert(1)';

function hostileFindings() {
  return parseFindings([
    {
      severity: "high",
      file: `src/${breakout}.go`,
      line: 12,
      title: `Unsafe ${script}`,
      description: `${img} ${link}`,
      suggestion: `Use ${script} instead`,
      snippet: `+${img}\n-${script}`,
      ruleId: `rule-${img}`,
      fingerprint: "fp-1",
    },
  ]);
}

describe("FindingsTable: model text is data, never markup", () => {
  it("puts <script> and <img onerror> into the page as escaped text, not as elements", () => {
    const html = render(<FindingsTable findings={hostileFindings()} report={null} />);
    expect(html).not.toContain("<script");
    expect(html).not.toContain("<img");
    expect(html).not.toContain("<a ");
    // The text itself is still there, escaped, so the user can read what the model wrote.
    expect(html).toContain("&lt;script&gt;window.pwned = 1&lt;/script&gt;");
    expect(html).toContain("&lt;img src=x onerror=alert(1)&gt;");
  });

  it("does not turn Markdown or HTML links into links", () => {
    const html = render(<FindingsTable findings={hostileFindings()} report={null} />);
    expect(html).not.toContain("<a ");
    expect(html).not.toContain('href="javascript:');
    expect(html).not.toContain('href="https://evil.example"');
    expect(html).toContain("[click me](javascript:alert(1))");
  });

  it("cannot break out of an attribute: untrusted text is never placed in one", () => {
    const html = render(<FindingsTable findings={hostileFindings()} report={null} />);
    expect(html).not.toContain('" onmouseover="');
    expect(html).toContain("&quot; onmouseover=&quot;alert(1)");
  });

  it("shows the raw model answer of a degraded review as text too", () => {
    const report = parseReport({
      content: `${script}${img}`,
      findings: [],
      meta: { degradeReason: "unparseable", provider: "anthropic", model: "m" },
    });
    const html = render(<FindingsTable findings={[]} report={report} />);
    expect(html).not.toContain("<script");
    expect(html).not.toContain("<img");
    expect(html).toContain("&lt;script&gt;");
    // An empty list of a degraded review is not "no findings".
    expect(html).not.toContain(translate("en", "jobs.findings.none.title"));
    expect(html).toContain(translate("en", "jobs.findings.degraded.title"));
  });

  it("says so when there are no findings and the review was fine", () => {
    const html = render(<FindingsTable findings={[]} report={parseReport({ content: "", findings: [], meta: {} })} />);
    expect(html).toContain(translate("en", "jobs.findings.none.title"));
  });

  it("lists the most severe finding first and offers a filter only for severities that occur", () => {
    const findings = parseFindings([
      { severity: "low", file: "b.go", line: 1, title: "second", description: "d" },
      { severity: "critical", file: "a.go", line: 9, title: "first", description: "d" },
    ]);
    const html = render(<FindingsTable findings={findings} report={null} />);
    expect(html.indexOf("first")).toBeLessThan(html.indexOf("second"));
    expect(html).toContain("Critical (1)");
    expect(html).toContain("Low (1)");
    expect(html).not.toContain("Medium (");
  });

  it("words the table in the interface language", () => {
    const html = render(<FindingsTable findings={hostileFindings()} report={null} />, "tr");
    expect(html).toContain(`>${translate("tr", "jobs.findings.col.description")}<`);
    expect(html).not.toContain(`>${translate("en", "jobs.findings.col.description")}<`);
  });
});
