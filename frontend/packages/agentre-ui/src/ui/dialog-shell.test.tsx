import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import {
  DialogShell,
  DialogShellBody,
  DialogShellFooter,
  DialogShellHeader,
  DialogShellSubmit,
} from "./dialog-shell";

/**
 * 弹窗外壳，两端共用那一份。
 *
 * 它原来只住在 `agentre-server`（`src/components/ui/dialog-shell.tsx`），因为
 * 「桌面端没有对应形态」——2026-08-21 那一轮据此把它留在了宿主。这一轮桌面端的项目
 * 弹窗**正要获得**这个形态（保存态在头部、脚部只有「完成」、错误落脚部左侧、窄屏贴底
 * sheet），前提没了，它就该进包（规格 2026-08-22 决策 6）。
 *
 * 这份用例钉的是那七条规范本身。每一条都对着一个具体毛病：
 *
 *  1. 尺寸成阶梯，不由调用点临时塞 className —— 否则确认框和目录选择器一样宽。
 *  2. 窄屏是**基础**样式、`sm:` 才变回浮卡 —— 反过来写会先画一帧浮卡再跳。
 *  3. 只有 body 滚 —— 头脚跟着滚就再也回不到按钮上。
 *  4. 错误落在**脚部左侧**，与按钮同一行 —— 点了按钮的人视线就在那。
 *  5. 主按钮自带 busy —— 「提交中还能再点一次」每个调用点都有一次机会犯。
 *  6. 危险确认是一种**形态**，不是一段文案。
 *  7. 即时保存的弹窗没有「保存」，保存态在头部。
 */

function renderShell(
  props: Partial<React.ComponentProps<typeof DialogShell>> = {},
  body: React.ReactNode = <p>正文</p>,
) {
  return render(
    <DialogShell open onOpenChange={vi.fn()} {...props}>
      <DialogShellHeader title="项目设置" />
      <DialogShellBody>{body}</DialogShellBody>
      <DialogShellFooter>
        <button type="button">完成</button>
      </DialogShellFooter>
    </DialogShell>,
  );
}

const content = () =>
  document.querySelector('[data-slot="dialog-shell-content"]');

describe("DialogShell 的尺寸阶梯", () => {
  it.each([
    ["sm", "sm:max-w-[420px]"],
    ["md", "sm:max-w-[560px]"],
    ["lg", "sm:max-w-[760px]"],
  ] as const)("size=%s 封顶在 %s", (size, cls) => {
    renderShell({ size });
    expect(content()).toHaveClass(cls);
  });

  it("不给 size 时走 md 这一档", () => {
    renderShell();
    expect(content()).toHaveClass("sm:max-w-[560px]");
  });
});

describe("DialogShell 的窄屏形态", () => {
  it("贴底 sheet 是基础样式，浮卡那一档全部挂在 sm: 上", () => {
    renderShell();
    const el = content();
    // 基础层就是 sheet：贴底、满宽、上圆角。
    expect(el).toHaveClass("inset-x-0");
    expect(el).toHaveClass("bottom-0");
    expect(el).toHaveClass("rounded-t-2xl");
    expect(el).toHaveClass("max-h-[90dvh]");
    // 居中浮卡的定位一条都不能落在基础层——否则窄屏会先画一帧浮卡再跳。
    expect(el?.className).not.toMatch(/(^|\s)left-1\/2/);
    expect(el?.className).not.toMatch(/(^|\s)top-1\/2/);
    expect(el).toHaveClass("sm:left-1/2");
    expect(el).toHaveClass("sm:top-1/2");
  });

  it("拖动条只在 sheet 那一档露出来", () => {
    renderShell();
    // 宽屏的浮卡拖不动，画一条只会说谎。
    expect(
      document.querySelector('[data-slot="dialog-shell-grip"]'),
    ).toHaveClass("sm:hidden");
  });
});

describe("DialogShell 只有 body 滚", () => {
  it("body 自己出滚动条，头与脚不跟着滚", () => {
    renderShell();
    const body = document.querySelector('[data-slot="dialog-shell-body"]');
    // min-h-0 不能少：flex 子项默认 min-height:auto，正文一长就把头脚顶出可视区。
    expect(body).toHaveClass("min-h-0");
    expect(body).toHaveClass("flex-1");
    expect(body).toHaveClass("overflow-y-auto");
    expect(
      document.querySelector('[data-slot="dialog-shell-header"]'),
    ).toHaveClass("shrink-0");
    expect(
      document.querySelector('[data-slot="dialog-shell-footer"]'),
    ).toHaveClass("shrink-0");
  });
});

