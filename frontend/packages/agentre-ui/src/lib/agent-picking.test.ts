import { describe, expect, it } from "vitest";

import { groupAgentsForPicking } from "./agent-picking";

/**
 * 「挑一个 Agent 开新对话」的分组与排序。
 *
 * 这条规则此前在两端各有一份实现，且已经开始漂：
 *   - 桌面端 `command-palette/sources/new-chat-source.tsx` 的 `flattenAgents`：
 *     按 `chattable` 分两组，可对话组里「上次选过的」冒泡到最前，不可对话的带
 *     「需要先配置」次级标题、留下但不可点。
 *   - agentre-server `components/session/newconv/AgentPickList.tsx`：
 *     最近用过 / 可以开 / 现在开不了 三组，跑不了的「不隐藏也不可点」。
 *
 * 判据与顺序是同一条，**呈现不是**（一个冒泡进同一组、一个单列一组带标题），
 * 所以进包的只有这只纯函数，标题与容器留在各自宿主。
 */

type A = { id: string; ok: boolean; pin?: boolean };

const agents: A[] = [
  { id: "a", ok: true },
  { id: "b", ok: true, pin: true },
  { id: "c", ok: false },
  { id: "d", ok: false, pin: true },
  { id: "e", ok: true },
];

const base = {
  key: (a: A) => a.id,
  available: (a: A) => a.ok,
  pinned: (a: A) => !!a.pin,
};

describe("groupAgentsForPicking", () => {
  it("Given 混着能开与开不了的, When 分组, Then 开不了的留下来、排在最后", () => {
    // 「不隐藏」是这条规则的要害：藏起来会让人以为 Agent 丢了。宿主据此渲染成
    // 不可点，而不是不渲染。
    const g = groupAgentsForPicking({ agents, ...base });

    expect(g.available.map((a) => a.id)).toEqual(["b", "a", "e"]);
    expect(g.unavailable.map((a) => a.id)).toEqual(["d", "c"]);
    expect(g.recent).toEqual([]);
  });

  it("Given 置顶的, When 排序, Then 置顶在前、其余保持原顺序", () => {
    // 稳定排序：账号里的顺序本身有意义（用户自己排的），只把置顶提前，不重排其余。
    const g = groupAgentsForPicking({ agents, ...base });
    expect(g.available.map((a) => a.id)).toEqual(["b", "a", "e"]);
  });

  it("Given 最近用过的, When 分组, Then 单独成组并按最近在前", () => {
    // 顺序不重排成账号顺序 —— 「最近用过」的价值就是那个顺序。
    const g = groupAgentsForPicking({
      agents,
      ...base,
      recentKeys: ["e", "a"],
    });

    expect(g.recent.map((a) => a.id)).toEqual(["e", "a"]);
    // 已经进了 recent 的不再出现在 available 里：同一个 Agent 在同一屏出现两次，
    // 读者会以为是两个。
    expect(g.available.map((a) => a.id)).toEqual(["b"]);
  });

  it("Given 最近用过但现在开不了的, When 分组, Then 它落回开不了那组", () => {
    // 「最近用过」不能盖过「现在开不了」：把一个点不动的 Agent 摆在最显眼的第一组，
    // 是把死路放在最前面。
    const g = groupAgentsForPicking({
      agents,
      ...base,
      recentKeys: ["c", "a"],
    });

    expect(g.recent.map((a) => a.id)).toEqual(["a"]);
    expect(g.unavailable.map((a) => a.id)).toEqual(["d", "c"]);
  });

  it("Given recentKeys 里有已经不存在的 id, When 分组, Then 跳过它", () => {
    // 「最近用过」是本地记的，Agent 可能已经被删了。
    const g = groupAgentsForPicking({
      agents,
      ...base,
      recentKeys: ["zzz", "a"],
    });
    expect(g.recent.map((x) => x.id)).toEqual(["a"]);
  });

  it("Given recentKeys 里同一个 id 出现两次, When 分组, Then 只算一次", () => {
    const g = groupAgentsForPicking({
      agents,
      ...base,
      recentKeys: ["a", "a"],
    });
    expect(g.recent.map((x) => x.id)).toEqual(["a"]);
  });

  it("Given 不传 pinned, When 分组, Then 原顺序照旧", () => {
    // web 宿主今天没有「置顶」这回事，不传即可，不必编一个恒 false 的谓词。
    const g = groupAgentsForPicking({
      agents,
      key: base.key,
      available: base.available,
    });
    expect(g.available.map((a) => a.id)).toEqual(["a", "b", "e"]);
    expect(g.unavailable.map((a) => a.id)).toEqual(["c", "d"]);
  });
});
