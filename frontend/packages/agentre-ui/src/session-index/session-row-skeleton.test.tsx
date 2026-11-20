import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { SessionRowSkeleton } from "./session-row-skeleton";

/**
 * 会话行骨架。
 *
 * server 现有的 `SessionListSkeleton` 不能被共享包反向 import（依赖方向），而两端
 * 各画一份又会漂移，所以这一颗搬进包并复用包内的 `Skeleton` 原语。它替换的是
 * 「加载中…」那种**不占位置**的提示：行落地时列表会跳一下。
 *
 * 整块对读屏隐藏：「正在取」由容器的 `aria-busy` 说一次，几条灰条不必再念。
 */
describe("SessionRowSkeleton", () => {
  it("Given the default, When it renders, Then it holds several placeholder rows hidden from the screen reader", () => {
    const { container } = render(<SessionRowSkeleton />);

    const skeleton = container.querySelector(
      '[data-slot="session-row-skeleton"]',
    );
    expect(skeleton).not.toBeNull();
    expect(skeleton).toHaveAttribute("aria-hidden", "true");
    expect(skeleton!.children.length).toBeGreaterThan(1);
  });

  it("Given a row count, When it renders, Then exactly that many placeholder rows are reserved", () => {
    const { container } = render(<SessionRowSkeleton rows={2} />);

    const skeleton = container.querySelector(
      '[data-slot="session-row-skeleton"]',
    );
    expect(skeleton!.children).toHaveLength(2);
  });

  it("Given the package Skeleton primitive, When it renders, Then the placeholders are that primitive rather than a second skeleton style", () => {
    const { container } = render(<SessionRowSkeleton rows={1} />);

    // Skeleton 原语的三件事之一：它在动。第二份手画的骨架会先丢掉的就是它。
    expect(
      container.querySelectorAll("span.animate-pulse").length,
    ).toBeGreaterThan(0);
  });

  // 2026-11-20 手机审计 15e：手机首页的骨架还是桌面那一档（8px 圆点 + 两条细线、行高
  // 约 40px），真实行是 64px 高、44px 圆角头像、右上角时间——加载完整列表会跳一下。
  // phone 档照 SessionRow variant="phone" 的几何画。
  it("Given the phone variant, When it renders, Then each placeholder row has the phone row geometry: 64px tall, a 44px rounded avatar and a top-right time", () => {
    const { container } = render(
      <SessionRowSkeleton variant="phone" rows={2} />,
    );

    const skeleton = container.querySelector(
      '[data-slot="session-row-skeleton"]',
    )!;
    expect(skeleton).toHaveAttribute("data-variant", "phone");
    const row = skeleton.children[0] as HTMLElement;
    expect(row).toHaveClass("min-h-16", "gap-3");
    const avatar = row.firstElementChild as HTMLElement;
    expect(avatar).toHaveClass("size-11", "rounded-xl");
    expect(
      row.querySelector('[data-slot="session-row-skeleton-time"]'),
    ).not.toBeNull();
  });

  it("Given no variant, When it renders, Then the desktop dot row is unchanged", () => {
    const { container } = render(<SessionRowSkeleton rows={1} />);

    const row = container.querySelector('[data-slot="session-row-skeleton"]')!
      .children[0] as HTMLElement;
    expect(row.firstElementChild).toHaveClass("size-2", "rounded-full");
    expect(
      row.querySelector('[data-slot="session-row-skeleton-time"]'),
    ).toBeNull();
  });
});
