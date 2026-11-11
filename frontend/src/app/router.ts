// SPDX-License-Identifier: GPL-3.0-or-later
import { useSyncExternalStore } from "react";

// A hash router: the app is served from the embedded asset handler, so there is
// no server to answer deep links. Paths look like `#/jobs`.

function subscribe(callback: () => void): () => void {
  window.addEventListener("hashchange", callback);
  return () => window.removeEventListener("hashchange", callback);
}

function readPath(): string {
  const raw = window.location.hash.replace(/^#/, "");
  const path = raw.length > 1 ? raw.replace(/\/+$/, "") : raw;
  return path === "" ? "/" : path;
}

/** The current hash path, e.g. "/jobs"; "/" when the hash is empty. */
export function usePath(): string {
  return useSyncExternalStore(subscribe, readPath, () => "/");
}
