import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Skeleton } from "./skeleton";

/**
 * 骨架的那一条占位，合并后的唯一一份。
 *
 * 此前它在两端一共散着九处内联：包内 `TranscriptSkeleton` / `BoardSkeleton` 各一份，
 * agentre-server 的总览、隐私、账户、设备、组织各一份，桌面端 `panel-feedback` 与
 * `exec-target-list` 又两处。九处里桌面端那两处是**分叉的**——用的是 `bg-muted`，
 * 正是 `transcript-skeleton` 抬头判过死刑的那一档（浅色下压在 --background 上几乎
 * 不显影，静止的灰块读起来像渲染坏了）。
 *
 * 所以这条守的不是"好不好看"，是"这个概念只有一份、且是已经定过的那一份"。
 */
describe("Skeleton", () => {
  it("Given 一条占位, When 渲染, Then 取色走 secondary，不是 muted", () => {
    render(<Skeleton data-testid="bar" />);

    const bar = screen.getByTestId("bar");
    expect(bar.className).toMatch(/(^|\s)bg-secondary(\s|$)/);
    expect(bar.className).not.toMatch(/bg-muted/);
  });

  it("Given 一条占位, When 渲染, Then 它在脉冲，且 reduced motion 下停住", () => {
    render(<Skeleton data-testid="bar" />);

    const bar = screen.getByTestId("bar");
    expect(bar.className).toMatch(/(^|\s)animate-pulse(\s|$)/);
    expect(bar.className).toMatch(/(^|\s)motion-reduce:animate-none(\s|$)/);
  });

  it("Given 一条占位, When 渲染, Then 默认对读屏隐藏", () => {
    // 「正在取」由容器上的 aria-busy 说。几条灰条再念一遍只是噪音。
    render(<Skeleton data-testid="bar" />);

    expect(screen.getByTestId("bar").getAttribute("aria-hidden")).toBe("true");
  });

  it("Given 调用方给了自己的面, When 渲染, Then 它盖得掉默认那一层", () => {
    // 看板那张卡片占位要的是卡面 + 描边，不是一条灰杠。tailwind-merge 负责让
    // 后来者赢——不然调用方只能绕开这个件自己拼一遍，副本就是这么长回来的。
    render(
      <Skeleton data-testid="bar" className="rounded-md border bg-card" />,
    );

    const bar = screen.getByTestId("bar");
    expect(bar.className).toMatch(/(^|\s)bg-card(\s|$)/);
    expect(bar.className).not.toMatch(/bg-secondary/);
    expect(bar.className).toMatch(/(^|\s)animate-pulse(\s|$)/);
  });
});
