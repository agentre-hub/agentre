import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { agent_backend_svc } from "../../../../../wailsjs/go/models";
import { OrgIndex, type OrgIndexProps } from "../org-index";
import type { OrgAgent, OrgDepartment } from "../types";

function dept(p: Partial<OrgDepartment> & { id: number }): OrgDepartment {
  return {
    id: p.id,
    name: p.name ?? `d${p.id}`,
    description: "",
    icon: "hammer",
    accentColor: "agent-2",
    parentId: p.parentId ?? 0,
    leadAgentId: p.leadAgentId ?? 0,
    leadAgentName: p.leadAgentName ?? "",
    sortOrder: p.sortOrder ?? 0,
    directAgentCount: 0,
    subdepartmentCount: 0,
    memberCount: p.memberCount ?? 0,
    createtime: 0,
    updatetime: 0,
  } as OrgDepartment;
}

function agent(p: Partial<OrgAgent> & { id: number }): OrgAgent {
  return {
    id: p.id,
    name: p.name ?? `a${p.id}`,
    description: p.description ?? "",
    avatarColor: "agent-1",
    avatarIcon: "",
    avatarDataUrl: "",
    systemBadge: p.systemBadge ?? "",
    departmentId: p.departmentId ?? 0,
    departmentName: "",
    parentAgentId: p.parentAgentId ?? 0,
    parentAgentName: "",
    agentBackendId: p.agentBackendId ?? 0,
    sortOrder: p.sortOrder ?? 0,
    prompt: [],
    skills: [],
    // LoadOrg 每个 Agent 都带这一份（`make(..., 0, n)`，空也是 `[]` 不是 null），
    // 「一档都没有」因此是可信的事实而不是缺省。
    execTargets: p.execTargets ?? [{ id: 1, agentBackendId: 5, skills: [] }],
  } as unknown as OrgAgent;
}

const backends = [
  { id: 5, name: "Claude Code", type: "claude-code" },
  { id: 6, name: "Codex", type: "codex" },
] as agent_backend_svc.BackendItem[];

const departments = [
  dept({
    id: 1,
    name: "工程部",
    sortOrder: 1,
    leadAgentId: 2,
    leadAgentName: "Eva",
  }),
  dept({ id: 2, name: "平台组", parentId: 1, sortOrder: 1 }),
  dept({ id: 3, name: "产品部", sortOrder: 2 }),
];

const agents = [
  agent({ id: 1, name: "CEO 助手", systemBadge: "DEFAULT" }),
  agent({
    id: 2,
    name: "Eva",
    description: "工程总监",
    departmentId: 1,
    sortOrder: 1,
    agentBackendId: 5,
  }),
  agent({ id: 3, name: "Bob", parentAgentId: 2, sortOrder: 1 }),
  agent({
    id: 5,
    name: "Dan",
    departmentId: 1,
    sortOrder: 2,
    agentBackendId: 6,
  }),
  agent({ id: 6, name: "Ann", departmentId: 2, sortOrder: 1 }),
  agent({
    id: 7,
    name: "Doc",
    departmentId: 3,
    sortOrder: 1,
    execTargets: [],
  } as Partial<OrgAgent> & { id: number }),
];

function setup(overrides: Partial<OrgIndexProps> = {}) {
  const props: OrgIndexProps = {
    departments,
    agents,
    backends,
    selected: null,
    onSelect: vi.fn(),
    onMoveAgent: vi.fn(),
    onMoveDepartment: vi.fn(),
    onReorderAgent: vi.fn(),
    onCreateDepartment: vi.fn(),
    onCreateAgent: vi.fn(),
    ...overrides,
  };
  const user = userEvent.setup({ pointerEventsCheck: 0 });
  render(<OrgIndex {...props} />);
  return { props, user };
}

