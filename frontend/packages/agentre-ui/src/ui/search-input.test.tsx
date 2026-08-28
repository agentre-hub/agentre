import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { SearchInput } from "./search-input";

/**
 * 搜索框此前在两个宿主里手搓了六遍：图标绝对定位一遍、`pl-8` 一遍、focus 环一遍，
 * 每一处的 class 都差一点点。这里只钉住那几处**差异曾经落在哪儿**的地方。
 */
describe("SearchInput", () => {
  it("Given 一个搜索框, When 打字, Then 值经 onChange 交给调用方", () => {
    const onChange = vi.fn();
    render(<SearchInput value="" onChange={onChange} aria-label="Search" />);

    fireEvent.change(screen.getByRole("searchbox", { name: "Search" }), {
      target: { value: "atlas" },
    });

    expect(onChange).toHaveBeenCalledWith("atlas");
  });

  it("Given 任何一种外观, When 渲染, Then 都带一枚不抢点击的放大镜", () => {
    const { container } = render(
      <SearchInput value="" onChange={vi.fn()} aria-label="Search" />,
    );

    const icon = container.querySelector("svg");
    expect(icon).not.toBeNull();
    // 图标坐在输入框上方：不挡点击，否则点在图标上聚不了焦。
    expect(icon?.getAttribute("class")).toContain("pointer-events-none");
    expect(icon?.getAttribute("aria-hidden")).toBe("true");
  });

  it("Given placeholder 与 aria-label, When 两者都给, Then 各归各位", () => {
    render(
      <SearchInput
        value=""
        onChange={vi.fn()}
        aria-label="Search sessions"
        placeholder="Search…"
      />,
    );

    const box = screen.getByRole("searchbox", { name: "Search sessions" });
    expect(box).toHaveAttribute("placeholder", "Search…");
  });

  it("Given 三种外观, When 分别渲染, Then 各自标好自己是哪一种", () => {
    for (const variant of ["outline", "muted", "bare"] as const) {
      const { container, unmount } = render(
        <SearchInput
          value=""
          onChange={vi.fn()}
          aria-label="Search"
          variant={variant}
        />,
      );
      expect(
        container.querySelector('[data-slot="search-input"]'),
      ).toHaveAttribute("data-variant", variant);
      unmount();
    }
  });

  it("Given 调用方要自己调尺寸, When 传 className, Then 落在外框上而不是被吞掉", () => {
    const { container } = render(
      <SearchInput
        value=""
        onChange={vi.fn()}
        aria-label="Search"
        className="w-[220px]"
      />,
    );

    expect(
      container.querySelector('[data-slot="search-input"]')?.className,
    ).toContain("w-[220px]");
  });
});
