// SPDX-License-Identifier: GPL-3.0-or-later
import { defineConfig } from "vitest/config";

// Unit tests only exercise pure modules (validation, error mapping), so they run
// in plain Node. The app's vite.config.ts is deliberately not reused: it loads
// the Wails plugin and the CSP plugin, which tests do not need.
export default defineConfig({
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"],
  },
});
