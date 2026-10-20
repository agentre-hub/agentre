import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { TooltipProvider } from "@agentre-hub/agentre-ui";

import { TabTooltip } from "../tab-tooltip";
import { edgeClasses } from "@/__tests__/helpers/edge-classes";

// Radix Tooltip 在 jsdom 中需要关闭 pointerEvents 检查;tests 把 delayDuration
// 置 0 以便 hover 即开,断言用 waitFor 等 Radix 内部状态切换。
// Radix Tooltip 会同时渲染可见 tooltip + 一份 aria 隐藏的副本(供 screen reader),
// 所以用 *AllBy* 查询断言数量,而不是 *By*。
function setupUser() {
  return userEvent.setup({ pointerEventsCheck: 0 });
}

type Props = Partial<React.ComponentProps<typeof TabTooltip>>;

/** 悬停卡由 Tab 条那一个 TooltipProvider 统一管延迟，单测里补一个。 */
function renderTooltip(props: Props = {}) {
  return render(
    <TooltipProvider>
      <TabTooltip
        title="arch-review · jwt 模型"
        projectChain={null}
        projectColor={null}
        status="idle"
        worktreeBranch={null}
        keyboardIndex={null}
        delayDuration={0}
        {...props}
      >
        <button type="button">tab</button>
      </TabTooltip>
    </TooltipProvider>,
  );
}

async function open(text: string) {
  const user = setupUser();
  await user.hover(screen.getByRole("button", { name: "tab" }));
  await waitFor(() => {
    expect(screen.queryAllByText(text).length).toBeGreaterThan(0);
  });
}

describe("TabTooltip", () => {
  it("hover 后弹出, 显示标题与项目链", async () => {
    renderTooltip({
      projectChain: ["Agentre", "backend"],
      projectColor: "var(--agent-2)",
    });
    expect(screen.queryAllByText("Agentre / backend")).toHaveLength(0);

    await open("Agentre / backend");

    expect(
      screen.queryAllByText("arch-review · jwt 模型").length,
    ).toBeGreaterThan(0);
  });

  it("标题写全文，最多 3 行，不再截成一行 —— 悬停最想看的就是全文", async () => {
    const long =
      "调研一下 https://github.com/opskat/opskat/issues/329 能否支持 Oracle 数据库，怎么支持";
    renderTooltip({ title: long });

    await open(long);

    const title = screen.getAllByText(long)[0];
    expect(title).toHaveClass("line-clamp-3");
    expect(title).not.toHaveClass("truncate");
  });

  it("projectColor 给定时 Folder 图标着色", async () => {
    renderTooltip({
      projectChain: ["Agentre"],
      projectColor: "rgb(91, 141, 239)",
    });

    await open("Agentre");

    const folders = screen.getAllByTestId("tooltip-folder-icon");
    expect(folders[0].getAttribute("style") ?? "").toContain(
      "rgb(91, 141, 239)",
    );
  });

  it("无 projectChain 时那一行写「没有项目」，不画文件夹图标", async () => {
    renderTooltip({ title: "CEO 助手 · 周报" });

    await open("CEO 助手 · 周报");

    expect(screen.queryAllByTestId("tooltip-folder-icon")).toHaveLength(0);
    expect(screen.queryAllByText("No project").length).toBeGreaterThan(0);
  });

  it("状态与时间是本地化的文字，有 worktree 才写分支；不再写会话号和操作提示", async () => {
    renderTooltip({
      status: "running",
      worktreeBranch: "feat/jwt-rs256",
      lastMessageAt: Date.now() - 5 * 60_000,
    });

    await open("Running");

    // 英文是紧凑写法；中文是「5 分钟前」（共享包 relativeTime.*）。
    expect(screen.queryAllByText("5m ago").length).toBeGreaterThan(0);
    expect(screen.queryAllByText("feat/jwt-rs256").length).toBeGreaterThan(0);
    const card = screen
      .getAllByText("Running")[0]
      .closest("[data-slot=tab-hover-card]") as HTMLElement;
    expect(card).not.toBeNull();
    expect(card.textContent).not.toMatch(/sess-/);
    expect(card.textContent).not.toMatch(/\b(idle|running)\b/);
    expect(card.textContent).not.toMatch(/Activate/);
  });

  it("没有 worktree 时不写分支那一段", async () => {
    renderTooltip({ status: "idle" });

    await open("Idle");

    expect(screen.queryAllByTestId("tab-tooltip-branch")).toHaveLength(0);
  });
});

// ─── 2026-08-23 对话页外壳收口 · 悬浮浮窗的状态点回归共享原语 ────────────────

describe("TabTooltip · 状态点来自共享 statusConfig", () => {
  it.each([
    ["idle", "bg-status-idle"],
    ["running", "bg-status-running"],
    ["waiting", "bg-status-waiting"],
    ["error", "bg-status-error"],
  ] as const)(
    "Given status=%s, When 浮窗展开, Then 那枚点的颜色与其余状态记号同源",
    async (status, dotClass) => {
      renderTooltip({ title: "处理周一例会纪要", status });
      const user = setupUser();
      await user.hover(screen.getByRole("button", { name: "tab" }));

      // Radix 同时渲染可见浮窗与一份 aria 副本，取第一枚即可。
      const dots = await screen.findAllByTestId("tab-tooltip-status-dot");
      expect(dots[0]).toHaveClass(dotClass);
    },
  );
});

describe("TabTooltip · 无边框", () => {
  it("Given the tooltip, When it opens, Then the shortcut sits on the project row as a filled key cap and nothing draws a line", async () => {
    renderTooltip({ title: "Fix login flow", keyboardIndex: 1 });

    await open("⌘1");

    // Radix 另渲染一份只给读屏的副本，取可见的那一枚。
    const kbd = screen.getAllByText("⌘1")[0];
    expect(kbd).toHaveClass("bg-button-fill");
    const card = kbd.closest("[data-slot=tab-hover-card]") as HTMLElement;
    expect(card).not.toBeNull();
    // 快捷键和项目同一行（卡片的第一行）。
    expect(card.firstElementChild).toContainElement(kbd);
    expect(edgeClasses(card)).toEqual([]);
  });
});
