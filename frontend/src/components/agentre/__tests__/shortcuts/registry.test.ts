import { describe, expect, it } from "vitest";

import {
  REGISTRY,
  SESSION_CHIP_IDS,
  TAB_CHIP_IDS,
  TAB_CLOSE_ID,
  SYSTEM_RESERVED,
  getDefaultBindings,
} from "../../shortcuts/registry";

describe("shortcut registry", () => {
  it("defines seven rebindable global navigation actions, two palette actions, nine fixed session-chip actions, and ten fixed tabs actions", () => {
    const global = REGISTRY.filter((def) => def.scope === "global");
    const session = REGISTRY.filter((def) => def.scope === "session");
    const tabs = REGISTRY.filter((def) => def.scope === "tabs");

    expect(global.map((def) => def.id)).toEqual([
      "nav.chat",
      "nav.projects",
      "nav.issues",
      "nav.org",
      "nav.hooks",
      "nav.memory",
      "nav.settings",
      "palette.open",
      "cmd.new-chat",
    ]);
    expect(global.every((def) => def.rebindable)).toBe(true);

    expect(session).toHaveLength(9);
    expect(session.every((def) => !def.rebindable)).toBe(true);
    expect(session.map((def) => def.id)).toEqual(SESSION_CHIP_IDS);

    // 9 tab-switch + 1 tab-close
    expect(tabs).toHaveLength(10);
    expect(tabs.every((def) => !def.rebindable)).toBe(true);
    expect(tabs.map((def) => def.id)).toEqual([...TAB_CHIP_IDS, TAB_CLOSE_ID]);
  });

  it("registers palette.open with default ⌘P, scope=global, rebindable=true", () => {
    const def = REGISTRY.find((d) => d.id === "palette.open");
    expect(def).toBeDefined();
    expect(def?.scope).toBe("global");
    expect(def?.rebindable).toBe(true);
    expect(def?.defaultBinding).toEqual({ mod: "primary", key: "P" });
  });

  it("registers cmd.new-chat with default ⌘N, scope=global, rebindable=true", () => {
    const def = REGISTRY.find((d) => d.id === "cmd.new-chat");
    expect(def).toBeDefined();
    expect(def?.scope).toBe("global");
    expect(def?.rebindable).toBe(true);
    expect(def?.defaultBinding).toEqual({ mod: "primary", key: "N" });
  });

  it("assigns the default E/D/B/G/Y/U/, bindings to navigation actions", () => {
    const defaults = getDefaultBindings();
    expect(defaults.get("nav.chat")).toEqual({ mod: "primary", key: "E" });
    expect(defaults.get("nav.projects")).toEqual({ mod: "primary", key: "D" });
    expect(defaults.get("nav.issues")).toEqual({ mod: "primary", key: "B" });
    expect(defaults.get("nav.org")).toEqual({ mod: "primary", key: "G" });
    expect(defaults.get("nav.hooks")).toEqual({ mod: "primary", key: "Y" });
    expect(defaults.get("nav.memory")).toEqual({ mod: "primary", key: "U" });
    expect(defaults.get("nav.settings")).toEqual({ mod: "primary", key: "," });
  });

  it("assigns ⌘1-9 to session-chip actions in order", () => {
    const defaults = getDefaultBindings();
    for (let i = 1; i <= 9; i += 1) {
      expect(defaults.get(`chat.session.${i}`)).toEqual({
        mod: "primary",
        key: String(i),
      });
    }
  });

  it("assigns ⌘1-9 to chat.tab.N in order and ⌘W to chat.tab.close", () => {
    const defaults = getDefaultBindings();
    for (let i = 1; i <= 9; i += 1) {
      expect(defaults.get(`chat.tab.${i}`)).toEqual({
        mod: "primary",
        key: String(i),
      });
    }
    expect(defaults.get(TAB_CLOSE_ID)).toEqual({ mod: "primary", key: "W" });
  });

  it("declares the macOS system-reserved set (⌘C/V/X/A/Z/Q/M/H) — ⌘W removed (now handled by chat.tab.close)", () => {
    const keys = new Set(SYSTEM_RESERVED.map((c) => c.key));
    for (const k of ["C", "V", "X", "A", "Z", "Q", "M", "H"]) {
      expect(keys.has(k)).toBe(true);
    }
    // ⌘W is intentionally NOT system-reserved — app overrides it for tab close.
    expect(keys.has("W")).toBe(false);
    expect(SYSTEM_RESERVED.every((c) => c.mod === "primary")).toBe(true);
  });

  it("does not duplicate ids", () => {
    const ids = REGISTRY.map((def) => def.id);
    expect(new Set(ids).size).toBe(ids.length);
  });
});
