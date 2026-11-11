// SPDX-License-Identifier: GPL-3.0-or-later
import type { ReactNode } from "react";
import { Icon, type IconName } from "./Icon";
import styles from "./EmptyState.module.css";

export interface EmptyStateProps {
  title: string;
  description?: string;
  icon?: IconName;
  /** Optional call to action, e.g. a <Button>. Omit when nothing can act yet. */
  action?: ReactNode;
}

export function EmptyState({ title, description, icon = "emptyBox", action }: EmptyStateProps) {
  return (
    <div className={styles.root}>
      <span className={styles.icon}>
        <Icon name={icon} size={24} />
      </span>
      <h2 className={styles.title}>{title}</h2>
      {description ? <p className={styles.description}>{description}</p> : null}
      {action ? <div className={styles.action}>{action}</div> : null}
    </div>
  );
}
