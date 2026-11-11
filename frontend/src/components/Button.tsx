// SPDX-License-Identifier: GPL-3.0-or-later
import { forwardRef, type ButtonHTMLAttributes, type ReactNode } from "react";
import { useI18n } from "../i18n";
import { cx } from "./cx";
import { Icon, type IconName } from "./Icon";
import styles from "./Button.module.css";

export type ButtonVariant = "primary" | "secondary" | "ghost" | "danger";
export type ButtonSize = "md" | "sm";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  /** Decorative icon before the label. */
  icon?: IconName;
  /** Shows a spinner and ignores activation while an action is running. */
  loading?: boolean;
  children?: ReactNode;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = "secondary", size = "md", icon, loading = false, className, children, type = "button", onClick, ...rest },
  ref,
) {
  const { t } = useI18n();
  return (
    <button
      {...rest}
      ref={ref}
      type={type}
      className={cx(styles.button, styles[variant], size === "sm" && styles.small, className)}
      // aria-disabled instead of disabled while loading keeps the button
      // focusable, so keyboard users do not lose their place.
      aria-disabled={loading || undefined}
      aria-busy={loading || undefined}
      // While loading, activation is swallowed. preventDefault also stops a
      // type="submit" button from submitting its form a second time (click,
      // Enter and Space all arrive as a click event).
      onClick={loading ? (event) => event.preventDefault() : onClick}
    >
      {loading ? <Icon name="statusRunning" className={styles.spinner} /> : icon ? <Icon name={icon} /> : null}
      {children}
      {loading ? <span className="sr-only">{t("common.loading")}</span> : null}
    </button>
  );
});
