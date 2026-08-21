// 统一会话索引的三件外壳：分组选择器 / 「随手对话」组头 / 行首 14px 槽位。
// 每个 describe 里都带一条**决策守卫** —— 规格
// docs/specs/2026-08-16-unified-chat-index.md 的决策 3 / 4 / 6 各自有一句
// 「不能有什么」，那句话是这些组件存在的理由，光测「渲染出来了」测不到。
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import enCommon from "@/i18n/locales/en";
import zhCommon from "@/i18n/locales/zh-CN";

import { AxisPicker } from "@agentre-ai/agentre-ui";

import { INDEX_AXES } from "@/lib/session-axis";
import { FreeGroupHeader, RowLeadingSlot } from "@agentre-ai/agentre-ui";

import { agentIconNode } from "../primitives";

// Radix 菜单在 happy-dom 中需要关闭 pointerEvents 检查。
function setupUser() {
  return userEvent.setup({ pointerEventsCheck: 0 });
}

describe("AxisPicker", () => {
  it("Given the project axis, When the agent option is picked, Then onChange reports the agent axis", async () => {
    const user = setupUser();
    const onChange = vi.fn();
    render(
      <AxisPicker value="project" axes={INDEX_AXES} onChange={onChange} />,
    );

    await user.click(screen.getByTestId("axis-picker"));
    await user.click(await screen.findByTestId("axis-option-agent"));

    expect(onChange).toHaveBeenCalledWith("agent");
  });

  it("Given the project axis, When the time option is picked, Then onChange reports the time axis", async () => {
    const user = setupUser();
    const onChange = vi.fn();
    render(
      <AxisPicker value="project" axes={INDEX_AXES} onChange={onChange} />,
    );

    await user.click(screen.getByTestId("axis-picker"));
    await user.click(await screen.findByTestId("axis-option-time"));

    expect(onChange).toHaveBeenCalledWith("time");
  });

  it("Given the time axis, When the project option is picked, Then onChange reports the project axis", async () => {
    const user = setupUser();
    const onChange = vi.fn();
    render(<AxisPicker value="time" axes={INDEX_AXES} onChange={onChange} />);

    await user.click(screen.getByTestId("axis-picker"));
    await user.click(await screen.findByTestId("axis-option-project"));

    expect(onChange).toHaveBeenCalledWith("project");
  });

  it("Given the 288px sidebar row, When the picker is rendered, Then it shows only the current value — never the word 'Group' (decision 3)", async () => {
    const user = setupUser();
    render(<AxisPicker value="agent" axes={INDEX_AXES} onChange={vi.fn()} />);

    // 行内像素只够「图标 + 当前值 + chevron」：带上标签这一行就会把「未读 N」
    // chip 挤到第二行。可发现性交给 title，它不占行内像素。
    const trigger = screen.getByTestId("axis-picker");
    expect(trigger.textContent).toBe("Agent");
    expect(trigger.getAttribute("title")).toBeTruthy();

    await user.click(trigger);
    await screen.findByTestId("axis-option-project");
    expect(
      ["project", "agent", "time"].map(
        (axis) => screen.getByTestId(`axis-option-${axis}`).textContent,
      ),
    ).toEqual(["Project", "Agent", "Time"]);
  });

  it("Given the menu is open, When it renders, Then the grouping noun appears as the menu's own label — off the 288px row, where decision 3 has no quarrel with it", async () => {
    const user = setupUser();
    render(<AxisPicker value="project" axes={INDEX_AXES} onChange={vi.fn()} />);

    await user.click(screen.getByTestId("axis-picker"));

    expect(await screen.findByTestId("axis-picker-label")).toHaveTextContent(
      enCommon.sessionIndex.axis.title,
    );
  });

  it("Given both locales, When the axis labels are inspected, Then no label carries the grouping noun while the tooltip still does the explaining (decision 3)", () => {
    for (const locale of [zhCommon, enCommon]) {
      const axis = locale.sessionIndex.axis;
      for (const label of [axis.project, axis.agent, axis.time]) {
        expect(label).not.toMatch(/group/i);
        expect(label).not.toContain("分组");
      }
      expect(axis.title.length).toBeGreaterThan(0);
    }
  });
});

