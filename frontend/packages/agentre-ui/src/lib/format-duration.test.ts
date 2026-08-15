import { describe, it, expect } from "vitest";
import { formatDuration } from "./format-duration";

describe("formatDuration", () => {
  it("sub-second → milliseconds", () => {
    expect(formatDuration(0)).toBe("0ms");
    expect(formatDuration(420)).toBe("420ms");
    expect(formatDuration(999)).toBe("999ms");
  });
  it("seconds → one decimal", () => {
    expect(formatDuration(1000)).toBe("1.0s");
    expect(formatDuration(1200)).toBe("1.2s");
    expect(formatDuration(59900)).toBe("59.9s");
  });
  it("minutes → m s, seconds floored so never 60", () => {
    expect(formatDuration(60000)).toBe("1m 0s");
    expect(formatDuration(63000)).toBe("1m 3s");
    expect(formatDuration(119900)).toBe("1m 59s");
  });
});
