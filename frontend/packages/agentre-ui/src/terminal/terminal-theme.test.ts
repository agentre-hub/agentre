import { describe, it, expect } from "vitest";
import { resolveTerminalTheme } from "./terminal-theme";

// xterm only ships visible glyph colors for the 16-entry ANSI palette when each
// entry is set; leaving them at xterm's black-bg defaults makes light mode wash
// out. These are the keys a complete terminal theme must carry.
const ANSI_KEYS = [
  "black",
  "red",
  "green",
  "yellow",
  "blue",
  "magenta",
  "cyan",
  "white",
  "brightBlack",
  "brightRed",
  "brightGreen",
  "brightYellow",
  "brightBlue",
  "brightMagenta",
  "brightCyan",
  "brightWhite",
] as const;

describe("resolveTerminalTheme", () => {
  it("Given dark mode, Then it returns the dark background with a full ANSI palette", () => {
    const theme = resolveTerminalTheme(true);
    expect(theme.background).toBe("#17191c");
    expect(theme.foreground).toBe("#e6e8eb");
    for (const key of ANSI_KEYS) {
      expect(theme[key], `dark theme missing ${key}`).toBeTruthy();
    }
    // Explicit selection fg/bg keep WebGL/canvas from re-rasterizing selected
    // glyphs into a different weight.
    expect(theme.selectionForeground).toBeTruthy();
    expect(theme.selectionBackground).toBeTruthy();
  });

  it("Given light mode, Then it returns the light background with a full ANSI palette", () => {
    const theme = resolveTerminalTheme(false);
    expect(theme.background).toBe("#fafafa");
    expect(theme.foreground).toBe("#18181b");
    for (const key of ANSI_KEYS) {
      expect(theme[key], `light theme missing ${key}`).toBeTruthy();
    }
  });

  it("Given a live bg/fg override, Then it follows the app surface but keeps the ANSI palette", () => {
    const theme = resolveTerminalTheme(true, "#abcdef", "#123456");
    expect(theme.background).toBe("#abcdef");
    expect(theme.foreground).toBe("#123456");
    expect(theme.red).toBeTruthy();
  });

  it("Given a blank override, Then it falls back to the palette default", () => {
    const theme = resolveTerminalTheme(false, "", "  ");
    expect(theme.background).toBe("#fafafa");
    expect(theme.foreground).toBe("#18181b");
  });
});
