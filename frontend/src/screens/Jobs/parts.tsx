// SPDX-License-Identifier: GPL-3.0-or-later
import { useEffect, useState, type ReactNode } from "react";
import { describeJobError, nextStepFor } from "../../api/jobsErrors";
import { Button, Icon, ModeBadge, useToast, type IconName } from "../../components";
import { useI18n, type MessageKey } from "../../i18n";
import { copyText } from "../Settings/clipboard";
import type { RemoteView } from "./findings";
import type { JobMode } from "./runView";
import styles from "./Jobs.module.css";

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
  icon,
  title,
  children,
  action,
  live,
}: {
  tone: NoticeTone;
  /** Replaces the tone's default icon. */
  icon?: IconName;
  title?: string;
  children?: ReactNode;
  action?: ReactNode;
  /** `alert` for a failure that just happened; omit for state that was there at load. */
  live?: "alert" | "status";
}) {
  return (
    <div className={`${styles.notice} ${styles[`notice-${tone}`]}`} role={live}>
      <Icon name={icon ?? noticeIcons[tone]} size={16} className={styles.noticeIcon} />
      <div className={styles.noticeBody}>
        {title ? <p className={styles.noticeTitle}>{title}</p> : null}
        {children ? <div className={styles.noticeText}>{children}</div> : null}
        {action ? <div>{action}</div> : null}
      </div>
    </div>
  );
}

/** A command in a scrollable monospace box, with a button that copies it. The app never runs it. */
export function CopyCommand({ text }: { text: string }) {
  const { t } = useI18n();
  const toast = useToast();
  const copy = async () => {
    const ok = await copyText(text);
    toast(ok ? { kind: "success", title: t("jobs.copied") } : { kind: "error", title: t("jobs.copyFailed") });
  };
  return (
    <div className={styles.command}>
      {/* A focusable scroll box, so a long command is reachable by keyboard. */}
      <code className={styles.commandText} role="region" aria-label={t("jobs.command.label")} tabIndex={0}>
        {text}
      </code>
      <Button size="sm" icon="copy" onClick={() => void copy()}>
        {t("jobs.copy")}
      </Button>
    </div>
  );
}

/**
 * The failure of a run or task in the user's words: what happened and what to do
 * about it (a sentence, a link to the screen that fixes it, a command to run).
 * An `attention` status always carries an action; a plain failure may only say
 * what went wrong.
 */
export function FailureNotice({ code, attention, compact = false }: { code: string; attention: boolean; compact?: boolean }) {
  const { t } = useI18n();
  const next = nextStepFor(code);
  return (
    <Notice
      tone={attention ? "warning" : "error"}
      title={compact ? undefined : t(attention ? "jobs.failure.attention" : "jobs.failure.failed")}
    >
      <span>{describeJobError({ code }, t)}</span>
      {next?.hint ? <span> {t(next.hint)}</span> : null}
      {next?.command ? <CopyCommand text={next.command} /> : null}
      {next?.link ? (
        <p className={styles.noticeLinkRow}>
          <a className={styles.link} href={next.link.href}>
            {t(next.link.label)}
          </a>
        </p>
      ) : null}
    </Notice>
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

/** The current time, refreshed every second while `active` (slowly otherwise); one source for every live duration on screen. */
export function useNow(active: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    // Every second while something runs; every 30 s otherwise, only to keep "5 minutes ago" honest.
    const timer = window.setInterval(() => setNow(Date.now()), active ? 1000 : 30_000);
    return () => window.clearInterval(timer);
  }, [active]);
  return now;
}

/** The mode badge, with a neutral "unknown" for a run whose record does not say (never guessed as preview). */
export function ModeLabel({ mode }: { mode: JobMode }) {
  const { t } = useI18n();
  if (mode === "unknown") {
    return (
      <span className={styles.modeUnknown}>
        <Icon name="severityInfo" size={14} />
        {t("jobs.mode.unknown")}
      </span>
    );
  }
  return <ModeBadge mode={mode} />;
}

const remoteStatusKey: Record<string, MessageKey> = {
  submitted: "jobs.remote.submitted",
  no_new_commits: "jobs.remote.no_new_commits",
  degraded: "jobs.remote.degraded",
  too_volatile: "jobs.remote.too_volatile",
  failed: "jobs.remote.failed",
};

export interface PostItem {
  title: string;
  remote: RemoteView | null;
  active: boolean;
}

/**
 * What a posting task did, said only as far as its report says it. "Posted" is
 * claimed only for a submitted review; a task without a report (it failed
 * before writing one) is never reported as "nothing was posted", because the
 * record cannot say that.
 */
export function postOutcome(item: PostItem, t: ReturnType<typeof useI18n>["t"]): string {
  const { remote } = item;
  if (remote === null) return t(item.active ? "jobs.mode.post.pending" : "jobs.mode.post.unrecorded");
  const reason = t(remoteStatusKey[remote.status] ?? "jobs.remote.failed");
  if (remote.status === "submitted") {
    let text = remote.hasSync
      ? t("jobs.mode.post.done", { posted: remote.posted, updated: remote.updated, unchanged: remote.unchanged, resolved: remote.resolved })
      : t("jobs.mode.post.submitted");
    if (remote.verdict !== "") text += ` ${t("jobs.mode.post.verdict", { verdict: remote.verdict })}`;
    if (remote.postFailed > 0) text += ` ${t("jobs.mode.post.failed", { count: remote.postFailed })}`;
    return text;
  }
  if (remote.status === "no_new_commits" || remote.status === "degraded" || remote.status === "too_volatile") {
    return t("jobs.mode.post.notPosted", { reason });
  }
  return t("jobs.mode.post.incomplete", { reason });
}

/** UI-0008 section 5: what the run did and did not do on GitHub, said outright, and "unknown" when the record cannot say. */
export function ModeNotice({ mode, items }: { mode: JobMode; items: readonly PostItem[] }) {
  const { t } = useI18n();
  if (mode === "preview") {
    return (
      <Notice tone="info" icon="eye" title={t("jobs.mode.preview.title")}>
        {t("jobs.mode.preview.text")}
      </Notice>
    );
  }
  if (mode === "unknown") {
    return (
      <Notice tone="warning" icon="severityInfo" title={t("jobs.mode.unknown.title")}>
        {t("jobs.mode.unknown.text")}
      </Notice>
    );
  }
  return (
    <Notice tone="warning" icon="send" title={t("jobs.mode.post.title")}>
      {items.length === 1 ? (
        <span>{postOutcome(items[0] as PostItem, t)}</span>
      ) : (
        <ul className={styles.outcomeList}>
          {items.map((item, index) => (
            <li key={index}>
              <span className={styles.mono}>{item.title}</span>: {postOutcome(item, t)}
            </li>
          ))}
        </ul>
      )}
    </Notice>
  );
}
