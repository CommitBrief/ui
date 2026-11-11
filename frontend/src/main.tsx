import React from "react";
import ReactDOM from "react-dom/client";
import "@fontsource-variable/geist";
import "@fontsource-variable/jetbrains-mono";
import "./styles/global.css";
import App from "./App";
import { initTheme, ThemeProvider } from "./app/theme";
import { I18nProvider } from "./i18n/I18nProvider";

const root = document.getElementById("root");
if (!root) {
  throw new Error("missing #root element");
}

// Apply a pinned theme before the first render so it never flashes.
const initialTheme = initTheme();

ReactDOM.createRoot(root).render(
  <React.StrictMode>
    <ThemeProvider initial={initialTheme}>
      <I18nProvider>
        <App />
      </I18nProvider>
    </ThemeProvider>
  </React.StrictMode>,
);