function renderIndex() {
  const props: OrgIndexProps = {
    departments,
    agents,
    backends,
    selected: null,
    onSelect: vi.fn(),
    onMoveAgent: vi.fn(),
    onMoveDepartment: vi.fn(),
    onReorderAgent: vi.fn(),
    onCreateDepartment: vi.fn(),
    onCreateAgent: vi.fn(),
  };
  const view = render(<OrgIndex {...props} />);
  return {
    rerender: (next: OrgAgent[]) =>
      view.rerender(<OrgIndex {...props} agents={next} />),
  };
}

function announcement(): string {
  return screen.getByTestId("org-index-announcer").textContent ?? "";
}

async function pickUp(
  user: ReturnType<typeof userEvent.setup>,
  testId: string,
) {
  const handle = screen.getByTestId(testId);
  handle.focus();
  await user.keyboard(" ");
}

async function moveUntil(
  user: ReturnType<typeof userEvent.setup>,
  pattern: RegExp,
) {
  for (let step = 0; step < 40; step += 1) {
    if (pattern.test(announcement())) return;
    await user.keyboard("{ArrowDown}");
  }
  throw new Error(
    `no drop target matched ${pattern} (last announcement: ${announcement()})`,
  );
}

describe("OrgIndex 一屏之内", () => {
  // 决策 12「筛选不常驻占位」：顶栏只有搜索框与**一个**筛选入口，两维筛选都收在
  // 那个入口里。否决过的正是「常驻两个下拉」——未筛选时它们既不说明用途也占掉一行。
  it("顶栏只有搜索框与一个筛选入口：两维不各占一个常驻下拉", () => {
    setup();

    expect(screen.getByLabelText("Search agents")).toBeInTheDocument();
    expect(screen.getByTestId("org-filter-entry")).toBeInTheDocument();
    expect(screen.queryByTestId("org-filter-backend")).toBeNull();
    expect(screen.queryByTestId("org-filter-reportsTo")).toBeNull();
  });

  it("两维筛选都在那一个入口里：打开就能各选各的", async () => {
    const { user } = setup();

    await user.click(screen.getByTestId("org-filter-entry"));

    expect(
      await screen.findByTestId("org-filter-backend-option-6"),
    ).toBeInTheDocument();
    expect(
      screen.getByTestId("org-filter-reportsTo-option-2"),
    ).toBeInTheDocument();
  });

  it("没筛选时 chip 不占行，命中后长出可清除的 chip", async () => {
    const { user } = setup();

    expect(screen.queryByTestId("org-index-chips")).toBeNull();

    await user.click(screen.getByTestId("org-filter-entry"));
    await user.click(await screen.findByTestId("org-filter-backend-option-6"));

    const chip = await screen.findByRole("button", {
      name: "Clear filter Backend: Codex",
    });
    expect(screen.getByTestId("org-index-chips")).toBeInTheDocument();
    // 只剩用这个后端的那一个 Agent
    expect(screen.getByTestId("org-row-5")).toBeInTheDocument();
    expect(screen.queryByTestId("org-row-2")).toBeNull();

    await user.click(chip);
    expect(screen.queryByTestId("org-index-chips")).toBeNull();
    expect(screen.getByTestId("org-row-2")).toBeInTheDocument();
  });

  it("搜索自己不长 chip：搜索框已经把词摆在那儿了", async () => {
    const { user } = setup();

    await user.type(screen.getByLabelText("Search agents"), "Eva");

    expect(screen.getByTestId("org-row-2")).toBeInTheDocument();
    expect(screen.queryByTestId("org-index-chips")).toBeNull();
  });
});

