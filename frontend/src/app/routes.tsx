// SPDX-License-Identifier: GPL-3.0-or-later
import { lazy, Suspense, type ReactNode } from "react";
import type { IconName } from "../components";
import type { MessageKey } from "../i18n";
import { JobsScreen } from "../screens/Jobs";
import { ReposScreen } from "../screens/Repos";
import { SchedulesScreen } from "../screens/Schedules";
import { SettingsScreen } from "../screens/Settings";
import { UpdatesScreen } from "../screens/Updates";

export interface AppRoute {
  path: string;
  /** Catalog key of the navigation label and the document title. */
  labelKey: MessageKey;
  icon: IconName;
  /** "secondary" items sit at the bottom of the sidebar. */
  group: "primary" | "secondary";
  element: ReactNode;
}

export const routes: readonly AppRoute[] = [
  { path: "/repos", labelKey: "nav.repos", icon: "repos", group: "primary", element: <ReposScreen /> },
  { path: "/jobs", labelKey: "nav.jobs", icon: "jobs", group: "primary", element: <JobsScreen /> },
  { path: "/schedules", labelKey: "nav.schedules", icon: "schedules", group: "primary", element: <SchedulesScreen /> },
  { path: "/settings", labelKey: "nav.settings", icon: "settings", group: "secondary", element: <SettingsScreen /> },
  { path: "/updates", labelKey: "nav.updates", icon: "updates", group: "secondary", element: <UpdatesScreen /> },
];

export const defaultPath = "/repos";

// Development-only component gallery, reachable by typing #/_gallery. The
// dynamic import sits inside the `import.meta.env.DEV` branch, a build-time
// constant, so production builds neither contain the branch nor the chunk.
function createDevRoutes(): readonly { path: string; element: ReactNode }[] {
  const GalleryScreen = lazy(() => import("./Gallery"));
  return [
    {
      path: "/_gallery",
      element: (
        <Suspense fallback={null}>
          <GalleryScreen />
        </Suspense>
      ),
    },
  ];
}

const devRoutes = import.meta.env.DEV ? createDevRoutes() : [];

/** Empty hash opens the default screen; an unknown path resolves to null (not found). */
export function resolveRoute(path: string): { path: string; labelKey?: MessageKey; element: ReactNode } | null {
  const target = path === "/" ? defaultPath : path;
  return routes.find((route) => route.path === target) ?? devRoutes.find((route) => route.path === target) ?? null;
}
