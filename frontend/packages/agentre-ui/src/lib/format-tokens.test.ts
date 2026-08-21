import { describe, expect, it } from "vitest";

import { formatTokens } from "./format-tokens";

/**
 * token 计数的显示格式。此前桌面端 `chat.tsx` 与 agentre-server 的
 * `lib/sessionView.ts` 各持一份**逐字节相同**的实现——同一个量在两端渲染成同一个
 * 字符串，靠的是有人记得两边一起改。这份用例是那件事的唯一守卫。
 *
 * 注意包内另有两份**格式不同**的同名函数（`engine/llm-provider-models` 的 `128K`
 * 风格、`agent-spawn/card` 的两位小数 `M`），它们是各自展示面的约定，不归这里管。
 */
describe("formatTokens", () => {
  it("Given a count below one thousand, When formatted, Then it renders verbatim", () => {
    expect(formatTokens(0)).toBe("0");
    expect(formatTokens(999)).toBe("999");
  });

  it("Given the k band with a quotient under 100, When formatted, Then it keeps one decimal", () => {
    expect(formatTokens(1000)).toBe("1.0k");
    expect(formatTokens(12_340)).toBe("12.3k");
    expect(formatTokens(41_200)).toBe("41.2k");
    expect(formatTokens(99_949)).toBe("99.9k");
  });

  it("Given the k band with a quotient of 100 or more, When formatted, Then the figure is rounded", () => {
    expect(formatTokens(100_000)).toBe("100k");
    expect(formatTokens(120_500)).toBe("121k");
    expect(formatTokens(159_000)).toBe("159k");
    expect(formatTokens(200_000)).toBe("200k");
    expect(formatTokens(999_000)).toBe("999k");
  });

  it("Given rounding pushes the k figure to a thousand, When formatted, Then it switches to the M band", () => {
    // 这一条是这套格式存在的理由之一："1000k" 在任何输入下都不该被渲染出来。
    expect(formatTokens(999_999)).toBe("1M");
    expect(formatTokens(999_500)).toBe("1M");
    // 边界另一侧：取整后还是 999，仍留在 k 档。
    expect(formatTokens(999_499)).toBe("999k");
  });

  it("Given the M band, When the figure is whole, Then the trailing .0 is dropped", () => {
    expect(formatTokens(1_000_000)).toBe("1M");
    expect(formatTokens(1_500_000)).toBe("1.5M");
    expect(formatTokens(1_200_000)).toBe("1.2M");
    expect(formatTokens(9_900_000)).toBe("9.9M");
  });

  it("Given the M band with a quotient of ten or more, When formatted, Then the figure is rounded", () => {
    expect(formatTokens(10_000_000)).toBe("10M");
    expect(formatTokens(12_400_000)).toBe("12M");
    expect(formatTokens(12_500_000)).toBe("13M");
  });

  it('Given any count across the whole range, When formatted, Then "1000k" never appears', () => {
    for (let n = 0; n <= 10_000_000; n += 9973) {
      expect(formatTokens(n)).not.toBe("1000k");
    }
  });
});
