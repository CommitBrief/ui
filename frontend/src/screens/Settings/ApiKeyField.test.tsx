// SPDX-License-Identifier: GPL-3.0-or-later
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { I18nContext, translate, type Locale } from "../../i18n";
import { ApiKeyField } from "./ApiKeyField";

function render(locale: Locale, element: React.ReactElement): string {
  return renderToStaticMarkup(
    <I18nContext.Provider value={{ locale, setLocale: () => undefined, t: (key, params) => translate(locale, key, params) }}>
      {element}
    </I18nContext.Provider>,
  );
}

describe("ApiKeyField", () => {
  it("is a write-only password field that starts empty even when a key is stored", () => {
    const html = render("en", <ApiKeyField stored canWrite value="" onChange={() => undefined} />);
    expect(html).toContain('type="password"');
    expect(html).toContain('value=""');
    expect(html).toContain('autoComplete="new-password"');
    // It says a key is stored in words, and never shows one.
    expect(html).toContain("Stored key is hidden");
    expect(html).toContain("The stored key is never shown");
    expect(html).not.toMatch(/value="[^"]/);
  });

  it("shows only what was typed in this session, and nothing about a stored key's content", () => {
    const html = render("en", <ApiKeyField stored canWrite value="typed-now" onChange={() => undefined} />);
    expect(html).toContain('value="typed-now"');
    expect(html).not.toContain("sk-");
  });

  it("says when no key is stored, and has no placeholder that suggests one", () => {
    const html = render("en", <ApiKeyField stored={false} canWrite value="" onChange={() => undefined} />);
    expect(html).toContain("Write-only");
    expect(html).not.toContain("placeholder=");
  });

  it("is disabled, with the reason, on a CLI that cannot take a key on stdin", () => {
    const html = render("en", <ApiKeyField stored={false} canWrite={false} value="" onChange={() => undefined} />);
    expect(html).toContain("disabled");
    expect(html).toContain("Writing keys needs commitbrief 1.22.0 or later");
  });

  it("changes its words with the interface language", () => {
    const en = render("en", <ApiKeyField stored canWrite value="" onChange={() => undefined} />);
    const tr = render("tr", <ApiKeyField stored canWrite value="" onChange={() => undefined} />);
    expect(en).toContain("API key");
    expect(tr).toContain("API anahtarı");
    expect(tr).toContain("Kayıtlı anahtar gizli");
    expect(tr).not.toContain("Stored key is hidden");
    expect(en).not.toContain("Kayıtlı anahtar gizli");
  });
});

describe("language switch", () => {
  it("changes the texts of the Settings screen", () => {
    for (const key of ["settings.cli.version", "settings.providers.use", "settings.config.save", "settings.app.language", "settings.app.save"] as const) {
      expect(translate("tr", key)).not.toBe(translate("en", key));
    }
    expect(translate("tr", "settings.cli.cap.no", { min: "1.22.0" })).toBe("commitbrief 1.22.0 veya üstü gerekir");
  });
});
