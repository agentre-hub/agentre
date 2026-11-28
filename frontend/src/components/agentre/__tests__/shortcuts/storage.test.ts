import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  STORAGE_KEY,
  loadShortcutsState,
  saveShortcutsState,
} from "../../shortcuts/storage";

beforeEach(() => {
  localStorage.clear();
  vi.spyOn(console, "warn").mockImplementation(() => {});
});

afterEach(() => {
  vi.restoreAllMocks();
  localStorage.clear();
});

describe("loadShortcutsState", () => {
  it("returns defaults when storage is empty", () => {
    const state = loadShortcutsState();
    // No override → bindings map only carries defaults from registry.
    expect(state.bindings.get("nav.chat")).toEqual({
      mod: "primary",
      key: "E",
    });
    expect(state.bindings.get("nav.projects")).toEqual({
      mod: "primary",
      key: "D",
    });
  });

  it("merges persisted overrides on top of defaults", () => {
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({
        version: 1,
        bindings: { "nav.chat": { mod: "primary", key: "K" } },
      }),
    );

    const state = loadShortcutsState();
    expect(state.bindings.get("nav.chat")).toEqual({
      mod: "primary",
      key: "K",
    });
    // Unspecified ids keep the default
    expect(state.bindings.get("nav.projects")).toEqual({
      mod: "primary",
      key: "D",
    });
  });

  it("Given a saved primary-alt override next to an old primary one, Then both load", () => {
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({
        version: 1,
        bindings: {
          "nav.chat": { mod: "primary", key: "K" },
          "toolWindow.region.right": { mod: "primary-alt", key: "r" },
        },
      }),
    );

    const state = loadShortcutsState();
    expect(state.bindings.get("nav.chat")).toEqual({
      mod: "primary",
      key: "K",
    });
    expect(state.bindings.get("toolWindow.region.right")).toEqual({
      mod: "primary-alt",
      key: "R",
    });
  });

  it("ignores legacy behavior fields silently", () => {
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({
        version: 1,
        bindings: { "nav.chat": { mod: "primary", key: "K" } },
        behavior: { showHud: false, hudInInputs: true },
      }),
    );

    const state = loadShortcutsState();
    expect(state.bindings.get("nav.chat")).toEqual({
      mod: "primary",
      key: "K",
    });
    // No behavior on the returned state — the legacy field is dropped.
    expect("behavior" in state).toBe(false);
  });

  it("drops unknown ids without affecting valid ones", () => {
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({
        version: 1,
        bindings: {
          "nav.chat": { mod: "primary", key: "K" },
          "nav.ghost": { mod: "primary", key: "Z" },
        },
      }),
    );

    const state = loadShortcutsState();
    expect(state.bindings.get("nav.chat")).toEqual({
      mod: "primary",
      key: "K",
    });
    expect(state.bindings.has("nav.ghost")).toBe(false);
    expect(console.warn).toHaveBeenCalled();
  });

  it("drops a binding with a malformed chord", () => {
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({
        version: 1,
        bindings: {
          "nav.chat": { mod: "primary", key: "K" },
          "nav.issues": { mod: "garbage", key: 42 },
        },
      }),
    );

    const state = loadShortcutsState();
    expect(state.bindings.get("nav.chat")).toEqual({
      mod: "primary",
      key: "K",
    });
    // Falls back to default for nav.issues
    expect(state.bindings.get("nav.issues")).toEqual({
      mod: "primary",
      key: "B",
    });
  });

  it("falls back to all defaults when JSON is corrupted", () => {
    localStorage.setItem(STORAGE_KEY, "{not json");

    const state = loadShortcutsState();
    expect(state.bindings.get("nav.chat")).toEqual({
      mod: "primary",
      key: "E",
    });
    expect(console.warn).toHaveBeenCalled();
  });

  it("falls back to defaults on an unknown version", () => {
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({ version: 99, bindings: {} }),
    );

    const state = loadShortcutsState();
    expect(state.bindings.get("nav.chat")).toEqual({
      mod: "primary",
      key: "E",
    });
  });
});

describe("saveShortcutsState", () => {
  it("persists only overrides (not defaults)", () => {
    const bindings = new Map<string, { mod: "primary"; key: string }>([
      ["nav.chat", { mod: "primary", key: "K" }],
      ["nav.projects", { mod: "primary", key: "D" }], // default — should not be stored
    ]);
    saveShortcutsState({ bindings });

    const raw = JSON.parse(localStorage.getItem(STORAGE_KEY)!);
    expect(raw.version).toBe(1);
    expect(raw.bindings).toEqual({
      "nav.chat": { mod: "primary", key: "K" },
    });
    expect("behavior" in raw).toBe(false);
  });
});
