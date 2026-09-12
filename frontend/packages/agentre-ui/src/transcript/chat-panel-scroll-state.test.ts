import { beforeEach, describe, expect, it } from "vitest";

import {
  __resetChatPanelScrollStateForTesting,
  loadTranscriptDraftState,
  nextAutoFollow,
  pruneChatPanelScrollState,
  saveTranscriptDraftState,
} from "./chat-panel-scroll-state";

describe("chat panel tab UI state", () => {
  beforeEach(() => {
    __resetChatPanelScrollStateForTesting();
  });

  it("Given draft state for multiple tabs, When closed tabs are pruned, Then only active tab drafts remain", () => {
    saveTranscriptDraftState("tab-a", "userAsk:req-1", { text: "keep" });
    saveTranscriptDraftState("tab-b", "userAsk:req-1", { text: "drop" });

    pruneChatPanelScrollState(new Set(["tab-a"]));

    expect(
      loadTranscriptDraftState<{ text: string }>("tab-a", "userAsk:req-1"),
    ).toEqual({ text: "keep" });
    expect(
      loadTranscriptDraftState<{ text: string }>("tab-b", "userAsk:req-1"),
    ).toBeNull();
  });
});

// nextAutoFollow 决定流式逐 chunk 是否继续贴底跟随。核心回归点:内容增长把底部推远
// (scrollTop 没变小、又不在底部容差内)时,跟随意图必须保持 —— 否则流式输出会在首帧
// 落后 >32px 后被永久关掉跟随,转录区冻结、最新输出沉到折叠线下(本次修复的 bug)。
describe("nextAutoFollow (streaming stick-to-bottom intent)", () => {
  it("Given following and content grows past the bottom (scrollTop unchanged, not at bottom), Then it KEEPS following", () => {
    // 流式增量:scrollHeight 长高 → distance>32 → atBottom=false,但 scrollTop 没变小。
    expect(
      nextAutoFollow({
        prev: true,
        prevScrollTop: 316,
        scrollTop: 316,
        atBottom: false,
      }),
    ).toBe(true);
  });

  it("Given the user scrolls up (scrollTop decreases) away from bottom, Then it STOPS following", () => {
    expect(
      nextAutoFollow({
        prev: true,
        prevScrollTop: 500,
        scrollTop: 300,
        atBottom: false,
      }),
    ).toBe(false);
  });

  it("Given back at the bottom tolerance, Then it RESUMES following (even if previously off)", () => {
    expect(
      nextAutoFollow({
        prev: false,
        prevScrollTop: 100,
        scrollTop: 980,
        atBottom: true,
      }),
    ).toBe(true);
  });

  it("Given a programmatic pin scrolls DOWN toward the bottom but lands a hair short, Then it KEEPS following (down-scroll never disengages)", () => {
    // anchorTo:end 部分调整:scrollTop 变大(向下)但还差几 px,不该被当成用户上滚而关掉。
    expect(
      nextAutoFollow({
        prev: true,
        prevScrollTop: 79,
        scrollTop: 316,
        atBottom: false,
      }),
    ).toBe(true);
  });

  it("Given not following and the user keeps scrolling up, Then it stays off", () => {
    expect(
      nextAutoFollow({
        prev: false,
        prevScrollTop: 300,
        scrollTop: 120,
        atBottom: false,
      }),
    ).toBe(false);
  });
});
