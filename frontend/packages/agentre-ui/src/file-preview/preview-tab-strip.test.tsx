import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { PreviewTabStrip, type FilePreviewTab } from "./preview-tab-strip";

/**
 * 标签条搬进包之后的契约：它只认 props。store 归宿主，包内既不 import 也不猜——
 * 这份用例把每一个动作钉在「触发了注入的那个回调」上，而不是「store 变成了什么」。
 */
function tab(
  path: string,
  extra: Partial<FilePreviewTab> = {},
): FilePreviewTab {
  return { path, isPreview: false, isPinned: false, ...extra };
}

function renderStrip(
  overrides: Partial<Parameters<typeof PreviewTabStrip>[0]> = {},
) {
  const handlers = {
    onActivate: vi.fn(),
    onPromote: vi.fn(),
    onPin: vi.fn(),
    onClose: vi.fn(),
    onCloseOthers: vi.fn(),
    onCloseAll: vi.fn(),
  };

  render(
    <PreviewTabStrip
      tabs={[tab("docs/a.md"), tab("b.md", { isPreview: true })]}
      activePath="b.md"
      {...handlers}
      {...overrides}
    />,
  );

  return handlers;
}

describe("PreviewTabStrip", () => {
  it("renders one tab per open file, the temporary one in italic and the active one marked", () => {
    renderStrip();

    const strip = screen.getByRole("tablist", { name: "Preview tabs" });
    const tabs = within(strip).getAllByRole("tab");
    expect(tabs).toHaveLength(2);
    // 标签显示文件名（不是整条路径）。
    expect(tabs[0]).toHaveTextContent("a.md");
    expect(tabs[1]).toHaveTextContent("b.md");
    expect(tabs[0]).toHaveAttribute("data-active", "false");
    expect(tabs[1]).toHaveAttribute("data-active", "true");
    // 临时标签的文件名以斜体区分，常驻标签不斜体。
    expect(within(tabs[1]).getByText("b.md")).toHaveClass("italic");
    expect(within(tabs[0]).getByText("a.md")).not.toHaveClass("italic");
  });

  it("renders nothing while fewer than two files are open", () => {
    renderStrip({ tabs: [tab("a.md")], activePath: "a.md" });

    expect(screen.queryByRole("tablist")).toBeNull();
  });

  it("shows the pin marker only on a pinned tab", () => {
    renderStrip({
      tabs: [tab("docs/a.md", { isPinned: true }), tab("b.md")],
    });

    const tabs = screen.getAllByRole("tab");
    expect(
      within(tabs[0]).queryByTestId("preview-tab-pin-icon"),
    ).toBeInTheDocument();
    expect(within(tabs[1]).queryByTestId("preview-tab-pin-icon")).toBeNull();
  });

  it("calls onActivate with the clicked tab's path", async () => {
    const handlers = renderStrip();

    await userEvent.click(screen.getAllByRole("tab")[0]);

    expect(handlers.onActivate).toHaveBeenCalledWith("docs/a.md");
  });

  it("calls onPromote on double click", async () => {
    const handlers = renderStrip();

    await userEvent.dblClick(screen.getAllByRole("tab")[1]);

    expect(handlers.onPromote).toHaveBeenCalledWith("b.md");
  });

  it("calls onClose from the tab's own close button without activating it", async () => {
    const handlers = renderStrip();

    await userEvent.click(
      within(screen.getAllByRole("tab")[0]).getByRole("button", {
        name: "Close Tab",
      }),
    );

    expect(handlers.onClose).toHaveBeenCalledWith("docs/a.md");
    expect(handlers.onActivate).not.toHaveBeenCalled();
  });

  it("calls onPin / onClose / onCloseOthers / onCloseAll from the tab context menu", async () => {
    const handlers = renderStrip();
    const target = screen.getAllByRole("tab")[1];

    await userEvent.pointer({ keys: "[MouseRight]", target });
    // 菜单项集合与次序：固定 / 关闭三项 / 复制相对路径。
    expect(
      screen.getAllByRole("menuitem").map((item) => item.textContent),
    ).toEqual([
      "Pin",
      "Close",
      "Close Others",
      "Close All",
      "Copy relative path",
    ]);
    await userEvent.click(screen.getByRole("menuitem", { name: /^Pin$/ }));
    expect(handlers.onPin).toHaveBeenCalledWith("b.md");

    await userEvent.pointer({ keys: "[MouseRight]", target });
    await userEvent.click(screen.getByRole("menuitem", { name: "Close" }));
    expect(handlers.onClose).toHaveBeenCalledWith("b.md");

    await userEvent.pointer({ keys: "[MouseRight]", target });
    await userEvent.click(
      screen.getByRole("menuitem", { name: "Close Others" }),
    );
    expect(handlers.onCloseOthers).toHaveBeenCalledWith("b.md");

    await userEvent.pointer({ keys: "[MouseRight]", target });
    await userEvent.click(screen.getByRole("menuitem", { name: "Close All" }));
    expect(handlers.onCloseAll).toHaveBeenCalledWith();
  });

  it("labels a pinned tab's context menu with Unpin", async () => {
    renderStrip({
      tabs: [tab("docs/a.md", { isPinned: true }), tab("b.md")],
    });

    await userEvent.pointer({
      keys: "[MouseRight]",
      target: screen.getAllByRole("tab")[0],
    });

    expect(
      screen.getByRole("menuitem", { name: /^Unpin$/ }),
    ).toBeInTheDocument();
  });

  it("lists every tab in the overflow menu and activates the picked one", async () => {
    const handlers = renderStrip();

    await userEvent.click(
      screen.getByRole("button", { name: "Open Tab menu" }),
    );
    const items = await screen.findAllByRole("menuitem");
    expect(items).toHaveLength(2);
    expect(items[0]).toHaveTextContent("a.md");

    await userEvent.click(items[0]);
    expect(handlers.onActivate).toHaveBeenCalledWith("docs/a.md");
  });

  it("moves between tabs with the arrow keys, activating the one focused", async () => {
    const handlers = renderStrip();
    const tabs = screen.getAllByRole("tab");

    // roving tabindex：标签条整体只有活动标签一个 Tab 停靠点。
    expect(tabs.map((el) => el.getAttribute("tabindex"))).toEqual(["-1", "0"]);

    tabs[0].focus();
    await userEvent.keyboard("{ArrowRight}");
    expect(tabs[1]).toHaveFocus();
    expect(handlers.onActivate).toHaveBeenLastCalledWith("b.md");

    // 末尾不回绕。
    handlers.onActivate.mockClear();
    await userEvent.keyboard("{ArrowRight}");
    expect(tabs[1]).toHaveFocus();
    expect(handlers.onActivate).not.toHaveBeenCalled();

    await userEvent.keyboard("{ArrowLeft}");
    expect(tabs[0]).toHaveFocus();
    expect(handlers.onActivate).toHaveBeenLastCalledWith("docs/a.md");
  });

  it("renders the host-injected file identity in tabs and in the overflow menu", async () => {
    renderStrip({
      renderFileIcon: (path, slot) => (
        <span data-testid={`icon-${slot}`} data-path={path} />
      ),
    });

    const tabs = screen.getAllByRole("tab");
    expect(within(tabs[0]).getByTestId("icon-tab")).toHaveAttribute(
      "data-path",
      "docs/a.md",
    );

    await userEvent.click(
      screen.getByRole("button", { name: "Open Tab menu" }),
    );
    const items = await screen.findAllByRole("menuitem");
    expect(within(items[1]).getByTestId("icon-overflow")).toHaveAttribute(
      "data-path",
      "b.md",
    );
  });

  // 钉的是「滚进来的是**活动**标签」：只断言「有人滚过」的话，滚错标签（把用户
  // 从正在看的文件上拽走）照样是绿的。
  it("scrolls the active tab into view and leaves the others alone", () => {
    const scrollIntoView = vi.fn();
    const original = HTMLElement.prototype.scrollIntoView;
    HTMLElement.prototype.scrollIntoView = scrollIntoView;
    try {
      renderStrip();

      const [inactive, active] = screen.getAllByRole("tab");
      expect(active).toHaveAttribute("data-active", "true");
      expect(scrollIntoView).toHaveBeenCalledTimes(1);
      // 滚的是包住标签的那个容器（ref 落在 ContextMenuTrigger 上）。
      const scrolled = scrollIntoView.mock.contexts[0] as HTMLElement;
      expect(scrolled.contains(active)).toBe(true);
      expect(scrolled.contains(inactive)).toBe(false);
    } finally {
      HTMLElement.prototype.scrollIntoView = original;
    }
  });
});
