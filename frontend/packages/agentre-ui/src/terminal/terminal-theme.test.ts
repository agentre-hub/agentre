import { describe, it, expect, afterEach } from "vitest";
import { readTerminalTheme, resolveTerminalTheme } from "./terminal-theme";

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
    expect(theme.background).toBe("#18191b");
    expect(theme.foreground).toBe("#e8e8e9");
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

describe("readTerminalTheme surface", () => {
  const root = document.documentElement;
  afterEach(() => {
    root.classList.remove("dark");
    root.style.removeProperty("--background");
    root.style.removeProperty("--foreground");
    root.style.removeProperty("--sheet");
  });
  const paint = (dark: boolean, sheet: string) => {
    root.classList.toggle("dark", dark);
    root.style.setProperty("--background", "#bbbbbb");
    root.style.setProperty("--foreground", "#222222");
    root.style.setProperty("--sheet", sheet);
  };

  it("Given the default surface, Then the background is --background", () => {
    paint(false, "#ffffff");
    expect(readTerminalTheme().background).toBe("#bbbbbb");
    expect(readTerminalTheme("background").background).toBe("#bbbbbb");
  });

  it("Given the sheet surface in light and dark, Then the background is --sheet and ANSI / cursor / foreground are unchanged", () => {
    for (const [dark, sheet] of [
      [false, "#ffffff"],
      [true, "#1b1c1f"],
    ] as const) {
      paint(dark, sheet);
      const plain = readTerminalTheme();
      const onSheet = readTerminalTheme("sheet");
      expect(onSheet.background).toBe(sheet);
      expect({ ...onSheet, background: plain.background }).toEqual(plain);
    }
  });

  it("Given the sheet surface but no --sheet value, Then it falls back to the palette background", () => {
    root.classList.add("dark");
    expect(readTerminalTheme("sheet").background).toBe("#18191b");
  });
});
