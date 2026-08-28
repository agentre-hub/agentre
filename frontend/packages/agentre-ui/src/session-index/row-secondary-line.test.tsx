import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { RowSecondaryLine } from "./row-secondary-line";

const agent = { name: "Atlas", color: "agent-3" };
const project = { name: "Agentre", color: "agent-7" };
const machine = { name: "书房小主机", online: true };

/** 第二行里说了哪几维，按它们出现的先后。 */
function dimensions(): string[] {
  return [...screen.getByTestId("row-secondary-line").children].map(
    (part) => (part as HTMLElement).dataset.dimension ?? "",
  );
}

describe("RowSecondaryLine", () => {
  it("时间轴没有组头也没有行首，三维全落第二行（决策 8）", () => {
    render(
      <RowSecondaryLine
        axis="time"
        agent={agent}
        project={project}
        machine={machine}
      />,
    );

    expect(dimensions()).toEqual(["agent", "project", "machine"]);
    const line = screen.getByTestId("row-secondary-line");
    expect(line.textContent).toContain("Atlas");
    expect(line.textContent).toContain("Agentre");
    expect(line.textContent).toContain("书房小主机");
  });

  it("按项目分组时第二行只说机器：项目在组头、Agent 在行首，重复一遍是浪费一行", () => {
    render(
      <RowSecondaryLine
        axis="project"
        agent={agent}
        project={project}
        machine={machine}
      />,
    );

    expect(dimensions()).toEqual(["machine"]);
  });

  it("按 Agent 分组时同理：组头说 Agent、行首说项目，第二行只剩机器", () => {
    render(
      <RowSecondaryLine
        axis="agent"
        agent={agent}
        project={project}
        machine={machine}
      />,
    );

    expect(dimensions()).toEqual(["machine"]);
  });

  it("按机器分组时第二行只说项目：组头已经说了是哪台机器、在不在线", () => {
    render(
      <RowSecondaryLine
        axis="machine"
        agent={agent}
        project={project}
        machine={{ name: "书房小主机", online: false }}
      />,
    );

    expect(dimensions()).toEqual(["project"]);
    expect(screen.getByTestId("row-secondary-line").textContent).not.toContain(
      "书房小主机",
    );
  });

  it("机器离线时在机器那一维后面跟一段「离线」，而不是让整条行变灰", () => {
    // 本体在 server 上：机器离线只影响能不能发新消息、不影响读。
    render(
      <RowSecondaryLine
        axis="agent"
        agent={agent}
        machine={{ name: "旧笔记本", online: false }}
      />,
    );

    expect(screen.getByTestId("row-secondary-line")).toHaveTextContent(
      "Offline",
    );
  });

  it("在线的机器不带这一段", () => {
    render(<RowSecondaryLine axis="agent" agent={agent} machine={machine} />);

    expect(screen.getByTestId("row-secondary-line")).not.toHaveTextContent(
      "Offline",
    );
  });

  it("自由会话如实写宿主给的那句话并把字形置灰，不留半行空白（决策 7）", () => {
    render(
      <RowSecondaryLine
        axis="time"
        agent={agent}
        project={null}
        freeLabel="随手对话"
      />,
    );

    expect(dimensions()).toEqual(["agent", "project"]);
    expect(screen.getByTestId("row-secondary-line")).toHaveTextContent(
      "随手对话",
    );
  });

  it("项目缺席而宿主没给兜底文案时，那一段整个省略", () => {
    render(<RowSecondaryLine axis="time" agent={agent} project={null} />);

    expect(dimensions()).toEqual(["agent"]);
  });

  it("这一行没什么可说时不画一条空行", () => {
    render(<RowSecondaryLine axis="project" agent={agent} project={project} />);

    expect(screen.queryByTestId("row-secondary-line")).toBeNull();
  });

  it("宿主自带字形时用宿主那一枚", () => {
    render(
      <RowSecondaryLine
        axis="time"
        agent={agent}
        project={project}
        agentGlyph={<span data-testid="host-agent">A</span>}
        projectGlyph={<span data-testid="host-project">P</span>}
      />,
    );

    expect(screen.getByTestId("host-agent")).toBeInTheDocument();
    expect(screen.getByTestId("host-project")).toBeInTheDocument();
  });
});
