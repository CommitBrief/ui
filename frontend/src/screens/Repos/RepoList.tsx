// SPDX-License-Identifier: GPL-3.0-or-later
import { Button, Icon, Switch, type IconName } from "../../components";
import { useI18n, type MessageKey } from "../../i18n";
import type { Repository } from "../../api/repos";
import styles from "./Repos.module.css";

type RepoStatus = "ready" | "noPath" | "disabled";

// Every status carries an icon and a text label, so meaning never depends on color alone.
const statusIcon: Record<RepoStatus, IconName> = {
  ready: "statusSucceeded",
  noPath: "statusAttention",
  disabled: "statusCancelled",
};
const statusLabel: Record<RepoStatus, MessageKey> = {
  ready: "repos.status.ready",
  noPath: "repos.status.noPath",
  disabled: "repos.status.disabled",
};

/** Disabled wins; an enabled repo is ready only when it has a local checkout. */
export function repoStatus(repo: Pick<Repository, "enabled" | "localPath">): RepoStatus {
  if (!repo.enabled) return "disabled";
  return repo.localPath === "" ? "noPath" : "ready";
}

export interface RepoListProps {
  repos: readonly Repository[];
  /** Ids of the repos whose enabled flag is being saved; each switch is held until its own call returns. */
  busyIds: ReadonlySet<number>;
  onToggle: (repo: Repository, enabled: boolean) => void;
  onEdit: (repo: Repository) => void;
  onRemove: (repo: Repository) => void;
}

export function RepoList({ repos, busyIds, onToggle, onEdit, onRemove }: RepoListProps) {
  const { t } = useI18n();
  return (
    <div className={styles.listBox}>
      <ul className={styles.list} aria-label={t("repos.list.label")}>
        {repos.map((repo) => {
          const slug = `${repo.owner}/${repo.name}`;
          const status = repoStatus(repo);
          return (
            <li key={repo.id} className={styles.item}>
              <div className={styles.main}>
                <div className={styles.titleRow}>
                  <h2 className={styles.slug}>
                    <span className={styles.owner}>{repo.owner}/</span>
                    {repo.name}
                  </h2>
                  <span className={`${styles.pill} ${styles[`pill-${status}`]}`}>
                    <Icon name={statusIcon[status]} size={14} />
                    {t(statusLabel[status])}
                  </span>
                </div>
                {repo.localPath !== "" ? (
                  <p className={styles.path}>
                    <Icon name="folder" size={14} className={styles.pathIcon} />
                    <span className="sr-only">{t("repos.localPath.label")}: </span>
                    <span className={styles.pathText}>{repo.localPath}</span>
                  </p>
                ) : (
                  <p className={styles.warning}>
                    <Icon name="statusAttention" size={16} className={styles.warningIcon} />
                    <span>
                      {t("repos.noPath.warning")}{" "}
                      <button type="button" className={styles.linkButton} onClick={() => onEdit(repo)}>
                        {t("repos.noPath.fix")}
                        <span className="sr-only"> ({slug})</span>
                      </button>
                    </span>
                  </p>
                )}
              </div>
              <div className={styles.controls}>
                <Switch
                  className={styles.toggle}
                  label={t(repo.enabled ? "repos.enabled" : "repos.status.disabled")}
                  aria-label={t("repos.enabled.aria", { repo: slug })}
                  checked={repo.enabled}
                  disabled={busyIds.has(repo.id)}
                  onChange={(event) => onToggle(repo, event.target.checked)}
                />
                <div className={styles.actions}>
                  <Button
                    variant="ghost"
                    size="sm"
                    icon="pencil"
                    aria-label={t("repos.edit.aria", { repo: slug })}
                    onClick={() => onEdit(repo)}
                  >
                    {t("repos.edit")}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    icon="trash"
                    aria-label={t("repos.remove.aria", { repo: slug })}
                    onClick={() => onRemove(repo)}
                  >
                    {t("repos.remove")}
                  </Button>
                </div>
              </div>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
