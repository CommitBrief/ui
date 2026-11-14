// SPDX-License-Identifier: GPL-3.0-or-later
import { describe, expect, it } from "vitest";
import { createWriteLock } from "./writeLock";

describe("createWriteLock", () => {
  it("lets one write at a time and tells the screen when it is busy", () => {
    const seen: boolean[] = [];
    const lock = createWriteLock((busy) => seen.push(busy));
    const release = lock.tryBegin();
    expect(release).not.toBeNull();
    expect(lock.isBusy()).toBe(true);
    // A second write (another section) has to wait: it gets no turn.
    expect(lock.tryBegin()).toBeNull();
    release?.();
    expect(lock.isBusy()).toBe(false);
    expect(lock.tryBegin()).not.toBeNull();
    expect(seen).toEqual([true, false, true]);
  });

  it("gives the turn back once, even if released twice", () => {
    const lock = createWriteLock();
    const first = lock.tryBegin();
    first?.();
    const second = lock.tryBegin();
    first?.(); // a stale release must not free the second write's turn
    expect(lock.isBusy()).toBe(true);
    second?.();
    expect(lock.isBusy()).toBe(false);
  });
});
