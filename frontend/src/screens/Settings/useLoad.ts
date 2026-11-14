// SPDX-License-Identifier: GPL-3.0-or-later
import { useCallback, useEffect, useRef, useState } from "react";
import { toSettingsError, type SettingsError } from "../../api/settings";

export interface LoadState<T> {
  /** The last value that loaded; kept while a reload runs or fails. */
  data: T | null;
  /** The failure of the last load; cleared when a load starts. */
  error: SettingsError | null;
  loading: boolean;
}

export interface Loader<T> extends LoadState<T> {
  reload: () => void;
  /** Replaces the value without a round trip (after a write that returned the new state). */
  setData: (next: T) => void;
}

/**
 * Loads a value when the component mounts and again on `reload()`. A result
 * that arrives after a newer load started, or after unmount, is dropped.
 */
export function useLoad<T>(load: () => Promise<T>): Loader<T> {
  const [state, setState] = useState<LoadState<T>>({ data: null, error: null, loading: true });
  const loadRef = useRef(load);
  // Declared before the load effect below so the first load already sees the latest function.
  useEffect(() => {
    loadRef.current = load;
  });
  const latest = useRef(0);
  const alive = useRef(true);

  // Starts a read. It never sets state synchronously, so the mount effect can call it.
  const fetchNow = useCallback(() => {
    const id = ++latest.current;
    loadRef.current().then(
      (data) => {
        if (alive.current && id === latest.current) setState({ data, error: null, loading: false });
      },
      (error: unknown) => {
        if (alive.current && id === latest.current) {
          setState((current) => ({ data: current.data, error: toSettingsError(error), loading: false }));
        }
      },
    );
  }, []);

  const reload = useCallback(() => {
    setState((current) => ({ data: current.data, error: null, loading: true }));
    fetchNow();
  }, [fetchNow]);

  useEffect(() => {
    alive.current = true;
    fetchNow();
    return () => {
      alive.current = false;
    };
  }, [fetchNow]);

  const setData = useCallback((next: T) => {
    latest.current++;
    setState({ data: next, error: null, loading: false });
  }, []);

  return { ...state, reload, setData };
}
