import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import * as React from "react";
import { describe, expect, it } from "vitest";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "./dialog";

/**
 * 合并后的唯一一份 Dialog。
 *
 * 此前有三份：桌面端 `src/components/ui/dialog.tsx`、agentre-server 一份、以及
 * 包内 `engine/ui/dialog.tsx`（引擎面板专用）。三份逐行对应，却各自漂了几处 ——
 * 后果不是抽象的：同一个 agentre-server 控制台里，设置页的 provider 弹窗（走包内
 * 那份，遮罩是字面色）和项目设置弹窗（走本地那份，遮罩是 bg-scrim）**压暗的颜色
 * 不一样**，而宿主的调色板 lint 管不到 node_modules，一直是绿的。
 *
 * 这份用例钉住合并时逐处的裁决（spec 2026-08-21-cross-host-ui-alignment 决策 5-8）：
 * 取「更 token 化的那一份」，而不是「某个仓的那一份」。
 */
function openDialog() {
  render(
    <Dialog open>
      <DialogContent>
        <DialogTitle>删除项目</DialogTitle>
        <DialogDescription>此操作不可撤销</DialogDescription>
      </DialogContent>
    </Dialog>,
  );
}

describe("Dialog", () => {
  it("遮罩用 bg-scrim，不用调色板字面色", () => {
    // 三份里只有 agentre-server 那份是 token 化的，桌面端与包内仍是
    // bg-slate-900/25 + dark:bg-black/70。合并取前者。
    openDialog();

    const overlay = document.querySelector('[data-slot="dialog-overlay"]');

    expect(overlay).toHaveClass("bg-scrim");
    expect(overlay?.className).not.toMatch(/bg-slate-|bg-black/);
  });

  it("窄视口下留出左右边距，宽视口下封顶 520", () => {
    // 只有 agentre-server 那份有窄屏兜底——它是浏览器宿主，视口可以窄到
    // 比浮卡还小；桌面端窗口有最小宽度所以没踩到。对桌面端无害，合并保留。
    openDialog();

    const content = document.querySelector('[data-slot="dialog-content"]');

    expect(content).toHaveClass("w-[calc(100%-2rem)]");
    expect(content).toHaveClass("max-w-[520px]");
  });

  it("标题与描述走字号 token，不写字面像素", () => {
    // 合并前 agentre-server 那份写的是字面像素（14 / 11），取值与这两档相同，
    // 但绕开了阶梯：行高只能靠继承，同一档在不同父容器下高矮不一。
    openDialog();

    expect(screen.getByText("删除项目")).toHaveClass("text-sm");
    expect(screen.getByText("此操作不可撤销")).toHaveClass("text-2xs");
  });

  it("关闭按钮的可访问名来自包自己的语言包", () => {
    // 组件进了包，文案就不能再指望宿主的默认 namespace 里有 common.close。
    openDialog();

    expect(screen.getByRole("button", { name: "Close" })).toBeInTheDocument();
  });
});

describe("Dialog · 关闭后焦点回到触发它的控件", () => {
  function Host({
    onCloseAutoFocus,
  }: {
    onCloseAutoFocus?: (e: Event) => void;
  }) {
    const [open, setOpen] = React.useState(false);
    return (
      <>
        <button type="button" onClick={() => setOpen(true)}>
          打开
        </button>
        <Dialog open={open} onOpenChange={setOpen}>
          <DialogContent onCloseAutoFocus={onCloseAutoFocus}>
            <DialogTitle>标题</DialogTitle>
            <DialogDescription>说明</DialogDescription>
          </DialogContent>
        </Dialog>
      </>
    );
  }

  // iOS Safari 点按钮不会让它拿到焦点：activeElement 仍是 body，
  // Radix 没有 Trigger 可回，关闭后焦点就落在 body 上。
  function openLikeIosTap() {
    const opener = screen.getByRole("button", { name: "打开" });
    fireEvent.pointerDown(opener);
    fireEvent.click(opener);
    return opener;
  }

  it("点按不带焦点的触发钮打开，关闭后焦点回到它", async () => {
    render(<Host />);
    const opener = openLikeIosTap();
    expect(opener).not.toHaveFocus();
    await screen.findByRole("dialog");

    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    expect(opener).toHaveFocus();
  });

  it("宿主在 onCloseAutoFocus 里 preventDefault 就不回焦点", async () => {
    render(<Host onCloseAutoFocus={(e) => e.preventDefault()} />);
    const opener = openLikeIosTap();
    await screen.findByRole("dialog");

    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    expect(opener).not.toHaveFocus();
  });
});
