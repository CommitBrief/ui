// SPDX-License-Identifier: GPL-3.0-or-later
import { defineConfig } from "vitest/config";

// Unit tests exercise pure modules (validation, error mapping) and components that
// do not need the Wails runtime (rendered to a string), so they run in plain Node. The app's
// vite.config.ts is deliberately not reused: it loads the Wails plugin and the
// CSP plugin, which tests do not need.
export default defineConfig({
  test: {
    environment: "node",
    include: ["src/**/*.test.{ts,tsx}"],
  },
});
