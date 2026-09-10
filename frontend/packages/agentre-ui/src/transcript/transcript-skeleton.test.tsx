import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { TranscriptSkeleton } from "./transcript-skeleton";

describe("TranscriptSkeleton", () => {
  it("Given 还在等转录, When 渲染, Then 先把行的位置占住", () => {
    render(<TranscriptSkeleton />);

    const skeleton = screen.getByTestId("transcript-skeleton");
    expect(skeleton.children.length).toBeGreaterThan(1);
  });

  it("Given 骨架, When 渲染, Then 每条都在脉冲，且 reduced motion 下不动", () => {
    render(<TranscriptSkeleton />);

    const rows = Array.from(
      screen.getByTestId("transcript-skeleton").children,
    ) as HTMLElement[];
    for (const row of rows) {
      expect(row.className).toMatch(/(^|\s)animate-pulse(\s|$)/);
      expect(row.className).toMatch(/(^|\s)motion-reduce:animate-none(\s|$)/);
    }
  });

  it("Given 骨架, When 渲染, Then 逐条淡下去，读作「还在往下长」而不是「就这些」", () => {
    render(<TranscriptSkeleton />);

    const rows = Array.from(
      screen.getByTestId("transcript-skeleton").children,
    ) as HTMLElement[];
    const opacities = rows.map((row) => Number(row.style.opacity));
    for (let i = 1; i < opacities.length; i += 1) {
      expect(opacities[i]).toBeLessThan(opacities[i - 1]);
    }
  });

  it("Given 骨架, When 读屏读到这里, Then 它整块隐身 —— 「下面还会变」由滚动带的 busy 语义说", () => {
    render(<TranscriptSkeleton />);

    expect(screen.getByTestId("transcript-skeleton")).toHaveAttribute(
      "aria-hidden",
      "true",
    );
  });
});
