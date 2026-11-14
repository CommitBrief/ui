// SPDX-License-Identifier: GPL-3.0-or-later
import type { AuthStatus } from "../../api/settings";
import type { MessageKey } from "../../i18n";
import type { PillTone } from "./parts";

/** The command that fixes a missing or broken sign-in. The app runs nothing itself. */
export const GH_LOGIN_COMMAND = "gh auth login";

const hostName = /^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$/;

/**
 * The sign-in command for a host. github.com is gh's default; a GitHub
 * Enterprise host needs `--hostname`. The host comes from gh's own output and
 * ends up in a command the user pastes into a shell, so anything that is not a
 * plain host name falls back to the bare command.
 */
export function loginCommand(host: string): string {
  return host !== "" && host !== "github.com" && hostName.test(host) ? `${GH_LOGIN_COMMAND} --hostname ${host}` : GH_LOGIN_COMMAND;
}

interface StatusView {
  tone: PillTone;
  label: MessageKey;
  /** The guidance under the status; null when there is nothing to fix. */
  guide: MessageKey | null;
  showCommand: boolean;
}

/** Maps what `gh auth status` reported to what the card says. Pure, so it is unit-tested. */
export function describeAuth(status: AuthStatus): StatusView {
  if (!status.installed) {
    return { tone: "error", label: "settings.github.status.notInstalled", guide: "settings.github.notInstalled.description", showCommand: false };
  }
  if (status.authenticated) {
    return { tone: "ok", label: "settings.github.status.ok", guide: null, showCommand: false };
  }
  switch (status.problem) {
    case "token_invalid":
      return { tone: "warning", label: "settings.github.status.tokenInvalid", guide: "settings.github.login.tokenInvalid", showCommand: true };
    case "unreachable":
      return { tone: "warning", label: "settings.github.status.unreachable", guide: "settings.github.login.unreachable", showCommand: false };
    default:
      return { tone: "warning", label: "settings.github.status.notLoggedIn", guide: "settings.github.login.notLoggedIn", showCommand: true };
  }
}
