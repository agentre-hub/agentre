import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { OrgAgentRow } from "./org-agent-row";
import type { OrgIndexRow } from "./org-index-model";

const row = { agent: { id: 7, name: "Eva" } } as OrgIndexRow;

function renderRow(gap?: string) {
  return render(
    <OrgAgentRow
      row={row}
      indent={0}
      selected={false}
      onSelect={vi.fn()}
      gap={gap}
    />,
  );
}

describe("OrgAgentRow gap", () => {
  it("给了 gap：画 6px status-waiting 点，可读名带原因", () => {
    const { container } = renderRow("Cannot chat yet");
    const dot = container.querySelector("[data-gap-dot]")!;
    expect(dot).toBeInTheDocument();
    expect(dot).toHaveAttribute("aria-hidden", "true");
    expect(dot.className).toContain("size-1.5");
    expect(dot.className).toContain("bg-status-waiting");
    expect(
      screen.getByRole("button", { name: "Eva, Cannot chat yet" }),
    ).toBeInTheDocument();
  });

  it("没给 gap：无点，可读名仍是名字本身", () => {
    const { container } = renderRow();
    expect(container.querySelector("[data-gap-dot]")).toBeNull();
    expect(screen.getByTestId("org-row-select-7")).not.toHaveAttribute(
      "aria-label",
    );
  });
});

describe('OrgAgentRow 两行档（density="stacked"）', () => {
  const backendRow = {
    agent: {
      id: 7,
      name: "Eva",
      backend: { name: "Claude Code" },
    },
  } as unknown as OrgIndexRow;

  function renderStacked(
    r: OrgIndexRow,
    gap?: string,
    machine: string | undefined = "Mac mini",
  ) {
    return render(
      <OrgAgentRow
        row={r}
        indent={0}
        selected={false}
        onSelect={vi.fn()}
        density="stacked"
        machine={machine}
        gap={gap}
      />,
    );
  }

  it("第二行是「机器 · 后端」，选择按钮命中区 ≥44px", () => {
    renderStacked(backendRow);
    expect(screen.getByTestId("org-row-second-7")).toHaveTextContent(
      "Mac mini · Claude Code",
    );
    expect(screen.getByTestId("org-row-select-7").className).toContain(
      "min-h-11",
    );
  });

  it("宿主不给 machine：第二行只写后端名", () => {
    renderStacked(backendRow, undefined, "");
    expect(screen.getByTestId("org-row-second-7")).toHaveTextContent(
      /^Claude Code$/,
    );
  });

  it("待配置：原因以可见文字替换第二行，点与可读名保留", () => {
    const { container } = renderStacked(backendRow, "Cannot chat yet");
    const second = screen.getByTestId("org-row-second-7");
    expect(second).toHaveTextContent("Cannot chat yet");
    expect(second).not.toHaveTextContent("Mac mini");
    expect(container.querySelector("[data-gap-dot]")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Eva, Cannot chat yet" }),
    ).toBeInTheDocument();
  });

  it("不传 density：没有第二行，行尾仍是后端名", () => {
    render(
      <OrgAgentRow
        row={backendRow}
        indent={0}
        selected={false}
        onSelect={vi.fn()}
      />,
    );
    expect(screen.queryByTestId("org-row-second-7")).toBeNull();
    expect(screen.getByTestId("org-row-tail-7")).toHaveTextContent(
      "Claude Code",
    );
  });
});
