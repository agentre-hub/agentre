import { describe, expect, it } from "vitest";

import { tokenToCssColor } from "./agent-color";

// 这两条随 tokenToCssColor 从宿主 session-avatar.test.ts 搬来 —— 守卫跟着被守卫的
// 代码走。宿主那份只留 firstLetter / avatarFromMeta（会话头像推导仍归宿主）。
describe("tokenToCssColor", () => {
  it("把 agent token 映射成 css 变量", () => {
    expect(tokenToCssColor("agent-1")).toBe("var(--agent-1)");
    expect(tokenToCssColor("agent-10")).toBe("var(--agent-10)");
    expect(tokenToCssColor("agent-16")).toBe("var(--agent-16)");
  });
  it("空 / 非 agent token → null", () => {
    expect(tokenToCssColor(null)).toBeNull();
    expect(tokenToCssColor(undefined)).toBeNull();
    expect(tokenToCssColor("nope")).toBeNull();
  });
});
