import type {
  AgentStatus as PackageAgentStatus,
  TranscriptBlock,
  TranscriptLocalCommand,
  TranscriptMessage,
} from "@agentre-ai/agentre-ui";
import { describe, expect, it } from "vitest";

import type { LocalCommandEntry } from "@/stores/local-commands-store";
import type { AgentStatus as HostAgentStatus } from "@/stores/types";

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

/**
 * `AgentStatus` 是唯一一处**双向**断言：它在两侧各有一份同形的字符串联合
 * （宿主的 `stores/types.ts` 是领域 token，包里那份是展示投影的键），
 * 刻意不让 store 反向 import UI 包。两份必须逐字相等 —— 任一侧加减一个状态，
 * 包里的 `statusConfig` 就会缺键或多键，这里当场红。
 */
export type AgentStatusMatchesHost = Assert<
  HostAgentStatus extends PackageAgentStatus ? true : false
>;

export type AgentStatusMatchesPackage = Assert<
  PackageAgentStatus extends HostAgentStatus ? true : false
>;

/**
 * 本地 `!command` 条目由宿主的 zustand store 产生，包只按形状把它排进转录行。
 * 断言方向与消息 / 块一致：**宿主的类型可赋值给包 DTO**，宿主往包里传才安全。
 */
export type LocalCommandIsAssignable = Assert<
  LocalCommandEntry extends TranscriptLocalCommand ? true : false
>;

describe("transcript DTO contract", () => {
  it("keeps generated chat_svc models assignable to the shared package DTO", () => {
    // 类型断言在 tsc 阶段生效（见上方 Assert<>）；这里只留一条运行时锚点，
    // 说明这份守卫的失败信号来自类型检查而不是断言表达式。
    const proof: MessageIsAssignable & BlockIsAssignable = true;

    expect(proof).toBe(true);
  });

  it("keeps AgentStatus identical on both sides", () => {
    const proof: AgentStatusMatchesHost & AgentStatusMatchesPackage = true;

    expect(proof).toBe(true);
  });

  it("keeps the host local-command entry assignable to the shared package DTO", () => {
    const proof: LocalCommandIsAssignable = true;

    expect(proof).toBe(true);
  });
});