describe("OrgIndex 组头与行", () => {
  it("组头带 Lead·某某，子部门缩在父部门里", () => {
    setup();

    const header = screen.getByTestId("org-group-1");
    expect(header).toHaveTextContent("工程部");
    expect(header).toHaveTextContent("Lead · Eva");
    expect(screen.getByTestId("org-group-2")).toHaveAttribute(
      "data-depth",
      "1",
    );
    expect(header).toHaveAttribute("data-depth", "0");
  });

  it("空部门照常摆组头", () => {
    setup();

    expect(screen.getByTestId("org-group-3")).toHaveTextContent("产品部");
  });

  it("下属 Agent 与主管平排，行内带 ↳ 主管", () => {
    setup();

    const bob = screen.getByTestId("org-row-3");
    expect(bob).toHaveTextContent("↳ Eva");
    // 平排：下属行不靠缩进表达从属，缩进只跟着所在部门走
    expect(bob).toHaveAttribute("data-indent", "0");
    expect(screen.getByTestId("org-row-2")).toHaveAttribute("data-indent", "0");
    expect(screen.getByTestId("org-row-2")).not.toHaveTextContent("↳");
    // 子部门里的行跟着子部门缩一级（层级只剩「部门套部门」这一种）
    expect(screen.getByTestId("org-row-6")).toHaveAttribute("data-indent", "1");
  });

  it("↳ 主管随数据变化：落定后行内那句跟着新上级走", () => {
    const { rerender } = renderIndex();

    expect(screen.getByTestId("org-row-3")).toHaveTextContent("↳ Eva");

    // 落定后 useOrgData 会重取；这里直接给新的上级，行内那句必须跟着改
    rerender(
      agents.map((a) =>
        a.id === 3
          ? ({ ...a, parentAgentId: 5, parentAgentName: "Eva" } as OrgAgent)
          : a,
      ),
    );

    expect(screen.getByTestId("org-row-3")).toHaveTextContent("↳ Dan");
  });

  it("点行选中 Agent，点组头选中部门，选中态落在行上", async () => {
    const { props, user } = setup({ selected: { kind: "agent", id: 2 } });

    expect(screen.getByTestId("org-row-2")).toHaveAttribute(
      "aria-current",
      "true",
    );
    expect(screen.getByTestId("org-row-3")).not.toHaveAttribute("aria-current");

    await user.click(screen.getByTestId("org-row-select-5"));
    expect(props.onSelect).toHaveBeenCalledWith({ kind: "agent", id: 5 });

    await user.click(screen.getByTestId("org-group-select-3"));
    expect(props.onSelect).toHaveBeenCalledWith({ kind: "department", id: 3 });
  });
});

describe("OrgIndex 组头上的入口与收放", () => {
  // 「新建部门 / 加 Agent」今天只在空态里出现，有了部门之后索引里就没有入口了。
  // 组头的 ＋ 补的正是这个（规格决策 12 也不允许把它顶到顶栏上）。
  it("每个组头上都有「往这个部门加 Agent」，不必回空态去找", async () => {
    const { props, user } = setup();

    await user.click(
      screen.getByRole("button", { name: "Add agent to 工程部" }),
    );

    expect(props.onCreateAgent).toHaveBeenCalledWith(1);
  });

  it("宿主不给这条能力时组头上就没有那个 ＋", () => {
    setup({ onCreateAgent: undefined });

    expect(
      screen.queryByRole("button", { name: "Add agent to 工程部" }),
    ).toBeNull();
  });

  it("收起一个部门：它自己的行与子部门整块收走，组头留在原地", async () => {
    const { user } = setup();

    expect(screen.getByTestId("org-row-2")).toBeInTheDocument();
    expect(screen.getByTestId("org-group-2")).toBeInTheDocument();

    const toggle = screen.getByTestId("org-group-toggle-1");
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    await user.click(toggle);

    expect(screen.getByTestId("org-group-1")).toBeInTheDocument();
    expect(screen.getByTestId("org-group-toggle-1")).toHaveAttribute(
      "aria-expanded",
      "false",
    );
    expect(screen.queryByTestId("org-row-2")).toBeNull();
    // 子部门连同它的行一起收走：只收一层会让「质量」浮在收起的「研发」下面。
    expect(screen.queryByTestId("org-group-2")).toBeNull();
    expect(screen.queryByTestId("org-row-6")).toBeNull();
    // 别的部门不受影响
    expect(screen.getByTestId("org-group-3")).toBeInTheDocument();

    await user.click(screen.getByTestId("org-group-toggle-1"));
    expect(screen.getByTestId("org-row-2")).toBeInTheDocument();
  });
});

