// SPDX-License-Identifier: GPL-3.0-or-later
import type { ReactNode } from "react";
import { describeSettingsError, type SettingsError } from "../../api/settings";
import { Button, Icon, useToast, type IconName } from "../../components";
import { useI18n } from "../../i18n";
import { copyText } from "./clipboard";
import styles from "./Settings.module.css";

/** A settings section: a labelled card with a heading, a short description and optional header actions. */
export function Section({
  anchor,
  title,
  description,
  actions,
  children,
}: {
  /** Element id of the section; the in-page navigation scrolls to it. */
  anchor: string;
  title: string;
  description?: string;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section id={anchor} className={styles.card} aria-labelledby={`${anchor}-title`}>
      <div className={styles.cardHead}>
        <div className={styles.cardText}>
          <h2 id={`${anchor}-title`} className={styles.cardTitle} tabIndex={-1}>
            {title}
          </h2>
          {description ? <p className={styles.cardDescription}>{description}</p> : null}
        </div>
        {actions ? <div className={styles.cardActions}>{actions}</div> : null}
      </div>
      {children}
    </section>
  );
}

export type NoticeTone = "info" | "warning" | "error" | "success";

const noticeIcons: Record<NoticeTone, IconName> = {
  info: "severityInfo",
  warning: "statusAttention",
  error: "statusFailed",
  success: "statusSucceeded",
};

/** A message with an icon and, optionally, a bold title: the meaning never rests on color alone. */
export function Notice({
  tone,
  title,
  children,
  action,
  live,
}: {
  tone: NoticeTone;
  title?: string;
  children?: ReactNode;
  action?: ReactNode;
  /** `alert` for a failure that just happened; omit for state that was there at load. */
  live?: "alert" | "status";
}) {
  return (
    <div className={`${styles.notice} ${styles[`notice-${tone}`]}`} role={live}>
      <Icon name={noticeIcons[tone]} size={16} className={styles.noticeIcon} />
      <div className={styles.noticeBody}>
        {title ? <p className={styles.noticeTitle}>{title}</p> : null}
        {children ? <div>{children}</div> : null}
        {action ? <div className={styles.noticeAction}>{action}</div> : null}
      </div>
    </div>
  );
}

/** The failure state of a section that could not load, with a retry. */
export function LoadFailed({ error, onRetry }: { error: SettingsError; onRetry: () => void }) {
  const { t } = useI18n();
  return (
    <Notice
      tone="error"
      title={t("settings.loadError")}
      live="alert"
      action={<Button onClick={onRetry}>{t("settings.retry")}</Button>}
    >
      {describeSettingsError(error, t)}
    </Notice>
  );
}

export function Loading() {
  const { t } = useI18n();
  return (
    <p className={styles.muted} role="status">
      {t("common.loading")}
    </p>
  );
}

export type PillTone = "ok" | "warning" | "error" | "neutral";

const pillIcons: Record<PillTone, IconName> = {
  ok: "statusSucceeded",
  warning: "statusAttention",
  error: "statusFailed",
  neutral: "severityInfo",
};

/** A status pill: an icon and a word, so the status is readable without color. */
export function Pill({ tone, children }: { tone: PillTone; children: ReactNode }) {
  return (
    <span className={`${styles.pill} ${styles[`pill-${tone}`]}`}>
      <Icon name={pillIcons[tone]} size={14} />
      {children}
    </span>
  );
}

/** A command or path in a scrollable monospace box, with a button that copies it. */
export function CopyCommand({ text, label, copyLabel }: { text: string; label: string; copyLabel?: string }) {
  const { t } = useI18n();
  const toast = useToast();
  const copy = async () => {
    const ok = await copyText(text);
    toast(ok ? { kind: "success", title: t("settings.copied") } : { kind: "error", title: t("settings.copyFailed") });
  };
  return (
    <div className={styles.command}>
      {/* A focusable scroll box, so a long command or path is reachable by keyboard. */}
      <code className={styles.commandText} role="region" aria-label={label} tabIndex={0}>
        {text}
      </code>
      <Button size="sm" icon="copy" onClick={() => void copy()}>
        {copyLabel ?? t("settings.copy")}
      </Button>
    </div>
  );
}

/** Label/value rows of a card. */
export function Facts({ rows }: { rows: readonly { label: string; value: ReactNode }[] }) {
  return (
    <dl className={styles.facts}>
      {rows.map((row) => (
        <div key={row.label} className={styles.fact}>
          <dt>{row.label}</dt>
          <dd>{row.value}</dd>
        </div>
      ))}
    </dl>
  );
}
