// SPDX-License-Identifier: GPL-3.0-or-later
import type { TrackedPRView } from "../../api/schedules";
import { Icon, ModeBadge, Table, TBody, Td, Th, THead, Tr } from "../../components";
import { useI18n } from "../../i18n";
import { formatDateTime, formatRelative, parseTime, shortOid } from "../Jobs/format";
import styles from "./Schedules.module.css";

export interface TrackedPRsProps {
  rows: readonly TrackedPRView[];
  now: number;
}

/**
 * The open pull requests the bot follows, one row per schedule: `repo#N`, the head
 * it last reviewed (empty until the first review), when it last ran on it and that
 * the pull request is open. A pull request leaves the list when it is closed or
 * merged. Untrusted text is not shown (no PR titles), only numbers, commit ids and
 * the user's own schedule and repository names, as plain text.
 */
export function TrackedPRs({ rows, now }: TrackedPRsProps) {
  const { t, locale } = useI18n();
  return (
    <section className={styles.panel} aria-labelledby="tracked-title">
      <div>
        <h2 id="tracked-title" className={styles.panelTitle}>
          {t("schedules.tracked.title")}
        </h2>
        <p className={styles.panelDescription}>{t("schedules.tracked.description")}</p>
      </div>
      {rows.length === 0 ? (
        <p className={styles.muted}>{t("schedules.tracked.empty")}</p>
      ) : (
        <Table label={t("schedules.tracked.label")}>
          <THead>
            <Tr>
              <Th>{t("schedules.tracked.col.pr")}</Th>
              <Th>{t("schedules.tracked.col.head")}</Th>
              <Th>{t("schedules.tracked.col.lastRun")}</Th>
              <Th>{t("schedules.tracked.col.status")}</Th>
              <Th>{t("schedules.tracked.col.schedule")}</Th>
            </Tr>
          </THead>
          <TBody>
            {rows.map((row) => {
              const slash = row.repository.indexOf("/");
              const owner = slash > 0 ? row.repository.slice(0, slash) : "";
              const name = slash > 0 ? row.repository.slice(slash + 1) : row.repository;
              const lastRun = parseTime(row.lastRunAt);
              return (
                <Tr key={`${row.jobId}:${row.repositoryId}#${row.number}`}>
                  <Td>
                    <span className={styles.prTitle}>
                      {owner !== "" ? <span className={styles.prOwner}>{owner}/</span> : null}
                      {name}#{row.number}
                    </span>
                  </Td>
                  <Td>
                    {row.lastHeadOid !== "" ? (
                      <span className={`${styles.mono} ${styles.nowrap}`}>{shortOid(row.lastHeadOid)}</span>
                    ) : (
                      <span className={styles.muted}>{t("schedules.tracked.notReviewed")}</span>
                    )}
                  </Td>
                  <Td>
                    {lastRun !== null ? (
                      <time className={styles.nowrap} dateTime={new Date(lastRun).toISOString()} title={formatDateTime(lastRun, locale)}>
                        {formatRelative(lastRun, now, locale)}
                      </time>
                    ) : (
                      "—"
                    )}
                  </Td>
                  <Td>
                    <span className={`${styles.pill} ${styles["pill-idle"]}`}>
                      <Icon name="repos" size={14} />
                      {t("schedules.tracked.state.open")}
                    </span>
                  </Td>
                  <Td>
                    <span className={styles.cellStack}>
                      <span>{row.jobName}</span>
                      <ModeBadge mode={row.mode === "post" ? "post" : "preview"} />
                      {!row.jobEnabled ? <span className={styles.muted}>({t("schedules.enabled.off")})</span> : null}
                    </span>
                  </Td>
                </Tr>
              );
            })}
          </TBody>
        </Table>
      )}
    </section>
  );
}
