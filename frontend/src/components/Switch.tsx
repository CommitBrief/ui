// SPDX-License-Identifier: GPL-3.0-or-later
import { forwardRef, useId, type InputHTMLAttributes, type ReactNode } from "react";
import { cx } from "./cx";
import styles from "./Switch.module.css";

export interface SwitchProps extends Omit<InputHTMLAttributes<HTMLInputElement>, "type" | "role" | "id" | "children"> {
  label: string;
  description?: ReactNode;
}

/**
 * A native checkbox exposed as role="switch": Space toggles, the state is
 * announced. The description is bound with aria-describedby, so the accessible
 * name stays the short label.
 */
export const Switch = forwardRef<HTMLInputElement, SwitchProps>(function Switch(
  { label, description, className, ...rest },
  ref,
) {
  const id = useId();
  const descriptionId = `${id}-description`;
  return (
    <div className={cx(styles.root, rest.disabled && styles.disabled, className)}>
      <input
        {...rest}
        ref={ref}
        id={id}
        type="checkbox"
        role="switch"
        className={styles.input}
        aria-describedby={description ? descriptionId : undefined}
      />
      <div className={styles.text}>
        <label className={styles.label} htmlFor={id}>
          {label}
        </label>
        {description ? (
          <span id={descriptionId} className={styles.description}>
            {description}
          </span>
        ) : null}
      </div>
    </div>
  );
});
