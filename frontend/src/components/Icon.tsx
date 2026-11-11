// SPDX-License-Identifier: GPL-3.0-or-later
import type { SVGProps } from "react";

// Inline stroke icons (24x24 grid). The CSP forbids data: URIs and remote
// assets, so icons are plain SVG elements that inherit `currentColor`.
const paths = {
  repos: ["M6 3v12", "M18 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6z", "M6 21a3 3 0 1 0 0-6 3 3 0 0 0 0 6z", "M18 9a9 9 0 0 1-9 9"],
  jobs: ["M8 6h13", "M8 12h13", "M8 18h13", "M3 6h.01", "M3 12h.01", "M3 18h.01"],
  schedules: ["M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18z", "M12 7v5l3 2"],
  settings: ["M4 21v-7", "M4 10V3", "M12 21v-9", "M12 8V3", "M20 21v-5", "M20 12V3", "M1 14h6", "M9 8h6", "M17 16h6"],
  updates: ["M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18z", "M12 8v8", "M8.5 12.5 12 16l3.5-3.5"],
  chevronDown: ["m6 9 6 6 6-6"],
  check: ["M20 6 9 17l-5-5"],
  close: ["M18 6 6 18", "m6 6 12 12"],
  eye: ["M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12z", "M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z"],
  send: ["m22 2-11 11", "M22 2l-7 20-4-9-9-4z"],
  sun: ["M12 16a4 4 0 1 0 0-8 4 4 0 0 0 0 8z", "M12 2v2", "M12 20v2", "m4.93 4.93 1.41 1.41", "m17.66 17.66 1.41 1.41", "M2 12h2", "M20 12h2", "m6.34 17.66-1.41 1.41", "m19.07 4.93-1.41 1.41"],
  moon: ["M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z"],
  monitor: ["M3 4h18v12H3z", "M8 20h8", "M12 16v4"],
  globe: ["M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18z", "M3 12h18", "M12 3a14 14 0 0 1 0 18 14 14 0 0 1 0-18z"],
  // Severity glyphs: a different outline per level, so the level is readable without color.
  severityCritical: ["M8 3h8l5 5v8l-5 5H8l-5-5V8z", "M12 8v5", "M12 16.5v.01"],
  severityHigh: ["M12 3 22 20H2z", "M12 10v4", "M12 17v.01"],
  severityMedium: ["M12 3l9 9-9 9-9-9z", "M12 8v4", "M12 15.5v.01"],
  severityLow: ["M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18z", "M8 12h8"],
  severityInfo: ["M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18z", "M12 11v5", "M12 8v.01"],
  // Run status glyphs.
  statusQueued: ["M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18z", "M12 7v5l3 2"],
  statusRunning: ["M21 12a9 9 0 1 1-6.2-8.56"],
  statusSucceeded: ["M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18z", "m8 12 3 3 5-6"],
  statusFailed: ["M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18z", "m9 9 6 6", "m15 9-6 6"],
  statusCancelled: ["M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18z", "m5.6 5.6 12.8 12.8"],
  statusAttention: ["M12 3 22 20H2z", "M12 10v4", "M12 17v.01"],
  emptyBox: ["M3 7l9-4 9 4v10l-9 4-9-4z", "m3 7 9 4 9-4", "M12 11v10"],
} as const;

export type IconName = keyof typeof paths;

interface IconProps extends Omit<SVGProps<SVGSVGElement>, "name" | "children"> {
  name: IconName;
  size?: number;
}

/** Decorative by default (aria-hidden); the surrounding control carries the accessible name. */
export function Icon({ name, size = 16, ...rest }: IconProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.75}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
      {...rest}
    >
      {paths[name].map((d) => (
        <path key={d} d={d} />
      ))}
    </svg>
  );
}
