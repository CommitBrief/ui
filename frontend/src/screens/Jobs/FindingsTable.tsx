// SPDX-License-Identifier: GPL-3.0-or-later
import { useMemo, useState } from "react";
import { EmptyState, SeverityBadge, Table, TBody, Td, Th, THead, Tr, severities, type Severity } from "../../components";
import { useI18n } from "../../i18n";
import { countBySeverity, findingLocation, snippetLines, sortFindings, type Finding, type ReportView } from "./findings";
import { Notice } from "./parts";
import styles from "./Jobs.module.css";

export interface FindingsTableProps {
  findings: readonly Finding[];
  /** The stored report, for the degraded state; null when there is none. */
  report: ReportView | null;
}

type Filter = Severity | "all";

/**
 * The findings of one task, most severe first.
 *
 * Every text here is model output and may be shaped by the code under review, so
 * it is only ever placed into the page as text: no HTML, no Markdown, no links.
 * (React escapes text children; nothing in this file bypasses that.)
 */
export function FindingsTable({ findings, report }: FindingsTableProps) {
  const { t } = useI18n();
  const [filter, setFilter] = useState<Filter>("all");
  const sorted = useMemo(() => sortFindings(findings), [findings]);
  const counts = useMemo(() => countBySeverity(findings), [findings]);
  const visible = filter === "all" ? sorted : sorted.filter((finding) => finding.severity === filter);
  const degraded = report !== null && report.degradeReason !== "";

  if (findings.length === 0) {
    return (
      <div className={styles.findingsBody}>
        {degraded ? (
          <Notice tone="warning" title={t("jobs.findings.degraded.title")}>
            {t("jobs.findings.degraded.text")}
          </Notice>
        ) : (
          <EmptyState icon="statusSucceeded" title={t("jobs.findings.none.title")} description={t("jobs.findings.none.description")} />
        )}
        {degraded && report.content !== "" ? <RawAnswer content={report.content} /> : null}
      </div>
    );
  }

  return (
    <div className={styles.findingsBody}>
      {degraded ? (
        <Notice tone="warning" title={t("jobs.findings.degraded.title")}>
          {t("jobs.findings.degraded.partial")}
        </Notice>
      ) : null}
      <div className={styles.filters} role="group" aria-label={t("jobs.findings.filter")}>
        <button type="button" className={styles.chip} aria-pressed={filter === "all"} onClick={() => setFilter("all")}>
          {t("jobs.findings.all", { count: findings.length })}
        </button>
        {severities
          .filter((severity) => counts[severity] > 0)
          .map((severity) => (
            <button
              key={severity}
              type="button"
              className={styles.chip}
              aria-pressed={filter === severity}
              onClick={() => setFilter(severity)}
            >
              {t(`severity.${severity}`)} ({counts[severity]})
            </button>
          ))}
      </div>
      <p className="sr-only" role="status">
        {t("jobs.findings.shown", { shown: visible.length, total: findings.length })}
      </p>
      {/* Wide: a table. Narrow: one card per finding, so no reading needs sideways scrolling.
          The container query on the body shows exactly one of the two (display:none leaves the other out of the accessibility tree). */}
      <div className={styles.findingsWide}>
        <Table label={t("jobs.findings.label")} className={styles.findingsTable}>
          <THead>
            <Tr>
              <Th>{t("jobs.findings.col.severity")}</Th>
              <Th>{t("jobs.findings.col.location")}</Th>
              <Th>{t("jobs.findings.col.title")}</Th>
              <Th>{t("jobs.findings.col.description")}</Th>
            </Tr>
          </THead>
          <TBody>
            {visible.map((finding, index) => (
              <Tr key={`${finding.fingerprint}:${index}`} className={styles.findingRow}>
                <Td className={styles.colSeverity}>
                  <SeverityBadge severity={finding.severity} />
                </Td>
                <Td className={styles.colLocation}>
                  {finding.file !== "" ? (
                    <code className={styles.location}>{findingLocation(finding)}</code>
                  ) : (
                    <span className={styles.muted}>{t("jobs.findings.noLocation")}</span>
                  )}
                </Td>
                <Td className={styles.colTitle}>
                  <span className={styles.findingTitle}>{finding.title !== "" ? finding.title : t("jobs.findings.untitled")}</span>
                  {finding.ruleId !== "" ? <code className={styles.ruleId}>{finding.ruleId}</code> : null}
                </Td>
                <Td className={styles.colDescription}>
                  {finding.description !== "" ? <p className={styles.findingText}>{finding.description}</p> : null}
                  {finding.suggestion !== "" || finding.snippet !== "" ? <Extras finding={finding} /> : null}
                </Td>
              </Tr>
            ))}
          </TBody>
        </Table>
      </div>
      <ul className={styles.findingsNarrow} aria-label={t("jobs.findings.label")}>
        {visible.map((finding, index) => (
          <li key={`${finding.fingerprint}:${index}`} className={styles.findingCard}>
            <div className={styles.findingCardHead}>
              <SeverityBadge severity={finding.severity} />
              {finding.file !== "" ? (
                <code className={styles.location}>{findingLocation(finding)}</code>
              ) : (
                <span className={styles.muted}>{t("jobs.findings.noLocation")}</span>
              )}
            </div>
            <p className={styles.findingTitle}>{finding.title !== "" ? finding.title : t("jobs.findings.untitled")}</p>
            {finding.ruleId !== "" ? <code className={styles.ruleId}>{finding.ruleId}</code> : null}
            {finding.description !== "" ? <p className={styles.findingText}>{finding.description}</p> : null}
            {finding.suggestion !== "" || finding.snippet !== "" ? <Extras finding={finding} /> : null}
          </li>
        ))}
      </ul>
      {degraded && report.content !== "" ? <RawAnswer content={report.content} /> : null}
    </div>
  );
}

/** The suggestion and the code strip of a finding, folded away until asked for. */
function Extras({ finding }: { finding: Finding }) {
  const { t } = useI18n();
  const lines = finding.snippet !== "" ? snippetLines(finding.snippet) : [];
  return (
    <details className={styles.more}>
      <summary>{t("jobs.findings.more")}</summary>
      {finding.suggestion !== "" ? (
        <>
          <p className={styles.moreLabel}>{t("jobs.findings.suggestion")}</p>
          <p className={styles.findingText}>{finding.suggestion}</p>
        </>
      ) : null}
      {lines.length > 0 ? (
        <>
          <p className={styles.moreLabel}>{t("jobs.findings.snippet")}</p>
          {/* A focusable scroll box for a long line. */}
          <pre className={styles.snippet} tabIndex={0}>
            {lines.map((line, index) => (
              <span key={index} className={line.kind === "add" ? styles.snippetAdd : line.kind === "del" ? styles.snippetDel : styles.snippetLine}>
                {line.text}
              </span>
            ))}
          </pre>
        </>
      ) : null}
    </details>
  );
}

/** The model's own words, kept for a review whose answer could not be read as findings. */
function RawAnswer({ content }: { content: string }) {
  const { t } = useI18n();
  return (
    <details className={styles.more}>
      <summary>{t("jobs.findings.raw")}</summary>
      <pre className={styles.snippet} tabIndex={0}>
        {content}
      </pre>
    </details>
  );
}
