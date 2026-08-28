import { describe, expect, it } from "vitest";
import type { TFunction } from "i18next";

import { indexRowFromMeta } from "../session-index/row-model";

/**
 * 会话行的唯一投影。合并前这份逻辑在对话页与项目页各写了一遍（规格问题 2），
 * 两边的 trailingLabel 分支逐字相同 —— 差异全在数据源上，而那正是「同一条会话
 * 在两个页面显示不同」的来源。
 */
const t = ((key: string) => key) as unknown as TFunction;

function row(over: Partial<Parameters<typeof indexRowFromMeta>[0]> = {}) {
  return indexRowFromMeta({
    sessionID: 1,
    title: "重构索引",
    lastMessageAt: Date.now() - 60_000,
    agentStatus: "idle",
    reason: null,
    t,
    ...over,
  });
}

describe("indexRowFromMeta", () => {
  it("Given an empty title, When the row is built, Then it falls back to the shared untitled copy instead of two different ones", () => {
    expect(row({ title: "" }).title).toBe("sessionIndex.untitledSession");
  });

  it("Given no attention reason, When the row is built, Then the raw run state drives the dot", () => {
    expect(row({ agentStatus: "running" }).status).toBe("running");
    expect(row({ agentStatus: "idle" }).status).toBe("idle");
  });

  it("Given an unknown run state, When the row is built, Then it degrades to idle rather than rendering an undefined style", () => {
    expect(row({ agentStatus: "" }).status).toBe("idle");
  });

  it("Given an attention reason, When the row is built, Then it wins over the raw run state", () => {
    // idle + unread 要显示成 waiting（黄），否则未读会话在侧栏和已读的一模一样。
    expect(row({ agentStatus: "idle", reason: "unread" }).status).toBe(
      "waiting",
    );
    expect(row({ agentStatus: "idle", reason: "needs_attention" }).status).toBe(
      "waiting",
    );
    expect(row({ agentStatus: "idle", reason: "bg_running" }).status).toBe(
      "running",
    );
  });

  it("Given a background subagent, When the trailing label is built, Then it says so instead of showing a timestamp", () => {
    // 项目页此前从不传 bgRunning，这一档在那边永远显示相对时间（规格问题 3）。
    // reasonToPillText 走的是模块级 i18n 实例（宿主 setup 固定 en），不是这里传的 t。
    expect(row({ reason: "bg_running" }).trailingLabel).toBe("Background");
  });

  it("Given running or error, When the trailing label is built, Then it is the bare status code (not i18n, same convention as RUNNING/IDLE)", () => {
    expect(row({ agentStatus: "running" }).trailingLabel).toBe("running");
    expect(row({ agentStatus: "error", reason: "error" }).trailingLabel).toBe(
      "error",
    );
  });

  it("Given a waiting row, When the trailing label is built, Then it says why", () => {
    expect(row({ agentStatus: "idle", reason: "unread" }).trailingLabel).toBe(
      "Unread",
    );
  });

  it("Given nothing worth saying, When the trailing label is built, Then it falls back to relative time", () => {
    expect(row({ lastMessageAt: Date.now() - 120_000 }).trailingLabel).toBe(
      "2m",
    );
  });

  it("Given no rank, When the row is built, Then the key is absent rather than set to undefined", () => {
    expect("attentionRank" in row()).toBe(false);
    expect("attentionRank" in row({ attentionRank: "selected" })).toBe(true);
  });

  it("Given no href, When the row is built, Then the key is absent (the desktop opens a tab, there is no address)", () => {
    expect("href" in row()).toBe(false);
    expect(row({ href: "/s/1" }).href).toBe("/s/1");
  });
});
