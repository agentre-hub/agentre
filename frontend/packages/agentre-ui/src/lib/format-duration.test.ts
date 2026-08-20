import { describe, it, expect } from "vitest";
import { formatDuration, formatTurnDuration } from "./format-duration";

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

describe("formatTurnDuration", () => {
  it("Given a duration under a minute, When formatted, Then it uses seconds with one decimal and never milliseconds", () => {
    expect(formatTurnDuration(0)).toBe("0.0s");
    expect(formatTurnDuration(800)).toBe("0.8s");
    expect(formatTurnDuration(8600)).toBe("8.6s");
    expect(formatTurnDuration(59900)).toBe("59.9s");
  });

  it("Given a duration of a minute or more, When formatted, Then it uses m s and does not convert to hours", () => {
    expect(formatTurnDuration(60000)).toBe("1m 0s");
    expect(formatTurnDuration(72000)).toBe("1m 12s");
    expect(formatTurnDuration(90 * 60000 + 5000)).toBe("90m 5s");
  });
});
