import { describe, expect, it } from "vitest";

import { truncateFlashText } from "./agent-backends-utils";

describe("truncateFlashText", () => {
  it("短文本原样返回，truncated=false", () => {
    const r = truncateFlashText("✅ 128ms · pong");
    expect(r.display).toBe("✅ 128ms · pong");
    expect(r.truncated).toBe(false);
    expect(r.full).toBe("✅ 128ms · pong");
  });

  it("超过 80 字时截断 + …，truncated=true，full 保留原文", () => {
    const long = "a".repeat(300);
    const r = truncateFlashText(long);
    expect(r.truncated).toBe(true);
    expect(r.display.endsWith("…")).toBe(true);
    expect(r.display.length).toBeLessThanOrEqual(81); // 80 + …
    expect(r.full).toBe(long);
  });

  it("换行/制表符压成单空格防止 flash 行高被撑起", () => {
    const r = truncateFlashText("line1\nline2\t\tline3");
    expect(r.display).toBe("line1 line2 line3");
  });
});
