import { describe, it, expect } from "vitest";
import {
  formatCompactTurnDuration,
  formatDuration,
  formatTurnDuration,
} from "./format-duration";

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

describe("formatCompactTurnDuration", () => {
  it("Given a duration under a minute, When formatted, Then it is whole seconds without a decimal", () => {
    expect(formatCompactTurnDuration(0)).toBe("0s");
    expect(formatCompactTurnDuration(49_000)).toBe("49s");
    expect(formatCompactTurnDuration(48_600)).toBe("49s");
  });

  it("Given a duration of a minute or more, When formatted, Then minutes and two-digit seconds are joined without a space", () => {
    expect(formatCompactTurnDuration(92_400)).toBe("1m32s");
    expect(formatCompactTurnDuration(725_000)).toBe("12m05s");
    expect(formatCompactTurnDuration(90 * 60_000 + 5000)).toBe("90m05s");
  });

  // 59.6s 四舍五入到 60 —— 不写「60s」，进位成 1m00s。
  it("Given a duration that rounds up to a minute, When formatted, Then it carries into minutes", () => {
    expect(formatCompactTurnDuration(59_600)).toBe("1m00s");
  });

  it("Given an invalid duration, When formatted, Then it is zero", () => {
    expect(formatCompactTurnDuration(Number.NaN)).toBe("0s");
    expect(formatCompactTurnDuration(-5)).toBe("0s");
  });
});