describe("OrgIndex 行的密度", () => {
  it("一档执行目标都没有的 Agent，行尾标出「无目标」", () => {
    setup();

    expect(screen.getByTestId("org-row-tail-7")).toHaveTextContent("No target");
    // 有档的那些行尾画的是机器名，不是告警
    expect(screen.queryByTestId("org-row-tail-2")).toBeNull();
  });

  it("索引里的头像是 18px 的小方块，不是详情里那枚 32px", () => {
    setup();

    const avatar = screen
      .getByTestId("org-row-2")
      .querySelector('[role="img"]');
    expect(avatar?.className).toContain("size-4.5");
    expect(avatar?.className).not.toContain("size-8");
  });

  it("索引里的行不带描述第二行", () => {
    setup();

    expect(screen.queryByText("工程总监")).toBeNull();
  });
});

describe("OrgIndex 两种空态", () => {
  it("搜索无结果时说出哪些条件同时生效，并一键清除", async () => {
    const { user } = setup();

    await user.click(screen.getByTestId("org-filter-entry"));
    await user.click(await screen.findByTestId("org-filter-backend-option-6"));
    await user.type(screen.getByLabelText("Search agents"), "zzz");

    expect(await screen.findByText("No agents found")).toBeInTheDocument();
    const conditions = screen.getByTestId("org-index-no-match-conditions");
    expect(conditions).toHaveTextContent("Search: zzz");
    expect(conditions).toHaveTextContent("Backend: Codex");
    expect(screen.queryByTestId("org-group-1")).toBeNull();

    await user.click(screen.getByRole("button", { name: "Clear all filters" }));

    expect(screen.getByTestId("org-group-1")).toBeInTheDocument();
    expect(screen.getByTestId("org-row-2")).toBeInTheDocument();
  });

  it("一个部门都没建时给出路：新建部门 / 添加 Agent", async () => {
    const { props, user } = setup({ departments: [], agents: [agents[0]] });

    expect(screen.getByText("No departments created")).toBeInTheDocument();
    // 系统 Agent 那一行仍在
    expect(screen.getByTestId("org-row-1")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "New Department" }));
    expect(props.onCreateDepartment).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole("button", { name: "Add Agent" }));
    expect(props.onCreateAgent).toHaveBeenCalledTimes(1);
  });

  it("没有可挂载节点时不给「添加 Agent」这条出路", () => {
    setup({ departments: [], agents: [], onCreateAgent: undefined });

    expect(screen.getByText("No departments created")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add Agent" })).toBeNull();
  });
});

