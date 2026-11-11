// SPDX-License-Identifier: GPL-3.0-or-later
import type { ReactNode } from "react";
import styles from "./PageHeader.module.css";

export interface PageHeaderProps {
  title: string;
  description?: string;
  /** Right-aligned actions, e.g. a primary <Button>. */
  actions?: ReactNode;
}

/** The single <h1> of a screen; the shell moves focus here on navigation. */
export function PageHeader({ title, description, actions }: PageHeaderProps) {
  return (
    <header className={styles.root}>
      <div className={styles.text}>
        <h1 className={styles.title} tabIndex={-1} data-page-heading="">
          {title}
        </h1>
        {description ? <p className={styles.description}>{description}</p> : null}
      </div>
      {actions ? <div className={styles.actions}>{actions}</div> : null}
    </header>
  );
}
