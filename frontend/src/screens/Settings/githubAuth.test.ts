// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, expect, it } from "vitest";
import type { AuthStatus } from "../../api/settings";
import { GH_LOGIN_COMMAND, describeAuth, loginCommand } from "./githubAuth";

const base: AuthStatus = {
  installed: true,
  path: "/usr/bin/gh",
  authenticated: false,
  host: "github.com",
  account: "",
  scopes: [],
  problem: "",
};

describe("describeAuth", () => {
  it("says gh is missing, and offers no login command for a tool that is not there", () => {
    const view = describeAuth({ ...base, installed: false, path: "" });
    expect(view.label).toBe("settings.github.status.notInstalled");
    expect(view.showCommand).toBe(false);
  });

  it("is quiet when signed in", () => {
    const view = describeAuth({ ...base, authenticated: true, account: "octo" });
    expect(view).toMatchObject({ tone: "ok", guide: null, showCommand: false });
  });

  it("shows the login command when there is no account", () => {
    const view = describeAuth({ ...base, problem: "not_logged_in" });
    expect(view.label).toBe("settings.github.status.notLoggedIn");
    expect(view.showCommand).toBe(true);
  });

  it("shows the login command for a rejected token", () => {
    const view = describeAuth({ ...base, problem: "token_invalid" });
    expect(view.label).toBe("settings.github.status.tokenInvalid");
    expect(view.showCommand).toBe(true);
  });

  it("does not send the user to log in again when only the network is down", () => {
    const view = describeAuth({ ...base, problem: "unreachable" });
    expect(view.label).toBe("settings.github.status.unreachable");
    expect(view.showCommand).toBe(false);
  });

  it("uses the command the app documents", () => {
    expect(GH_LOGIN_COMMAND).toBe("gh auth login");
  });
});

describe("loginCommand", () => {
  it("is the bare command for github.com and when the host is unknown", () => {
    expect(loginCommand("github.com")).toBe("gh auth login");
    expect(loginCommand("")).toBe("gh auth login");
  });

  it("adds --hostname for a GitHub Enterprise host", () => {
    expect(loginCommand("git.corp.example")).toBe("gh auth login --hostname git.corp.example");
  });

  it("never puts anything but a plain host name into a command the user will paste", () => {
    for (const host of ["x; rm -rf ~", "a b", "$(id)", "h`id`", "-flag", "host\nrm"]) {
      expect(loginCommand(host)).toBe("gh auth login");
    }
  });
});
