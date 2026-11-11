// SPDX-License-Identifier: GPL-3.0-or-later
import { useI18n, type MessageKey } from "../i18n";
import { cx } from "./cx";
import { Icon, type IconName } from "./Icon";
import styles from "./Badge.module.css";

// Every badge carries an icon and a text label, so meaning never depends on color alone.

export const severities = ["critical", "high", "medium", "low", "info"] as const;
export type Severity = (typeof severities)[number];

const severityIcon: Record<Severity, IconName> = {
  critical: "severityCritical",
  high: "severityHigh",
  medium: "severityMedium",
  low: "severityLow",
  info: "severityInfo",
};

export function SeverityBadge({ severity, className }: { severity: Severity; className?: string }) {
  const { t } = useI18n();
  return (
    <span className={cx(styles.badge, styles[`sev-${severity}`], className)}>
      <Icon name={severityIcon[severity]} size={14} />
      {t(`severity.${severity}`)}
    </span>
  );
}

export const runStatuses = ["queued", "running", "succeeded", "failed", "cancelled", "attention"] as const;
export type RunStatus = (typeof runStatuses)[number];

const statusIcon: Record<RunStatus, IconName> = {
  queued: "statusQueued",
  running: "statusRunning",
  succeeded: "statusSucceeded",
  failed: "statusFailed",
  cancelled: "statusCancelled",
  attention: "statusAttention",
};

export function StatusBadge({ status, className }: { status: RunStatus; className?: string }) {
  const { t } = useI18n();
  const key: MessageKey = `status.${status}`;
  return (
    <span className={cx(styles.badge, styles[`status-${status}`], className)}>
      <Icon name={statusIcon[status]} size={14} className={status === "running" ? styles.spin : undefined} />
      {t(key)}
    </span>
  );
}

export type JobMode = "preview" | "post";

/**
 * UI-0008 section 5: the preview/post distinction is visible wherever a job or
 * run is shown. Post is the loud one: it writes to GitHub under the user's name.
 */
export function ModeBadge({ mode, className }: { mode: JobMode; className?: string }) {
  const { t } = useI18n();
  return (
    <span className={cx(styles.badge, styles[`mode-${mode}`], className)}>
      <Icon name={mode === "preview" ? "eye" : "send"} size={14} />
      {t(`mode.${mode}`)}
    </span>
  );
}
