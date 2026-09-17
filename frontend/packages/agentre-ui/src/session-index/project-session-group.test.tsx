import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";

import { ProjectSessionGroup } from "./project-session-group";
import type { SessionRowModel } from "./types";

function session(id: string): SessionRowModel {
  return { id, title: `s-${id}`, status: "idle" } as SessionRowModel;
}

function header({
  expanded,
  toggle,
}: {
  expanded: boolean;
  toggle: () => void;
}) {
  return (
    <button type="button" aria-expanded={expanded} onClick={toggle}>
      Parent
    </button>
  );
}

describe("ProjectSessionGroup", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it("父项目同时有自己的会话和子项目时，自己的会话下沉进可独立折叠的子分组，子项目不跟着收", async () => {
    render(
      <ProjectSessionGroup
        persistenceKey="project:1"
        defaultExpanded
        renderHeader={header}
        ownSessionsName="Agentre"
        sessions={[session("1")]}
        subprojects={<div data-testid="sub-projects" />}
      />,
    );

    expect(screen.getByRole("button", { name: /s-1/ })).toBeInTheDocument();
    const toggle = screen.getByRole("button", {
      name: "Toggle Agentre sessions",
    });
    expect(toggle).toHaveTextContent("1");

    await userEvent.click(toggle);

    expect(screen.queryByRole("button", { name: /s-1/ })).toBeNull();
    expect(screen.getByTestId("sub-projects")).toBeInTheDocument();
    // 子分组的折叠态挂在父组键下，与桌面端此前的 `project:<id>:sessions` 同一个键。
    expect(
      window.localStorage.getItem("agentre.agentExpanded.project:1:sessions"),
    ).toBe("0");
  });

  it("子分组组头的计数可由宿主给总数（首屏只是一页窗口）", () => {
    render(
      <ProjectSessionGroup
        defaultExpanded
        renderHeader={header}
        ownSessionsName="Agentre"
        ownSessionsCount={44}
        sessions={[session("1")]}
        subprojects={<div />}
      />,
    );

    expect(
      screen.getByRole("button", { name: "Toggle Agentre sessions" }),
    ).toHaveTextContent("44");
  });

  it("没有子项目时会话直接挂在项目组下，不多出一行组头", () => {
    render(
      <ProjectSessionGroup
        defaultExpanded
        renderHeader={header}
        ownSessionsName="Agentre"
        sessions={[session("1")]}
      />,
    );

    expect(screen.getByRole("button", { name: /s-1/ })).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Toggle Agentre sessions" }),
    ).toBeNull();
  });

  it("自己没有会话、只有子项目时也不出子分组组头", () => {
    render(
      <ProjectSessionGroup
        defaultExpanded
        renderHeader={header}
        ownSessionsName="Agentre"
        sessions={[]}
        subprojects={<div data-testid="sub-projects" />}
      />,
    );

    expect(screen.getByTestId("sub-projects")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Toggle Agentre sessions" }),
    ).toBeNull();
  });

  it("收起父项目时子项目一起收起", async () => {
    render(
      <ProjectSessionGroup
        defaultExpanded
        renderHeader={header}
        ownSessionsName="Agentre"
        sessions={[session("1")]}
        subprojects={<button type="button">child-project</button>}
      />,
    );

    await userEvent.click(screen.getByRole("button", { name: "Parent" }));

    expect(screen.queryByRole("button", { name: "child-project" })).toBeNull();
  });
});
