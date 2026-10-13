import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
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
