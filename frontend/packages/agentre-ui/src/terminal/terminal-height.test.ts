import { describe, it, expect } from "vitest";
import { computeTerminalHeight } from "./terminal-height";

const base = { cellHeight: 18, minRows: 3, maxRows: 9, paddingPx: 12 };

describe("computeTerminalHeight", () => {
  it("empty/short output clamps up to the min rows (no big void)", () => {
    expect(computeTerminalHeight({ ...base, contentRows: 0 })).toBe(66); // 3*18+12
    expect(computeTerminalHeight({ ...base, contentRows: 2 })).toBe(66);
  });
  it("mid output fits its content", () => {
    expect(computeTerminalHeight({ ...base, contentRows: 5 })).toBe(102); // 5*18+12
  });
  it("long output caps at max rows (then xterm scrolls)", () => {
    expect(computeTerminalHeight({ ...base, contentRows: 9 })).toBe(174); // 9*18+12
    expect(computeTerminalHeight({ ...base, contentRows: 50 })).toBe(174);
  });
});
