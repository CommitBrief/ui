// SPDX-License-Identifier: GPL-3.0-or-later
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { I18nContext, translate, type Locale } from "../../i18n";
import { ScheduleForm, type ScheduleFormProps } from "./ScheduleForm";
import { emptyDraft } from "./schedule";

function render(props: Partial<ScheduleFormProps>, locale: Locale = "en"): string {
  const full: ScheduleFormProps = {
    job: null,
    repos: [{ id: 1, owner: "acme", name: "api", localPath: "/src/api", enabled: true, createdAt: "2026-11-01T00:00:00Z" }] as unknown as ScheduleFormProps["repos"],
    posting: true,
    cliUnknown: false,
    onClose: () => undefined,
    onSaved: () => undefined,
    ...props,
  };
  return renderToStaticMarkup(
    <I18nContext.Provider value={{ locale, setLocale: () => undefined, t: (key, params) => translate(locale, key, params) }}>
      <ScheduleForm {...full} />
    </I18nContext.Provider>,
  );
}

/** The opening tag of the submit button that carries the label. */
function saveButton(html: string, label: string): string {
  const match = new RegExp(`<button[^>]*form="schedule-form"[^>]*>(?:(?!</button>).)*${label}`).exec(html);
  expect(match).not.toBeNull();
  return match?.[0] ?? "";
}

/** The opening tag of the mode radio with this value. */
function radio(html: string, value: string): string {
  const tag = (html.match(/<input[^>]*type="radio"[^>]*>/g) ?? []).find((candidate) => candidate.includes(`value="${value}"`));
  expect(tag).toBeDefined();
  return tag ?? "";
}

const draft = { ...emptyDraft("UTC"), name: "Team bot", targets: [1] };

describe("ScheduleForm save button", () => {
  it("is enabled for a preview schedule, the default", () => {
    const html = render({ initialDraft: draft });
    expect(saveButton(html, "Save schedule")).not.toContain("disabled");
    expect(radio(html, "preview")).toContain("checked");
    expect(radio(html, "post")).not.toContain("disabled");
  });

  it("is disabled when post is chosen but the consent was not given", () => {
    const html = render({ initialDraft: { ...draft, mode: "post", consented: false } });
    expect(saveButton(html, "Save schedule")).toContain("disabled");
  });

  it("is enabled once the consent is given", () => {
    const html = render({ initialDraft: { ...draft, mode: "post", consented: true } });
    expect(saveButton(html, "Save schedule")).not.toContain("disabled");
    expect(html).toContain("You accepted that this schedule posts reviews under your GitHub account.");
  });

  it("is enabled for a stored post job, whose consent is already recorded", () => {
    const job = {
      id: 3,
      name: "Team bot",
      mode: "post",
      enabled: true,
      targets: [1],
      includeDrafts: false,
      postConsentAt: "2026-11-01T10:00:00Z",
      windows: [{ days: 31, start: 540, end: 1080, every: 15, tz: "UTC" }],
      nextDue: null,
      attentionCode: "",
      attentionAt: null,
      createdAt: "2026-11-01T09:00:00Z",
    } as unknown as ScheduleFormProps["job"];
    const html = render({ job });
    expect(saveButton(html, "Save schedule")).not.toContain("disabled");
  });
});

describe("ScheduleForm with a CLI that cannot post", () => {
  it("turns the post option off and offers the CLI update", () => {
    const html = render({ posting: false, initialDraft: draft });
    expect(radio(html, "post")).toContain("disabled");
    expect(html).toContain("Posting needs a newer CLI");
    expect(html).toContain("1.22.0");
    expect(html).toContain("Update the CLI");
    expect(html).toContain('href="#/settings"');
    // Preview still saves.
    expect(saveButton(html, "Save schedule")).not.toContain("disabled");
  });

  it("keeps a job that is already in post mode from saving until it goes back to preview", () => {
    const html = render({ posting: false, initialDraft: { ...draft, mode: "post", consented: true } });
    expect(saveButton(html, "Save schedule")).toContain("disabled");
    expect(html).toContain("Switch to preview, or update the CLI, to save.");
  });

  it("says so, in Turkish too, when the CLI could not be checked", () => {
    expect(render({ posting: false, cliUnknown: true, initialDraft: draft })).toContain("CLI could not be checked");
    expect(render({ posting: false, initialDraft: draft }, "tr")).toContain("CLI&#x27;ı güncelle");
  });
});

describe("ScheduleForm with no repos", () => {
  it("points at the Repos screen and cannot be saved", () => {
    const html = render({ repos: [], initialDraft: draft });
    expect(html).toContain("There are no repos yet.");
    expect(saveButton(html, "Save schedule")).toContain("disabled");
  });
});