describe("FreeGroupHeader", () => {
  it("Given no free sessions at all, When the header renders, Then it is still there with its ＋ (decision 6)", async () => {
    const user = setupUser();
    const onNewSession = vi.fn();
    render(
      <FreeGroupHeader
        expanded
        onToggle={vi.fn()}
        attentionCount={0}
        attentionTone={null}
        onNewSession={onNewSession}
      />,
    );

    expect(screen.getByTestId("free-group-header")).toBeInTheDocument();
    expect(screen.getByText("Quick chats")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "New quick chat" }));
    expect(onNewSession).toHaveBeenCalledTimes(1);
  });

  it("Given a virtual group with nothing to configure, When the header renders, Then it exposes no ⋮ menu (decision 6)", () => {
    render(
      <FreeGroupHeader
        expanded
        onToggle={vi.fn()}
        attentionCount={1}
        attentionTone="waiting"
        onNewSession={vi.fn()}
      />,
    );

    // 虚拟组没有设置 / 子项目 / 合并 / 删除可言 —— 挂一个菜单上去是骗人。
    // 组头上只该有两个按钮：折叠与 ＋。
    const buttons = screen.getAllByRole("button");
    expect(buttons).toHaveLength(2);
    expect(buttons.map((button) => button.getAttribute("aria-label"))).toEqual([
      null,
      "New quick chat",
    ]);
    // 而且组头里没有任何菜单接线：项目组头的 ⋮ / 右键都是这两个 slot 起头的。
    const header = screen.getByTestId("free-group-header");
    expect(
      header.querySelector(
        "[data-slot='dropdown-menu-trigger'],[data-slot='context-menu-trigger']",
      ),
    ).toBeNull();
  });

  it("Given only unread quick chats, When the header renders, Then its mark is amber like the project header's, not a hardcoded green", () => {
    // 两种组头上的同一枚记号必须同一套规则：一个说真话、另一个写死绿色，
    // 会让「随手对话」这一组的颜色和它自己的行长期对不上。
    render(
      <FreeGroupHeader
        expanded
        onToggle={vi.fn()}
        attentionCount={2}
        attentionTone="waiting"
        onNewSession={vi.fn()}
      />,
    );

    const mark = screen.getByTestId("free-attention-mark");
    expect(mark.className).toMatch(/text-status-waiting-text/);
    expect(mark.className).not.toMatch(/text-status-running/);
  });

  it("Given a collapsed header, When the row is clicked, Then it toggles", async () => {
    const user = setupUser();
    const onToggle = vi.fn();
    render(
      <FreeGroupHeader
        expanded={false}
        onToggle={onToggle}
        attentionCount={0}
        attentionTone={null}
        onNewSession={vi.fn()}
      />,
    );

    const toggle = screen.getByRole("button", { expanded: false });
    await user.click(toggle);

    expect(onToggle).toHaveBeenCalledTimes(1);
  });
});

describe("RowLeadingSlot", () => {
  const agent = { name: "Atlas", color: "agent-3" };
  const project = { name: "Agentre", color: "agent-7", icon: "rocket" };

  it("Given grouping by project, When a row renders, Then the slot carries the agent avatar (decision 4)", () => {
    render(<RowLeadingSlot axis="project" agent={agent} project={project} />);

    const slot = screen.getByTestId("row-leading-slot");
    expect(slot.dataset.kind).toBe("agent-avatar");
    expect(screen.getByRole("img", { name: "Atlas" })).toBeInTheDocument();
  });

  it("Given grouping by agent, When a project-bound row renders, Then the slot carries that project's own icon and color — the same glyph its group header wears (decision 4)", () => {
    // 通用文件夹字形认不出**是哪个项目**：三个项目在行首长一个样，只有颜色不同。
    // 项目自己选的图标就是它在组头上的身份，行里必须是同一枚。
    render(
      <RowLeadingSlot
        axis="agent"
        agent={agent}
        project={project}
        projectGlyph={agentIconNode(project.icon)}
      />,
    );

    const slot = screen.getByTestId("row-leading-slot");
    expect(slot.dataset.kind).toBe("project-avatar");
    const glyph = screen.getByRole("img", { name: "Agentre" });
    // 上色走 css 变量而不是 bg-agent-* 类名：类名要靠宿主的 Tailwind 扫到包源码
    // 才生成得出来，消费方少配一条 content 路径字形就静默变透明。
    expect(glyph.style.backgroundColor).toBe("var(--agent-7)");
    expect(glyph.querySelector("svg")).not.toBeNull();
  });

  it("Given a project that never picked an icon, When its row renders, Then the slot falls back to the project initial, exactly like the group header", () => {
    render(
      <RowLeadingSlot
        axis="agent"
        agent={agent}
        project={{ name: project.name, color: project.color }}
        projectGlyph={agentIconNode("")}
      />,
    );

    expect(screen.getByRole("img", { name: "Agentre" })).toHaveTextContent("A");
  });

  it("Given grouping by agent, When a free session renders, Then the slot stays and only the glyph is muted (decision 4)", () => {
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
    expect(slot.querySelector("svg")?.getAttribute("class")).toContain(
      "text-decorative-foreground",
    );
  });

  it("Given grouping by time, When a row renders, Then the slot is absent because both dimensions live in the two-line row (decision 5)", () => {
    render(<RowLeadingSlot axis="time" agent={agent} project={project} />);

    expect(screen.queryByTestId("row-leading-slot")).toBeNull();
  });
});
