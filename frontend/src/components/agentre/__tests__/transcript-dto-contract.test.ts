import type {
  TranscriptBlock,
  TranscriptMessage,
} from "@agentre-ai/agentre-ui";
import { describe, expect, it } from "vitest";

import type { chat_svc } from "../../../../wailsjs/go/models";

/**
 * 守卫：共享包 `@agentre-ai/agentre-ui` 的对话流契约是手写的第二份声明，
 * 后端改了 `chat_svc.ChatMessage` / `ChatBlock` 而包没跟上时必须当场红。
 *
 * 断言方向只有一条：**生成类型可赋值给包 DTO**。反向不成立也不需要成立 ——
 * 生成类是 class（带 wails 注入的 convertValues），多出来的成员不影响赋值，
 * 而桌面端正是把生成对象零转换直接喂进渲染器的那一侧。
 *
 * 这里用条件类型而不是 `const x: DTO = ...`：前者在 `tsc -b` 阶段就失败，
 * 不依赖 vitest 跑到；后者还得造一个完整实例。
 */
type Assert<T extends true> = T;

export type MessageIsAssignable = Assert<
  chat_svc.ChatMessage extends TranscriptMessage ? true : false
>;

export type BlockIsAssignable = Assert<
  chat_svc.ChatBlock extends TranscriptBlock ? true : false
>;

describe("transcript DTO contract", () => {
  it("keeps generated chat_svc models assignable to the shared package DTO", () => {
    // 类型断言在 tsc 阶段生效（见上方 Assert<>）；这里只留一条运行时锚点，
    // 说明这份守卫的失败信号来自类型检查而不是断言表达式。
    const proof: MessageIsAssignable & BlockIsAssignable = true;

    expect(proof).toBe(true);
  });
});
