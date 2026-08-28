import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ProjectGroupHeader } from "./project-group-header";

const project = { name: "Acme", color: "agent-2" };

describe("ProjectGroupHeader", () => {
  it("组头的字形与行里那一维同源，只是尺寸档不同（根 24px）", () => {
    render(
      <ProjectGroupHeader
        project={project}
        expanded
        onToggle={vi.fn()}
        attentionCount={0}
        attentionTone={null}
      />,
    );

    const glyph = screen.getByTestId("project-group-glyph");
    expect(glyph.className).toContain("size-6");
    expect(glyph).toHaveAccessibleName("Acme");
  });

  it("子项目降一档，孙项目再降一档 —— 层级靠尺码说，不靠缩进堆", () => {
    const { unmount } = render(
      <ProjectGroupHeader
        project={project}
        depth={1}
        expanded
        onToggle={vi.fn()}
        attentionCount={0}
        attentionTone={null}
      />,
    );
    expect(screen.getByTestId("project-group-glyph").className).toContain(
      "size-4",
    );
    unmount();

    render(
      <ProjectGroupHeader
        project={project}
        depth={2}
        expanded
        onToggle={vi.fn()}
        attentionCount={0}
        attentionTone={null}
      />,
    );
    expect(screen.getByTestId("project-group-glyph").className).toContain(
      "size-3.5",
    );
  });

  it("项目自己的图标由宿主画好递进来（那张注册表不在包里）", () => {
    render(
      <ProjectGroupHeader
        project={project}
        glyph={<svg data-testid="host-icon" />}
        expanded
        onToggle={vi.fn()}
        attentionCount={0}
        attentionTone={null}
      />,
    );

    expect(screen.getByTestId("host-icon")).toBeInTheDocument();
  });

  it("动作在折叠按钮外，点它不折叠这一组", async () => {
    const user = userEvent.setup();
    const onToggle = vi.fn();
    const onMore = vi.fn();
    render(
      <ProjectGroupHeader
        project={project}
        expanded
        onToggle={onToggle}
        attentionCount={0}
        attentionTone={null}
        actions={
          <button type="button" aria-label="More" onClick={onMore}>
            ⋮
          </button>
        }
      />,
    );

    await user.click(screen.getByRole("button", { name: "More" }));
    expect(onMore).toHaveBeenCalledTimes(1);
    expect(onToggle).not.toHaveBeenCalled();
  });

  it("需要关注的条数与随手对话 / 机器组头同一枚记号", () => {
    render(
      <ProjectGroupHeader
        project={project}
        expanded
        onToggle={vi.fn()}
        attentionCount={4}
        attentionTone="running"
      />,
    );

    const mark = screen.getByTestId("project-attention-mark");
    expect(mark).toHaveTextContent("4");
    expect(mark.className).toContain("text-status-running");
  });
});
