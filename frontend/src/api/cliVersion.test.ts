// SPDX-License-Identifier: GPL-3.0-or-later
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { CLI_MIN_VERSION, CLI_MIN_VERSION_POSTING, CLI_MIN_VERSION_SECRET_WRITE } from "./cliVersion";

// The version card words the minimums; the Go side decides what is enabled.
// They must agree, so the Go table is read from its source.
describe("CLI minimum versions", () => {
  const source = readFileSync(new URL("../../../internal/adapter/clicmd/version.go", import.meta.url), "utf8");
  const goMin = (capability: string): string => {
    const match = new RegExp(`${capability}:\\s*\\{(\\d+),\\s*(\\d+),\\s*(\\d+)\\}`).exec(source);
    if (!match) throw new Error(`${capability} not found in version.go; update this test`);
    return `${match[1]}.${match[2]}.${match[3]}`;
  };

  it("match capabilityTable in internal/adapter/clicmd/version.go", () => {
    expect(CLI_MIN_VERSION).toBe(goMin("CapBase"));
    expect(CLI_MIN_VERSION_SECRET_WRITE).toBe(goMin("CapConfigSetStdin"));
    expect(CLI_MIN_VERSION_POSTING).toBe(goMin("CapPosting"));
  });
});
