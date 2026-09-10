import { describe, expect, it } from "vitest";

import {
  computeAttention,
  indexRowFromMeta,
  reasonToDisplayStatus,
  reasonToPillText,
  strongestAttentionTone,
} from "./attention";

/** 取词器替身：把 key 原样回显，断言因此看得出取的是哪一条，也不依赖资源加载。 */
const echo = (key: string) => key;

describe("computeAttention", () => {
  const base = {
    agentStatus: "idle" as const,
    needsAttention: false,
    lastMessageAt: 0,
    lastReadAt: 0,
  };

  it("待你处理压过一切", () => {
    expect(
      computeAttention({
        ...base,
        needsAttention: true,
        agentStatus: "running",
      }),
    ).toBe("needs_attention");
  });

  it("在跑排在出错之前——跑着的那条随时会自己变，出错的那条不会", () => {
    expect(computeAttention({ ...base, agentStatus: "running" })).toBe(
      "running",
    );
  });

  it("出错要未读才算：读过的那次失败不再拦你", () => {
    expect(
      computeAttention({
        ...base,
        agentStatus: "error",
        lastMessageAt: 2,
        lastReadAt: 1,
      }),
    ).toBe("error");
    expect(
      computeAttention({
        ...base,
        agentStatus: "error",
        lastMessageAt: 1,
        lastReadAt: 2,
      }),
    ).toBeNull();
  });

  it("后台子任务独立于已读未读，但让位 error / running", () => {
    expect(computeAttention({ ...base, bgRunning: true })).toBe("bg_running");
    expect(
      computeAttention({ ...base, agentStatus: "running", bgRunning: true }),
    ).toBe("running");
  });

  it("闲着 + 有新消息没读过 = 未读", () => {
    expect(computeAttention({ ...base, lastMessageAt: 2, lastReadAt: 1 })).toBe(
      "unread",
    );
  });

  it("闲着、读过了 = 不需要关注", () => {
    expect(
      computeAttention({ ...base, lastMessageAt: 1, lastReadAt: 2 }),
    ).toBeNull();
  });
});

describe("reasonToDisplayStatus", () => {
  it("待你处理与未读都画成 waiting 那一档", () => {
    expect(reasonToDisplayStatus("needs_attention", "idle")).toBe("waiting");
    expect(reasonToDisplayStatus("unread", "idle")).toBe("waiting");
  });

  it("后台在跑与自己在跑同一档", () => {
    expect(reasonToDisplayStatus("bg_running", "idle")).toBe("running");
    expect(reasonToDisplayStatus("running", "idle")).toBe("running");
  });

  it("出错就是出错；没有 reason 时退回本来的状态", () => {
    expect(reasonToDisplayStatus("error", "idle")).toBe("error");
    expect(reasonToDisplayStatus(null, "error")).toBe("error");
    expect(reasonToDisplayStatus(null, "idle")).toBe("idle");
  });
});

describe("reasonToPillText", () => {
  it("四种有话说的档各取自己那条文案", () => {
    expect(reasonToPillText("needs_attention", echo)).toBe(
      "attention.needsAttention",
    );
    expect(reasonToPillText("error", echo)).toBe("attention.error");
    expect(reasonToPillText("unread", echo)).toBe("attention.unread");
    expect(reasonToPillText("bg_running", echo)).toBe("attention.background");
  });

  it("在跑与无 reason 没有 pill——行上已经有别的东西在说这件事", () => {
    expect(reasonToPillText("running", echo)).toBeNull();
    expect(reasonToPillText(null, echo)).toBeNull();
  });
});

describe("strongestAttentionTone", () => {
  it("按「谁更需要你动手」排，不沿用 computeAttention 的会话内顺序", () => {
    // 三条在跑盖不住一条出错：组头的记号与它自己的行必须对得上。
    expect(strongestAttentionTone(["running", "running", "error"])).toBe(
      "error",
    );
    expect(strongestAttentionTone(["running", "waiting"])).toBe("waiting");
  });

  it("idle 不参与；一条都没有时没有记号", () => {
    expect(strongestAttentionTone(["idle", "idle"])).toBeNull();
    expect(strongestAttentionTone([])).toBeNull();
  });
});

describe("indexRowFromMeta", () => {
  const base = {
    id: "42",
    title: "重构登录页",
    lastMessageAt: 1_000,
    agentStatus: "idle" as const,
    reason: null,
    relativeTime: (ms: number) => `rel:${ms}`,
    t: echo,
  };

  it("行尾优先说「为什么需要你」，没什么可说时才退回相对时间", () => {
    expect(indexRowFromMeta(base).trailingLabel).toBe("rel:1000");
    expect(
      indexRowFromMeta({ ...base, agentStatus: "error", reason: "error" })
        .trailingLabel,
    ).toBe("error");
    expect(indexRowFromMeta({ ...base, reason: "unread" }).trailingLabel).toBe(
      "attention.unread",
    );
  });

  it("状态由 reason 投影而来，不是原始 agentStatus", () => {
    // 一条闲着但未读的会话，行首那颗点画的是 waiting——「有东西等你看」。
    expect(indexRowFromMeta({ ...base, reason: "unread" }).status).toBe(
      "waiting",
    );
  });

  it("没有 rank 时**不写这个键**", () => {
    expect("attentionRank" in indexRowFromMeta(base)).toBe(false);
    expect(
      indexRowFromMeta({ ...base, attentionRank: "selected" }).attentionRank,
    ).toBe("selected");
  });

  it("标题原样收下——空标题的退化文案是宿主的真相，包不编", () => {
    expect(indexRowFromMeta({ ...base, title: "" }).title).toBe("");
  });
});
