import { describe, expect, it } from "vitest";

import { SessionLifecycle, lifecycleToAgentStatus } from "./lifecycle-status";

describe("lifecycleToAgentStatus", () => {
  it("等你处理压过一切：那一轮在 daemon 眼里仍是 running，但挡在那儿的是你", () => {
    expect(
      lifecycleToAgentStatus({ lifecycleState: "running", waiting: true }),
    ).toBe("waiting");
    expect(
      lifecycleToAgentStatus({ lifecycleState: "failed", waiting: true }),
    ).toBe("waiting");
    expect(
      lifecycleToAgentStatus({ lifecycleState: "interrupted", waiting: true }),
    ).toBe("waiting");
  });

  it("running 就是在跑", () => {
    expect(lifecycleToAgentStatus({ lifecycleState: "running" })).toBe(
      "running",
    );
  });

  /**
   * 这个包关于「哪一档算出错」的**唯一**答案，也是这个函数存在的理由。
   *
   * `failed` 与 `interrupted` 在 wire 上是两件事（见 remote/wire 生命周期常量那一
   * 段）：前者是「上一轮的结局是错误」，后者是「这条会话此刻接不回实时流」。而
   * daemon 每次启动都按 R10 把**全部非终态会话**整批标成 interrupted —— 把它算成
   * 出错，重启一次两端的列表就整列红着，红色因此变廉价。
   */
  it("红只留给 failed：interrupted 是 daemon 重启后的常态，不是故障", () => {
    expect(lifecycleToAgentStatus({ lifecycleState: "failed" })).toBe("error");
    expect(lifecycleToAgentStatus({ lifecycleState: "interrupted" })).toBe(
      "idle",
    );
  });

  it("idle 与不认识的旧状态一样归闲置——不猜，也不冒充成出错", () => {
    expect(lifecycleToAgentStatus({ lifecycleState: "idle" })).toBe("idle");
    expect(lifecycleToAgentStatus({ lifecycleState: "weird" })).toBe("idle");
    expect(lifecycleToAgentStatus({ lifecycleState: "" })).toBe("idle");
  });

  /**
   * 词表本身也得出得去：宿主要按 `interrupted` 挑自己那句文案（「已中断」与
   * 「空闲」是两句话），照抄一份字面量就是又开一处漂移点。
   */
  it("生命周期词表与 wire 逐字相同", () => {
    expect(SessionLifecycle).toEqual({
      running: "running",
      idle: "idle",
      failed: "failed",
      interrupted: "interrupted",
    });
  });
});
