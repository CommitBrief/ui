// SPDX-License-Identifier: GPL-3.0-or-later
import { useId } from "react";
import styles from "./Logo.module.css";

// The brand mark (DESIGN_SYSTEM.md section 6): a rounded green square with a
// dark `>` arrow. The tile gradient runs green -> green-dim like the CLI logo.
export function Logo({ size = 28 }: { size?: number }) {
  const gradientId = useId();
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden="true" focusable="false">
      <defs>
        <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" className={styles.stopTop} />
          <stop offset="1" className={styles.stopBottom} />
        </linearGradient>
      </defs>
      <rect width="32" height="32" rx="8" fill={`url(#${gradientId})`} />
      <path
        className={styles.arrow}
        d="M11 9.5 19 16l-8 6.5"
        fill="none"
        strokeWidth="3"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}
