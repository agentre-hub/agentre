import { describe, expect, it } from "vitest";

import {
  compactTokenCount,
  fitMetaLine,
  normalizeModelName,
  type MetaFitInput,
} from "./meta-fit";

describe("compactTokenCount", () => {
  it("Given a count under a thousand, When compacted, Then it stays as is", () => {
    expect(compactTokenCount(0)).toBe("0");
    expect(compactTokenCount(999)).toBe("999");
  });

  it("Given thousands, When compacted, Then it uses k with one decimal", () => {
    expect(compactTokenCount(1000)).toBe("1.0k");
    expect(compactTokenCount(1630)).toBe("1.6k");
    expect(compactTokenCount(52_380)).toBe("52.4k");
    expect(compactTokenCount(86_400)).toBe("86.4k");
    expect(compactTokenCount(412_000)).toBe("412.0k");
  });

  it("Given millions, When compacted, Then it uses M with one decimal", () => {
    expect(compactTokenCount(1_000_000)).toBe("1.0M");
    expect(compactTokenCount(1_300_000)).toBe("1.3M");
    expect(compactTokenCount(12_340_000)).toBe("12.3M");
  });

  // 999_960 / 1000 四舍五入成 1000.0 —— 「1000.0k」不该出现，进位到 M。
  it("Given a count that rounds up to a thousand k, When compacted, Then it is written in M", () => {
    expect(compactTokenCount(999_960)).toBe("1.0M");
  });
});

describe("normalizeModelName", () => {
  it("Given a vendor prefix, a claude- prefix and a date suffix, When normalized, Then all three are stripped", () => {
    expect(normalizeModelName("anthropic/claude-opus-4-1-20250805")).toBe(
      "opus-4-1",
    );
    expect(normalizeModelName("claude-sonnet-4-5")).toBe("sonnet-4-5");
    expect(normalizeModelName("huu/gpt-5.6-sol")).toBe("gpt-5.6-sol");
  });

  it("Given a custom model name without those patterns, When normalized, Then it is unchanged", () => {
    expect(normalizeModelName("deepseek-reasoner-v3.2-exp-thinking")).toBe(
      "deepseek-reasoner-v3.2-exp-thinking",
    );
    expect(normalizeModelName("")).toBe("");
  });

  it("Given a name that would normalize to nothing, When normalized, Then the original is kept", () => {
    expect(normalizeModelName("anthropic/")).toBe("anthropic/");
  });
});

describe("fitMetaLine", () => {
  // 宽度都已把各段之前的间距算进去。
  const seg: MetaFitInput = {
    available: 0,
    model: 70,
    modelMin: 50,
    fixed: 110,
    duration: 50,
    speed: 60,
    more: 12,
  };

  it("Given room for every segment, When fitted, Then nothing is hidden and no more mark shows", () => {
    expect(fitMetaLine({ ...seg, available: 290 })).toEqual({
      level: 0,
      more: false,
    });
  });

  it("Given room for all but the speed, When fitted, Then tok/s goes first and the more mark shows", () => {
    expect(fitMetaLine({ ...seg, available: 289 })).toEqual({
      level: 1,
      more: true,
    });
    expect(fitMetaLine({ ...seg, available: 242 })).toEqual({
      level: 1,
      more: true,
    });
  });

  it("Given no room for the duration either, When fitted, Then the duration goes next", () => {
    expect(fitMetaLine({ ...seg, available: 241 })).toEqual({
      level: 2,
      more: true,
    });
    expect(fitMetaLine({ ...seg, available: 192 })).toEqual({
      level: 2,
      more: true,
    });
  });

  it("Given no room for the full model name, When fitted, Then the model name is truncated last", () => {
    expect(fitMetaLine({ ...seg, available: 191 })).toEqual({
      level: 3,
      more: true,
    });
    expect(fitMetaLine({ ...seg, available: 10 })).toEqual({
      level: 3,
      more: true,
    });
  });

  // 「›」只说「藏了或截了东西」：这一轮本来就没有 tok/s，让到第 2 档时被藏的只有用时。
  it("Given a turn without speed, When the duration has to go, Then it skips straight to hiding the duration", () => {
    const noSpeed = { ...seg, speed: 0 };
    expect(fitMetaLine({ ...noSpeed, available: 230 })).toEqual({
      level: 0,
      more: false,
    });
    expect(fitMetaLine({ ...noSpeed, available: 229 })).toEqual({
      level: 2,
      more: true,
    });
  });

  it("Given a short model and nothing else to hide, When it still overflows, Then no more mark claims something was hidden", () => {
    expect(
      fitMetaLine({
        ...seg,
        model: 40,
        modelMin: 40,
        duration: 0,
        speed: 0,
        available: 100,
      }),
    ).toEqual({ level: 3, more: false });
  });

  // 没布局过（隐藏的标签页、测试环境）量出来的可用宽度是 0：当作还不知道，不让。
  it("Given an unmeasured available width, When fitted, Then everything shows", () => {
    expect(fitMetaLine({ ...seg, available: 0 })).toEqual({
      level: 0,
      more: false,
    });
  });
});
