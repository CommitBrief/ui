// SPDX-License-Identifier: GPL-3.0-or-later
import { useEffect, useRef } from "react";
import { EmptyState, PageHeader, Sidebar, type SidebarItem } from "../components";
import { useI18n } from "../i18n";
import { Preferences } from "./Preferences";
import { StatusBar } from "./StatusBar";
import { usePath } from "./router";
import { resolveRoute, routes } from "./routes";
import styles from "./Shell.module.css";

const PRODUCT_NAME = "CommitBrief UI"; // brand name, not translated

export function Shell() {
  const { t } = useI18n();
  const path = usePath();
  const route = resolveRoute(path);
  const mainRef = useRef<HTMLElement>(null);
  const previousPath = useRef(path);

  const toItem = (r: (typeof routes)[number]): SidebarItem => ({ path: r.path, label: t(r.labelKey), icon: r.icon });
  const primary = routes.filter((r) => r.group === "primary").map(toItem);
  const secondary = routes.filter((r) => r.group === "secondary").map(toItem);

  const screenTitle = route ? (route.labelKey ? t(route.labelKey) : null) : t("route.notFound.title");
  useEffect(() => {
    document.title = screenTitle ? `${screenTitle} · ${PRODUCT_NAME}` : PRODUCT_NAME;
  }, [screenTitle]);

  // After a navigation, move focus to the new screen's heading so keyboard and
  // screen-reader users land on the content, not on the link they just used.
  useEffect(() => {
    if (previousPath.current === path) return;
    previousPath.current = path;
    const main = mainRef.current;
    const heading = main?.querySelector<HTMLElement>("[data-page-heading]");
    (heading ?? main)?.focus();
  }, [path]);

  return (
    <div className={styles.shell}>
      {/* href="#main" would collide with the hash router, so the click is handled here. */}
      <a
        className={styles.skip}
        href="#main"
        onClick={(event) => {
          event.preventDefault();
          mainRef.current?.focus();
        }}
      >
        {t("nav.skip")}
      </a>
      <Sidebar
        label={t("nav.label")}
        productName={PRODUCT_NAME}
        items={primary}
        secondaryItems={secondary}
        activePath={route?.path ?? null}
        footer={<Preferences />}
      />
      <div className={styles.column}>
        <main id="main" ref={mainRef} className={styles.main} tabIndex={-1}>
          <div className={styles.content}>
            {route ? (
              route.element
            ) : (
              <>
                <PageHeader title={t("route.notFound.title")} />
                <EmptyState title={t("route.notFound.message")} />
              </>
            )}
          </div>
        </main>
        <StatusBar />
      </div>
    </div>
  );
}