describe("OrgIndex 键盘拖拽：拾起 / 移动 / 落下 / 取消", () => {
  it("落在组头上 → moveAgent{ newDepartmentId, newParentAgentId: 0 }", async () => {
    const { props, user } = setup();

    await pickUp(user, "org-row-handle-3");
    expect(announcement()).toContain("Picked up Bob");

    await moveUntil(user, /move into 产品部/);
    await user.keyboard("{Enter}");

    expect(props.onMoveAgent).toHaveBeenCalledWith(3, {
      departmentId: 3,
      parentAgentId: 0,
    });
    expect(props.onReorderAgent).not.toHaveBeenCalled();
  });

  it("落在另一行上 → moveAgent{ newDepartmentId: 0, newParentAgentId }", async () => {
    const { props, user } = setup();

    await pickUp(user, "org-row-handle-5");
    await moveUntil(user, /report to Bob/);
    await user.keyboard("{Enter}");

    expect(props.onMoveAgent).toHaveBeenCalledWith(5, {
      departmentId: 0,
      parentAgentId: 3,
    });
  });

  it("落在行间细线上 → reorderAgents(部门, 上级, 新次序)", async () => {
    const { props, user } = setup();

    await pickUp(user, "org-row-handle-5");
    await moveUntil(user, /place before Eva/);
    await user.keyboard("{Enter}");

    expect(props.onReorderAgent).toHaveBeenCalledWith(1, 0, [5, 2]);
    expect(props.onMoveAgent).not.toHaveBeenCalled();
  });

  it("部门落在部门组头上 → moveDepartment", async () => {
    const { props, user } = setup();

    await pickUp(user, "org-group-handle-2");
    await moveUntil(user, /move into 产品部/);
    await user.keyboard("{Enter}");

    expect(props.onMoveDepartment).toHaveBeenCalledWith(2, 3);
  });

  it("当前落点属于哪一类不只靠颜色：三类各自被读出来", async () => {
    const { user } = setup();
    const seen: string[] = [];

    await pickUp(user, "org-row-handle-5");
    for (let step = 0; step < 12; step += 1) {
      await user.keyboard("{ArrowDown}");
      seen.push(announcement());
    }

    expect(seen.some((s) => s.includes("Reorder"))).toBe(true);
    expect(seen.some((s) => s.includes("Change manager"))).toBe(true);
    expect(seen.some((s) => s.includes("Change department"))).toBe(true);
  });

  it("非法落点保持可见并显示为拒绝态，落下也不发写操作", async () => {
    const { props, user } = setup();

    // Eva 拖到自己的下属 Bob 上会成环
    await pickUp(user, "org-row-handle-2");
    await moveUntil(user, /report to Bob/);

    expect(announcement()).toContain("can't drop");
    expect(announcement()).toContain("own subordinate");
    // 落点没有消失，只是画成拒绝态
    expect(screen.getByTestId("org-row-3")).toHaveAttribute(
      "data-drop-state",
      "invalid",
    );

    await user.keyboard("{Enter}");
    expect(props.onMoveAgent).not.toHaveBeenCalled();
    expect(props.onReorderAgent).not.toHaveBeenCalled();
    // 仍在拖拽中：拒绝不等于取消
    expect(screen.getByTestId("org-row-3")).toHaveAttribute(
      "data-drop-state",
      "invalid",
    );
  });

  it("系统 Agent 那一行也是拒绝态：读得出拒绝的理由", async () => {
    const { props, user } = setup();

    await pickUp(user, "org-row-handle-5");
    await moveUntil(user, /report to CEO/);

    expect(announcement()).toContain("doesn't accept drops");
    expect(screen.getByTestId("org-row-1")).toHaveAttribute(
      "data-drop-state",
      "invalid",
    );

    await user.keyboard("{Enter}");
    expect(props.onMoveAgent).not.toHaveBeenCalled();
  });

  it("部门落到自己的子部门上被拒绝", async () => {
    const { props, user } = setup();

    await pickUp(user, "org-group-handle-1");
    await moveUntil(user, /move into 平台组/);

    expect(announcement()).toContain("can't drop");
    await user.keyboard("{Enter}");
    expect(props.onMoveDepartment).not.toHaveBeenCalled();
  });

  it("Esc 取消，落点全部收起且不发写操作", async () => {
    const { props, user } = setup();

    await pickUp(user, "org-row-handle-5");
    await moveUntil(user, /move into 产品部/);
    await user.keyboard("{Escape}");

    expect(announcement()).toContain("Drag cancelled");
    expect(screen.getByTestId("org-group-3")).not.toHaveAttribute(
      "data-drop-state",
    );
    await user.keyboard("{Enter}");
    expect(props.onMoveAgent).not.toHaveBeenCalled();
  });

  it("系统 Agent 不可拖动：它那一行没有拖拽柄", () => {
    setup();

    expect(screen.queryByTestId("org-row-handle-1")).toBeNull();
    expect(screen.getByTestId("org-row-handle-5")).toBeInTheDocument();
  });
});
