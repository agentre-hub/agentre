import { describe, expect, it } from "vitest";

import { cn } from "@agentre-hub/agentre-ui";

// Task 1 在 globals.css 的 @theme inline 里新增了三个自定义字号 token:
// text-prose / text-aux / text-meta。tailwind-merge 不认识这些自定义类,
// 启发式把它们误判进 text-color 冲突组,导致字号类和真正的文字颜色类
// (如 text-muted-foreground)互斥、二选一被静默丢弃。
//
// 共享 cn()(本文件测试对象)必须把这三个 token 注册成独立的 font-size
// 冲突组,让它们能和颜色类共存,同时仍然与彼此互斥(同组后者覆盖前者)。
describe("cn()", () => {
  it("text-meta 与颜色类共存,不被 twMerge 启发式吃掉", () => {
    const merged = cn("text-meta", "text-muted-foreground");
    expect(merged).toContain("text-meta");
    expect(merged).toContain("text-muted-foreground");
  });

  it("text-aux 与颜色类共存,不被 twMerge 启发式吃掉", () => {
    const merged = cn("text-aux", "text-muted-foreground");
    expect(merged).toContain("text-aux");
    expect(merged).toContain("text-muted-foreground");
  });

  it("text-prose 与颜色类共存,不被 twMerge 启发式吃掉", () => {
    const merged = cn("text-prose", "text-muted-foreground");
    expect(merged).toContain("text-prose");
    expect(merged).toContain("text-muted-foreground");
  });

  it("三个自定义字号 token 仍被当成同一个 font-size 组:同组后者覆盖前者", () => {
    const merged = cn("text-prose", "text-aux");
    expect(merged).not.toContain("text-prose");
    expect(merged).toContain("text-aux");
  });
});
