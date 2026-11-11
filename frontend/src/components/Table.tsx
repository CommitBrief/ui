// SPDX-License-Identifier: GPL-3.0-or-later
import type { HTMLAttributes, ReactNode, TdHTMLAttributes, ThHTMLAttributes } from "react";
import { cx } from "./cx";
import styles from "./Table.module.css";

export interface TableProps {
  /** Accessible name of the scroll region and the table. */
  label: string;
  children: ReactNode;
  className?: string;
}

/**
 * The table scrolls inside its own focusable region, so a wide table never
 * produces page-level horizontal scroll and keyboard users can scroll it.
 */
export function Table({ label, children, className }: TableProps) {
  return (
    <div className={cx(styles.scroll, className)} role="region" aria-label={label} tabIndex={0}>
      <table className={styles.table}>
        {children}
      </table>
    </div>
  );
}

export function THead(props: HTMLAttributes<HTMLTableSectionElement>) {
  return <thead {...props} className={cx(styles.head, props.className)} />;
}

export function TBody(props: HTMLAttributes<HTMLTableSectionElement>) {
  return <tbody {...props} />;
}

export function Tr(props: HTMLAttributes<HTMLTableRowElement>) {
  return <tr {...props} className={cx(styles.row, props.className)} />;
}

export function Th({ alignEnd = false, ...rest }: ThHTMLAttributes<HTMLTableCellElement> & { alignEnd?: boolean }) {
  return <th scope="col" {...rest} className={cx(styles.cell, styles.th, alignEnd && styles.end, rest.className)} />;
}

export function Td({ alignEnd = false, ...rest }: TdHTMLAttributes<HTMLTableCellElement> & { alignEnd?: boolean }) {
  return <td {...rest} className={cx(styles.cell, alignEnd && styles.end, rest.className)} />;
}