describe("DialogShell 的错误落点", () => {
  it("整窗级错误摆在脚部左侧，且带 role=alert", () => {
    render(
      <DialogShell open onOpenChange={vi.fn()}>
        <DialogShellBody>正文</DialogShellBody>
        <DialogShellFooter error="账号同步版本冲突，请重试">
          <button type="button">完成</button>
        </DialogShellFooter>
      </DialogShell>,
    );
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("账号同步版本冲突，请重试");
    expect(alert.closest('[data-slot="dialog-shell-footer"]')).toBeTruthy();
  });

  it("错误优先于 left：两者都给时只出错误", () => {
    render(
      <DialogShell open onOpenChange={vi.fn()}>
        <DialogShellBody>正文</DialogShellBody>
        <DialogShellFooter error="写失败" left={<span>/srv/work/atlas</span>}>
          <button type="button">完成</button>
        </DialogShellFooter>
      </DialogShell>,
    );
    expect(screen.getByRole("alert")).toHaveTextContent("写失败");
    expect(screen.queryByText("/srv/work/atlas")).toBeNull();
  });
});

describe("DialogShellSubmit 自带 busy", () => {
  it("busy 时禁用且出转圈，点不动", () => {
    const onClick = vi.fn();
    render(
      <DialogShellSubmit busy onClick={onClick}>
        创建
      </DialogShellSubmit>,
    );
    const button = screen.getByRole("button", { name: /创建/ });
    expect(button).toBeDisabled();
    fireEvent.click(button);
    expect(onClick).not.toHaveBeenCalled();
  });

  it("不 busy 时照常可点", () => {
    const onClick = vi.fn();
    render(<DialogShellSubmit onClick={onClick}>创建</DialogShellSubmit>);
    fireEvent.click(screen.getByRole("button", { name: "创建" }));
    expect(onClick).toHaveBeenCalledTimes(1);
  });
});

describe("DialogShell 在写请求飞着的时候不关窗", () => {
  it("busy 期间按 Esc 不触发 onOpenChange", () => {
    const onOpenChange = vi.fn();
    renderShell({ busy: true, onOpenChange });
    fireEvent.keyDown(document.body, { key: "Escape" });
    // 关掉只会让人以为没提交。
    expect(onOpenChange).not.toHaveBeenCalled();
  });
});

describe("DialogShell 的危险形态", () => {
  it("danger 档给头部与浮卡各自的记号，而不是靠一段文案", () => {
    render(
      <DialogShell open onOpenChange={vi.fn()} danger>
        <DialogShellHeader title="删除项目" danger />
        <DialogShellBody>子项目会一并删除</DialogShellBody>
      </DialogShell>,
    );
    expect(content()).toHaveClass("border-destructive/40");
    expect(
      document.querySelector('[data-slot="dialog-shell-header"]'),
    ).toHaveClass("border-destructive/30");
  });
});

describe("DialogShellHeader 的保存态", () => {
  it("idle 时不占位置", () => {
    renderShell();
    expect(
      document.querySelector('[data-slot="dialog-shell-save-state"]'),
    ).toBeNull();
  });

  it.each([
    ["saving", "Saving..."],
    ["saved", "Saved"],
    ["error", "Save failed"],
  ] as const)("saveState=%s 在头部右侧说 %s", (state, copy) => {
    render(
      <DialogShell open onOpenChange={vi.fn()}>
        <DialogShellHeader title="项目设置" saveState={state} />
        <DialogShellBody>正文</DialogShellBody>
      </DialogShell>,
    );
    const el = document.querySelector('[data-slot="dialog-shell-save-state"]');
    expect(el).toHaveTextContent(copy);
    // 静默变化的东西要读屏播报得出来。
    expect(el).toHaveAttribute("aria-live", "polite");
  });

  it("给了 onClose 才画关闭按钮，可访问名走包自己的语言包", () => {
    const onClose = vi.fn();
    render(
      <DialogShell open onOpenChange={vi.fn()}>
        <DialogShellHeader title="项目设置" onClose={onClose} />
        <DialogShellBody>正文</DialogShellBody>
      </DialogShell>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("没给 onClose 就不画关闭按钮", () => {
    // 头部不替调用方决定「这个窗能不能就这么关掉」。
    renderShell();
    expect(screen.queryByRole("button", { name: "Close" })).toBeNull();
  });
});
