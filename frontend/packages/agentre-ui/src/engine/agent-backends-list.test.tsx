import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { BackendRow } from "./agent-backends-list";
import type { Backend } from "./agent-backends-shared";

/**
 * 行尾三颗纯图标按钮的取色。
 *
 * 「取消测试」的 × 与「删除」的垃圾桶都是单独的图标，不是要读的字：图标留在饱和的
 * 填充色上，`-text` 角色只给文字（docs/design.md §3.5）。两颗并排，各用一种红
 * 就是同一行里出现两个红。
 */
function renderRow(testing: boolean) {
  const backend = {
    id: 1,
    name: "claude",
    type: "claudecode",
    llmProviderActive: true,
    agentCount: 0,
  } as unknown as Backend;

  render(
    <BackendRow
      backend={backend}
      hasLocalDevice
      testing={testing}
      testDisabled={false}
      onTest={vi.fn()}
      onCancelTest={vi.fn()}
      onEdit={vi.fn()}
      onChangeBinding={vi.fn()}
      onDelete={vi.fn()}
    />,
  );
}

describe("BackendRow · 行尾图标按钮", () => {
  it("Given a test in flight, When the row renders, Then the cancel icon takes the same fill red as the delete icon", () => {
    renderRow(true);

    const cancel = screen.getByRole("button", {
      name: "Cancel test for claude",
    });
    const remove = screen.getByRole("button", { name: "Delete claude" });

    expect(remove).toHaveClass("text-status-error");
    expect(cancel).toHaveClass("text-status-error");
    expect(cancel).not.toHaveClass("text-destructive-text");
  });

  it("Given no test in flight, When the row renders, Then the test icon stays neutral", () => {
    renderRow(false);

    expect(
      screen.getByRole("button", { name: "Test connection for claude" }),
    ).toHaveClass("text-muted-foreground");
  });
});

/**
 * 手机逐层页（touch）：390 宽里放不下「名称 + 类型 + 绑定 + 位置」一行再加四颗 26px
 * 键——名称被挤没。触屏排法：整行点进编辑，右侧只留一颗 44px「⋯」，测试 / 换绑 /
 * 删除收进菜单；元数据拆成「绑定」「设备 · 引用数」两行。
 */
function renderTouchRow(over: Partial<Backend> = {}) {
  const handlers = {
    onTest: vi.fn(),
    onCancelTest: vi.fn(),
    onEdit: vi.fn(),
    onChangeBinding: vi.fn(),
    onDelete: vi.fn(),
  };
  const backend = {
    id: 1,
    name: "Claude Code",
    type: "claudecode",
    llmProviderKey: "p-1",
    llmProviderActive: true,
    llmProviderName: "Anthropic",
    llmProviderModel: "Claude Sonnet 4.5",
    deviceId: "d-1",
    deviceName: "书房 Mac mini",
    agentCount: 2,
    ...over,
  } as unknown as Backend;
  render(
    <BackendRow
      backend={backend}
      hasLocalDevice={false}
      testing={false}
      testDisabled={false}
      touch
      {...handlers}
    />,
  );
  return handlers;
}

describe("BackendRow · 触屏排法", () => {
  it("Given touch, When the row renders, Then the name is not squeezed and only one 44px More button sits on the right", () => {
    renderTouchRow();

    // 类型 chip 先让位，名称不再被压到 0 宽
    expect(screen.getByText("Claude Code")).toHaveClass(
      "shrink-0",
      "max-w-full",
    );
    expect(screen.getByTestId("backend-type-chip")).toHaveClass(
      "min-w-0",
      "shrink",
    );
    const buttons = screen.getAllByRole("button");
    const more = screen.getByRole("button", {
      name: "More actions for Claude Code",
    });
    expect(more).toHaveClass("size-11");
    expect(
      screen.queryByRole("button", { name: "Delete Claude Code" }),
    ).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Test connection for Claude Code" }),
    ).toBeNull();
    // 行本身一颗（进编辑）+ ⋯ 一颗
    expect(buttons).toHaveLength(2);
  });

  it("Given touch, When the row body is tapped, Then the editor opens", async () => {
    const user = userEvent.setup();
    const h = renderTouchRow();

    await user.click(screen.getByRole("button", { name: "Edit Claude Code" }));

    expect(h.onEdit).toHaveBeenCalledTimes(1);
  });

  it("Given touch, When the More menu opens, Then test, change binding and delete are its items", async () => {
    const user = userEvent.setup();
    const h = renderTouchRow();

    await user.click(
      screen.getByRole("button", { name: "More actions for Claude Code" }),
    );
    const items = screen.getAllByRole("menuitem").map((i) => i.textContent);
    expect(items).toEqual(["Test Connection", "Change binding", "Delete"]);

    await user.click(screen.getByRole("menuitem", { name: "Delete" }));
    expect(h.onDelete).toHaveBeenCalledTimes(1);
    expect(h.onEdit).not.toHaveBeenCalled();
  });

  it("Given touch, When the row renders, Then binding and device · usage sit on separate lines", () => {
    renderTouchRow();

    const binding = screen.getByTestId("backend-binding");
    const place = screen.getByTestId("backend-place");
    expect(place).toHaveTextContent("书房 Mac mini · 2 agents in use");
    expect(binding.contains(place)).toBe(false);
    expect(place.contains(binding)).toBe(false);
  });

  it("Given touch and an invalid binding, When the row renders, Then the needs-action badge stays next to the name", () => {
    renderTouchRow({ llmProviderActive: false } as Partial<Backend>);

    expect(screen.getByText("Needs action")).toBeInTheDocument();
  });

  it("Given a single agent, When the usage count renders, Then it is singular", () => {
    renderTouchRow({ agentCount: 1 } as Partial<Backend>);

    expect(screen.getByTestId("backend-place")).toHaveTextContent(
      "1 agent in use",
    );
  });
});
