import { describe, expect, it } from "vitest";

import * as pkg from "../index";

/**
 * `src/index.ts` 是这个包**唯一**的门：没从 barrel 里放出去的东西，消费方
 * （agentre-server 经 git 依赖）根本看不见 —— 而那个失败要等到对面 import 时才炸。
 *
 * 详情顶带是跨仓契约：两端的「四态同一副外壳」是同一副，控制台的草稿态与详情态
 * 因此不会一发消息就换一副头。
 */
describe("会话详情顶带的对外契约", () => {
  it("顶带从 barrel 出得去", () => {
    const missing = ["SessionHeaderBand"].filter((name) => !(name in pkg));

    expect(missing).toEqual([]);
  });
});
