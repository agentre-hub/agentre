import { describe, expect, it } from "vitest";

import {
  findConflict,
  isSystemReserved,
  sameChord,
} from "../../shortcuts/conflict";

describe("sameChord", () => {
  it("compares mod and key case-insensitively on the letter", () => {
    expect(
      sameChord({ mod: "primary", key: "D" }, { mod: "primary", key: "d" }),
    ).toBe(true);
    expect(
      sameChord(
        { mod: "primary", key: "D" },
        { mod: "primary-shift", key: "D" },
      ),
    ).toBe(false);
    expect(
      sameChord({ mod: "primary", key: "D" }, { mod: "primary", key: "J" }),
    ).toBe(false);
    // ⌥⌘B（右侧工具窗）与 ⌘B（看板）是两个组合。
    expect(
      sameChord({ mod: "primary", key: "B" }, { mod: "primary-alt", key: "B" }),
    ).toBe(false);
  });
});

describe("isSystemReserved", () => {
  it("rejects ⌘C / ⌘A / ⌘H but not ⌘D", () => {
    expect(isSystemReserved({ mod: "primary", key: "C" })).toBe(true);
    expect(isSystemReserved({ mod: "primary", key: "A" })).toBe(true);
    expect(isSystemReserved({ mod: "primary", key: "H" })).toBe(true);
    expect(isSystemReserved({ mod: "primary", key: "D" })).toBe(false);
  });

  it("does NOT reserve primary-shift variants (e.g. ⌘⇧C is fine)", () => {
    expect(isSystemReserved({ mod: "primary-shift", key: "C" })).toBe(false);
  });
});

describe("findConflict", () => {
  const bindings = new Map([
    ["nav.chat", { mod: "primary", key: "D" as string }],
    ["nav.issues", { mod: "primary", key: "B" as string }],
  ] as const);

  it("returns null when chord is free", () => {
    expect(
      findConflict(bindings, { mod: "primary", key: "K" }, "nav.chat"),
    ).toBeNull();
  });

  it("returns 'system' when chord hits the reserved set", () => {
    expect(
      findConflict(bindings, { mod: "primary", key: "C" }, "nav.chat"),
    ).toEqual({ type: "system" });
  });

  it("returns binding id when chord collides with another action", () => {
    expect(
      findConflict(bindings, { mod: "primary", key: "B" }, "nav.chat"),
    ).toEqual({ type: "binding", id: "nav.issues" });
  });

  it("ignores the excludeId (re-saving the same chord on the same row is fine)", () => {
    expect(
      findConflict(bindings, { mod: "primary", key: "D" }, "nav.chat"),
    ).toBeNull();
  });
});
