import type * as React from "react";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { OrgAgentRow } from "./org/org-agent-row";
import { OrgGroupHeader } from "./org/org-group-header";
import { buildOrgIndex } from "./org/org-index-model";
import { IndexGroupHeader } from "./session-index/group-header";
import { OwnSessionsHeader } from "./session-index/own-sessions-header";
import { SessionFilterChips } from "./session-index/session-filter-chips";
import { SessionRow } from "./session-index/session-row";
import { contrast, fillOn, type TokenScope } from "./styles/__testing__/tokens";

/**
 * 外框（chrome）上那几列的行：悬停与选中必须是两块看得出的面。
 *
 * 选中项一律抬到 `raised`（会话行、组织索引的 Agent 行与部门头）；悬停原来是
 * `sidebar-active-bg`——它是给旧的 `sidebar` 底调的「提亮到白」，落到外框上浅色
 * 正好也是 #ffffff，与 raised 同一个字节（1.00），深色 #292a2e 对 #27282c 也只有
 * 1.03：鼠标停在哪一行，那一行就和选中行长得一样。
 *
 * 这里不断言类名叫什么，而是从渲染出来的 `hover:bg-*` 反查它在外框上合成出的实色，
 * 两个主题各算一遍：
 * - 对外框分得开——不然悬停没有反馈；
 * - 对 raised 分得开——不然悬停读成选中；
 * - 选中比悬停离外框更远——「我选中的」不能比「鼠标碰巧停着的」更弱。
 * 门槛用 SURFACE_MIN（见 tokens.test.ts）：两块不画线的相邻面「静止时认得出是两块」，
 * 与填充对它脚下的面用的是同一条线。
 */
const SURFACE_MIN = 1.08;

const THEMES: Array<[TokenScope, string]> = [
  [":root", "亮色"],
  [".dark", "暗色"],
];

const org = buildOrgIndex({
  agents: [{ id: 2, name: "Eva", departmentId: 1 }],
  departments: [{ id: 1, name: "Engineering", memberCount: 1 }],
  filters: { search: "", backendId: 0, reportsToId: 0 },
});

/** 每一种外框上的行：渲染未选中的那一态，交回挂着底色的那个元素。 */
const ROWS: Array<[string, () => HTMLElement]> = [
  [
    "会话行 SessionRow",
    () => {
      render(<SessionRow status="idle" title="session-1" trailingLabel="5m" />);
      return screen.getByRole("button", { name: /session-1/ });
    },
  ],
  [
    "组织索引的 Agent 行 OrgAgentRow",
    () => {
      const row = org.groups[0].rows[0];
      render(
        <OrgAgentRow
          row={row}
          indent={0}
          selected={false}
          onSelect={vi.fn()}
        />,
      );
      return screen.getByTestId(`org-row-${row.agent.id}`);
    },
  ],
  [
    "组织索引的部门头 OrgGroupHeader",
    () => {
      render(
        <OrgGroupHeader
          group={org.groups[0]}
          selected={false}
          onSelect={vi.fn()}
        />,
      );
      return screen.getByTestId("org-group-1");
    },
  ],
  [
    "会话索引的组头 IndexGroupHeader",
    () => {
      const props: React.ComponentProps<typeof IndexGroupHeader> = {
        expanded: true,
        onToggle: vi.fn(),
        label: "Acme",
        testId: "group-header",
      };
      render(<IndexGroupHeader {...props} />);
      return screen.getByTestId("group-header");
    },
  ],
  [
    "父项目「自己的会话」组头 OwnSessionsHeader",
    () => {
      render(
        <OwnSessionsHeader
          name="Agentre"
          count={1}
          expanded
          onToggle={vi.fn()}
        />,
      );
      return screen.getByRole("button", { name: /Agentre/ });
    },
  ],
];

/** 元素上唯一那条 `hover:bg-<token>`（不带不透明度后缀）的 token 名。 */
function hoverFillOf(el: HTMLElement): string {
  const fills = Array.from(el.classList)
    .map((c) => /^hover:bg-([a-z]+(?:-[a-z]+)*)$/.exec(c)?.[1])
    .filter((name): name is string => name !== undefined);
  expect(fills, `${el.className} 里应当恰好有一条 hover 底色`).toHaveLength(1);
  return fills[0];
}

