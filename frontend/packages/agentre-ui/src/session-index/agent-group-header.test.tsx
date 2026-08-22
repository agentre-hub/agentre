import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { AgentGroupHeader } from "./agent-group-header";

describe("AgentGroupHeader", () => {
  it("Agent 那一档的身份是**那一枚头像**，不是一颗色点", () => {
    render(
      <AgentGroupHeader
        agent={{ name: "Ada", color: "agent-3" }}
        expanded
        onToggle={vi.fn()}
        attentionCount={0}
        attentionTone={null}
      />,
    );

    // 与行首那一槽同一枚记号（AgentAvatar），只是尺寸档不同。
    const avatar = screen.getByTestId("agent-group-avatar");
    expect(avatar.className).toContain("size-6");
    expect(avatar).toHaveAccessibleName("Ada");
    expect(screen.getByText("Ada")).toBeInTheDocument();
  });

  it("没有名字的老会话那一组不编一个字母出来，可及名也空着", () => {
    render(
      <AgentGroupHeader
        agent={{ name: "" }}
        label="Unnamed"
        expanded
        onToggle={vi.fn()}
        attentionCount={0}
        attentionTone={null}
      />,
    );

    // 包里那一枚的既定兜底：没有名字就摆「?」，而不是拿组名首字冒充一个身份。
    expect(screen.getByTestId("agent-group-avatar")).toHaveTextContent("?");
    expect(screen.getByText("Unnamed")).toBeInTheDocument();
  });

  it("点组头把折叠交回宿主", async () => {
    const user = userEvent.setup();
    const onToggle = vi.fn();
    render(
      <AgentGroupHeader
        agent={{ name: "Ada" }}
        expanded={false}
        onToggle={onToggle}
        attentionCount={0}
        attentionTone={null}
      />,
    );

    await user.click(screen.getByRole("button", { expanded: false }));
    expect(onToggle).toHaveBeenCalledTimes(1);
  });

  it("需要关注的条数与别的组头同一枚记号", () => {
    render(
      <AgentGroupHeader
        agent={{ name: "Ada" }}
        expanded
        onToggle={vi.fn()}
        attentionCount={2}
        attentionTone="waiting"
      />,
    );

    expect(screen.getByTestId("agent-attention-mark")).toHaveTextContent("2");
  });
});
