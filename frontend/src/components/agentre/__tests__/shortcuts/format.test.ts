import { describe, expect, it } from "vitest";

import {
  chordFromEvent,
  formatChord,
  formatPrimaryModifier,
  isPrimaryModifier,
  isPrimaryModifierKey,
} from "../../shortcuts/format";

function mkEvent(init: KeyboardEventInit & { key: string }): KeyboardEvent {
  return new KeyboardEvent("keydown", init);
}

describe("formatChord", () => {
  it("renders ⌘D on darwin and Ctrl+D elsewhere", () => {
    expect(formatChord({ mod: "primary", key: "D" }, "darwin")).toBe("⌘D");
    expect(formatChord({ mod: "primary", key: "D" }, "windows")).toBe("Ctrl+D");
    expect(formatChord({ mod: "primary", key: "D" }, "linux")).toBe("Ctrl+D");
    expect(formatChord({ mod: "primary", key: "D" }, "unknown")).toBe("Ctrl+D");
  });

  it("renders primary-alt as ⌥⌘ on darwin and Ctrl+Alt+ elsewhere", () => {
    expect(formatChord({ mod: "primary-alt", key: "B" }, "darwin")).toBe("⌥⌘B");
    expect(formatChord({ mod: "primary-alt", key: "B" }, "windows")).toBe(
      "Ctrl+Alt+B",
    );
    expect(formatChord({ mod: "primary-alt", key: "B" }, "linux")).toBe(
      "Ctrl+Alt+B",
    );
  });

  it("adds shift glyph for primary-shift", () => {
    expect(formatChord({ mod: "primary-shift", key: "K" }, "darwin")).toBe(
      "⌘⇧K",
    );
    expect(formatChord({ mod: "primary-shift", key: "K" }, "linux")).toBe(
      "Ctrl+Shift+K",
    );
  });

  it("renders the comma key literally", () => {
    expect(formatChord({ mod: "primary", key: "," }, "darwin")).toBe("⌘,");
  });
});

describe("formatPrimaryModifier", () => {
  it("renders the platform primary modifier label", () => {
    expect(formatPrimaryModifier("darwin")).toBe("⌘");
    expect(formatPrimaryModifier("windows")).toBe("Ctrl");
    expect(formatPrimaryModifier("linux")).toBe("Ctrl");
    expect(formatPrimaryModifier("unknown")).toBe("Ctrl");
  });
});

describe("isPrimaryModifier", () => {
  it("uses metaKey on darwin", () => {
    expect(
      isPrimaryModifier(mkEvent({ key: "Meta", metaKey: true }), "darwin"),
    ).toBe(true);
    expect(
      isPrimaryModifier(mkEvent({ key: "Control", ctrlKey: true }), "darwin"),
    ).toBe(false);
  });

  it("uses ctrlKey on windows/linux/unknown", () => {
    expect(
      isPrimaryModifier(mkEvent({ key: "Control", ctrlKey: true }), "linux"),
    ).toBe(true);
    expect(
      isPrimaryModifier(mkEvent({ key: "Meta", metaKey: true }), "windows"),
    ).toBe(false);
  });
});

describe("isPrimaryModifierKey", () => {
  it("only treats Meta itself as the darwin primary modifier key", () => {
    expect(
      isPrimaryModifierKey(mkEvent({ key: "Meta", metaKey: true }), "darwin"),
    ).toBe(true);
    expect(
      isPrimaryModifierKey(
        mkEvent({ key: "Control", ctrlKey: true }),
        "darwin",
      ),
    ).toBe(false);
  });

  it("only treats Control itself as the non-darwin primary modifier key", () => {
    expect(
      isPrimaryModifierKey(mkEvent({ key: "Control", ctrlKey: true }), "linux"),
    ).toBe(true);
    expect(
      isPrimaryModifierKey(mkEvent({ key: "Meta", metaKey: true }), "linux"),
    ).toBe(false);
  });
});

describe("chordFromEvent", () => {
  it("returns null when no primary modifier is held", () => {
    expect(chordFromEvent(mkEvent({ key: "D" }), "darwin")).toBeNull();
  });

  it("returns null for a bare modifier press", () => {
    expect(
      chordFromEvent(mkEvent({ key: "Meta", metaKey: true }), "darwin"),
    ).toBeNull();
  });

  it("captures a primary letter as { mod: 'primary', key: 'D' }", () => {
    expect(
      chordFromEvent(mkEvent({ key: "d", metaKey: true }), "darwin"),
    ).toEqual({ mod: "primary", key: "D" });
  });

  it("captures a number key as { mod: 'primary', key: '1' }", () => {
    expect(
      chordFromEvent(mkEvent({ key: "1", metaKey: true }), "darwin"),
    ).toEqual({ mod: "primary", key: "1" });
  });

  it("captures comma", () => {
    expect(
      chordFromEvent(mkEvent({ key: ",", metaKey: true }), "darwin"),
    ).toEqual({ mod: "primary", key: "," });
  });

  it("captures primary-shift when shift is also held", () => {
    expect(
      chordFromEvent(
        mkEvent({ key: "K", metaKey: true, shiftKey: true }),
        "darwin",
      ),
    ).toEqual({ mod: "primary-shift", key: "K" });
  });

  it("Given macOS, When ⌥⌘B is pressed (Option rewrites key to ∫), Then it captures primary-alt B from the physical key", () => {
    expect(
      chordFromEvent(
        mkEvent({ key: "∫", code: "KeyB", metaKey: true, altKey: true }),
        "darwin",
      ),
    ).toEqual({ mod: "primary-alt", key: "B" });
  });

  it("Given Windows, When Ctrl+Alt+1 / Ctrl+Alt+, is pressed, Then it captures primary-alt from the physical key", () => {
    expect(
      chordFromEvent(
        mkEvent({ key: "1", code: "Digit1", ctrlKey: true, altKey: true }),
        "windows",
      ),
    ).toEqual({ mod: "primary-alt", key: "1" });
    expect(
      chordFromEvent(
        mkEvent({ key: ",", code: "Comma", ctrlKey: true, altKey: true }),
        "windows",
      ),
    ).toEqual({ mod: "primary-alt", key: "," });
  });

  it("rejects alt+shift, alt without the primary key, and meta+ctrl combos", () => {
    expect(
      chordFromEvent(
        mkEvent({
          key: "K",
          code: "KeyK",
          metaKey: true,
          altKey: true,
          shiftKey: true,
        }),
        "darwin",
      ),
    ).toBeNull();
    expect(
      chordFromEvent(
        mkEvent({ key: "∫", code: "KeyB", altKey: true }),
        "darwin",
      ),
    ).toBeNull();
    expect(
      chordFromEvent(
        mkEvent({ key: "k", metaKey: true, ctrlKey: true }),
        "darwin",
      ),
    ).toBeNull();
  });
});
