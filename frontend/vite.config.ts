// SPDX-License-Identifier: GPL-3.0-or-later
import { readFileSync } from "node:fs";
import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import wails from "@wailsio/runtime/plugins/vite";

// The Go side sends this policy as a response header (UI-0001 section 4). The
// production build also bakes it into index.html as a <meta> tag, so the policy
// holds even where a webview ignores the header. A <meta> CSP cannot carry
// frame-ancestors, so that directive is dropped here. The dev server is left
// alone: Vite needs inline scripts and a WebSocket.
function contentSecurityPolicyMeta(): Plugin {
  const policy = readFileSync(
    new URL("../internal/adapter/desktop/csp.txt", import.meta.url),
    "utf8",
  )
    .trim()
    .split(";")
    .map((directive) => directive.trim())
    .filter((directive) => directive !== "" && !directive.startsWith("frame-ancestors"))
    .join("; ");

  return {
    name: "commitbrief-csp-meta",
    apply: "build",
    transformIndexHtml: {
      order: "post",
      handler: () => [
        {
          tag: "meta",
          attrs: { "http-equiv": "Content-Security-Policy", content: policy },
          injectTo: "head-prepend",
        },
      ],
    },
  };
}

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: Number(process.env["WAILS_VITE_PORT"]) || 9245,
    strictPort: true,
  },
  plugins: [react(), wails("./bindings"), contentSecurityPolicyMeta()],
});
