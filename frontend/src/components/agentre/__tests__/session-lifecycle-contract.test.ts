import { SessionLifecycle } from "@agentre-hub/agentre-ui";
import {
  SessionLifecycleFailed,
  SessionLifecycleIdle,
  SessionLifecycleInterrupted,
  SessionLifecycleRunning,
} from "@agentre-hub/agentre-wire";
import { describe, expect, it } from "vitest";

/**
 * 守卫：`@agentre-hub/agentre-ui` 里那份会话生命周期词表是**手抄的第二份**。
 *
 * 它不能 import `@agentre-hub/agentre-wire` —— UI 包以 git 子目录 tarball 的形态被
 * agentre-server 消费，抽出来的包里没有兄弟包（见该包的 `src/boundary.test.ts`）。
 * 同一个理由下 `event-kinds.gen.ts` 也是各写一份。
 *
 * 所以漂移只能由**宿主**挡：桌面端同时看得见两个包，是这两份唯一碰头的地方。真理源
 * 是 `internal/pkg/agentruntime/runtimes/remote/wire/wire.go`，Go 生成器把它写进
 * agentre-wire 的 `constants.gen.ts`；wire.go 上加减一个生命周期取值，这里当场红。
 *
 * 断言的是**值**而不是类型：两侧都是字符串字面量，写错一个字母 `tsc` 看不出来，
 * 而那正是会让 `lifecycleToAgentStatus` 静默把 `failed` 判成「闲置」的那种错。
 */
describe("会话生命周期词表：共享包 ↔ wire", () => {
  it("四个取值逐字相同", () => {
    expect(SessionLifecycle).toEqual({
      running: SessionLifecycleRunning,
      idle: SessionLifecycleIdle,
      failed: SessionLifecycleFailed,
      interrupted: SessionLifecycleInterrupted,
    });
  });
});