describe("外框上的行：悬停与选中是两块面", () => {
  it.each(
    THEMES.flatMap(([scope, label]) =>
      ROWS.map(([name, renderRow]) => [label, name, scope, renderRow] as const),
    ),
  )("%s下 %s 的悬停底色", (_label, _name, scope, renderRow) => {
    const fill = hoverFillOf(renderRow());
    const hover = fillOn(fill, scope, "chrome");
    const chrome = fillOn("chrome", scope, "chrome");
    const raised = fillOn("raised", scope, "chrome");
    expect(hover, `bg-${fill} 在 tokens.css 里解析不出颜色`).toBeDefined();

    const onChrome = contrast(hover!, chrome!);
    const fromRaised = contrast(hover!, raised!);
    expect(
      onChrome,
      `悬停 bg-${fill}（${hover}）对外框 ${chrome} 只有 ${onChrome.toFixed(3)}`,
    ).toBeGreaterThanOrEqual(SURFACE_MIN);
    expect(
      fromRaised,
      `悬停 bg-${fill}（${hover}）对选中面 raised ${raised} 只有 ${fromRaised.toFixed(3)}——悬停读成选中`,
    ).toBeGreaterThanOrEqual(SURFACE_MIN);
    expect(
      contrast(raised!, chrome!),
      `选中面 raised 离外框应当比悬停 bg-${fill} 更远`,
    ).toBeGreaterThan(onChrome);
  });
});

/** 元素上唯一那条静止态 `bg-<token>`（不带变体前缀、不带不透明度后缀）的 token 名。 */
function restingFillOf(el: HTMLElement): string {
  const fills = Array.from(el.classList)
    .map((c) => /^bg-([a-z]+(?:-[a-z]+)*)$/.exec(c)?.[1])
    .filter((name): name is string => name !== undefined);
  expect(fills, `${el.className} 里应当恰好有一条静止底色`).toHaveLength(1);
  return fills[0];
}

/**
 * 会话索引顶栏的筛选 chips 也坐在外框上（`IndexToolbar` 在 `ResizableSidebar` 里）。
 *
 * 没按下的 chip 原来静止就是 `sidebar-active-bg`：浅色与选中行的 raised 同一个字节
 * （1.00），深色对按下那颗的 primary-soft 只有 1.05——一排 chip 看着像一排选中项，
 * 按下与没按下只剩字色之差；悬停也不换底，只换字色。
 *
 * 三态各是一块面：静止对外框、对 raised 分得开；按下对静止分得开；悬停换一档底色，
 * 且不读成选中。
 */
describe("外框上的筛选 chips：静止、悬停、按下是三块面", () => {
  it.each(THEMES)("%s下的三态底色", (scope) => {
    render(
      <SessionFilterChips value="running" unreadCount={0} onChange={vi.fn()} />,
    );
    const idle = screen.getByTestId("filter-chip-all");
    const pressed = screen.getByTestId("filter-chip-running");
    const on = (name: string) => {
      const color = fillOn(name, scope, "chrome");
      expect(color, `bg-${name} 在 tokens.css 里解析不出颜色`).toBeDefined();
      return color!;
    };
    const chrome = on("chrome");
    const raised = on("raised");

    const restName = restingFillOf(idle);
    const rest = on(restName);
    const pressedName = restingFillOf(pressed);

    expect(
      contrast(rest, chrome),
      `静止 bg-${restName}（${rest}）对外框 ${chrome}`,
    ).toBeGreaterThanOrEqual(SURFACE_MIN);
    expect(
      contrast(rest, raised),
      `静止 bg-${restName}（${rest}）对选中面 raised ${raised}——没按下的 chip 读成选中`,
    ).toBeGreaterThanOrEqual(SURFACE_MIN);
    expect(
      contrast(on(pressedName), rest),
      `按下 bg-${pressedName} 对静止 bg-${restName}`,
    ).toBeGreaterThanOrEqual(SURFACE_MIN);

    const hoverName = hoverFillOf(idle);
    const hover = on(hoverName);
    expect(
      contrast(hover, rest),
      `悬停 bg-${hoverName} 与静止 bg-${restName} 是同一块面`,
    ).toBeGreaterThan(1);
    expect(
      contrast(hover, raised),
      `悬停 bg-${hoverName}（${hover}）对选中面 raised ${raised}——悬停读成选中`,
    ).toBeGreaterThanOrEqual(SURFACE_MIN);
  });
});
