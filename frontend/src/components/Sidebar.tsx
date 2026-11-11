// SPDX-License-Identifier: GPL-3.0-or-later
import type { ReactNode } from "react";
import { cx } from "./cx";
import { Icon, type IconName } from "./Icon";
import { Logo } from "./Logo";
import styles from "./Sidebar.module.css";

export interface SidebarItem {
  path: string;
  label: string;
  icon: IconName;
}

export interface SidebarProps {
  /** Accessible name of the <nav>. */
  label: string;
  productName: string;
  items: readonly SidebarItem[];
  /** Items shown after a divider, pinned to the bottom of the list. */
  secondaryItems?: readonly SidebarItem[];
  activePath: string | null;
  footer?: ReactNode;
}

/**
 * Wide windows get a labelled column; narrow windows collapse to an icon rail.
 * The labels stay in the DOM (visually hidden) so the rail keeps its names.
 */
export function Sidebar({ label, productName, items, secondaryItems = [], activePath, footer }: SidebarProps) {
  const renderItem = (item: SidebarItem) => (
    <li key={item.path}>
      <a
        className={styles.link}
        href={`#${item.path}`}
        title={item.label}
        aria-current={item.path === activePath ? "page" : undefined}
      >
        <Icon name={item.icon} size={18} />
        <span className={styles.linkLabel}>{item.label}</span>
      </a>
    </li>
  );

  return (
    <aside className={styles.root}>
      <div className={styles.brand}>
        <Logo size={28} />
        <span className={styles.brandName}>{productName}</span>
      </div>
      <nav className={styles.nav} aria-label={label}>
        <ul className={styles.list}>{items.map(renderItem)}</ul>
        {secondaryItems.length > 0 ? (
          <ul className={cx(styles.list, styles.secondary)}>{secondaryItems.map(renderItem)}</ul>
        ) : null}
      </nav>
      {footer ? <div className={styles.footer}>{footer}</div> : null}
    </aside>
  );
}
