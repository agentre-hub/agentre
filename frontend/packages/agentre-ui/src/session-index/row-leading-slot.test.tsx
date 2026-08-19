import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { RowLeadingSlot } from "./row-leading-slot";

const agent = { name: "Atlas", color: "agent-3" };
const project = { name: "Agentre", color: "agent-7" };

describe("RowLeadingSlot", () => {
  it("按项目分组时，槽位里是分组没说的那一维：Agent 字形（决策 4）", () => {
    render(<RowLeadingSlot axis="project" agent={agent} project={project} />);

    const slot = screen.getByTestId("row-leading-slot");
    expect(slot.dataset.kind).toBe("agent-avatar");
    expect(screen.getByRole("img", { name: "Atlas" })).toHaveTextContent("A");
  });

  it("按 Agent 分组时，槽位里是项目字形 —— 与它的组头同一枚（决策 4）", () => {
    render(<RowLeadingSlot axis="agent" agent={agent} project={project} />);

    const slot = screen.getByTestId("row-leading-slot");
    expect(slot.dataset.kind).toBe("project-avatar");
    expect(screen.getByRole("img", { name: "Agentre" })).toBeInTheDocument();
  });

  it("按机器分组时也是 Agent 字形：组头说了机器，行首补 Agent（决策 8）", () => {
    render(<RowLeadingSlot axis="machine" agent={agent} project={project} />);

    expect(screen.getByTestId("row-leading-slot").dataset.kind).toBe(
      "agent-avatar",
    );
  });

  it("自由会话在「按 Agent」档下槽位保留、只把字形置灰（决策 4）", () => {
    const { unmount } = render(
      <RowLeadingSlot axis="project" agent={agent} project={project} />,
    );
    const projectSlotClass = screen.getByTestId("row-leading-slot").className;
    unmount();

    render(<RowLeadingSlot axis="agent" agent={agent} project={null} />);

    // 左缘必须对齐：不渲染字形会让这一行比邻居往左缩，整列参差。
    const slot = screen.getByTestId("row-leading-slot");
    expect(slot.dataset.kind).toBe("free-glyph");
    expect(slot.className).toBe(projectSlotClass);
  });

  it("这一维解析不出来时也保留槽位，字形置灰而不是编一个身份", () => {
    render(<RowLeadingSlot axis="project" agent={null} project={project} />);

    const slot = screen.getByTestId("row-leading-slot");
    expect(slot.dataset.kind).toBe("agent-avatar");
    expect(screen.queryByRole("img")).toBeNull();
  });

  it("按时间分组时没有槽位：两维都在第二行里（决策 5）", () => {
    render(<RowLeadingSlot axis="time" agent={agent} project={project} />);

    expect(screen.queryByTestId("row-leading-slot")).toBeNull();
  });

  it("宿主自带头像时用宿主那一枚（桌面端的 AgentAvatar / 项目图标）", () => {
    const { unmount } = render(
      <RowLeadingSlot
        axis="project"
        agent={agent}
        project={project}
        agentGlyph={<span data-testid="host-agent">A</span>}
      />,
    );
    expect(screen.getByTestId("host-agent")).toBeInTheDocument();
    unmount();

    render(
      <RowLeadingSlot
        axis="agent"
        agent={agent}
        project={project}
        projectGlyph={<span data-testid="host-project">P</span>}
      />,
    );
    expect(screen.getByTestId("host-project")).toBeInTheDocument();
  });
});
