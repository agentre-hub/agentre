import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { OrgDetailAgent } from "../org-detail-agent";
import type { OrgAgent, OrgDepartment } from "../types";
import type {
  agent_backend_svc,
  agent_svc,
} from "../../../../../wailsjs/go/models";

// 执行目标区只有一个列表：它恒等于这台电脑当前实际的派发顺序（后端解析后的顺序）。
// 重排只写本端顺序覆盖（orderOverride），增删/更换写账号级执行目标集合且**不改动
// 账号顺序**；不存在作用域切换、不存在「恢复为账号默认顺序」、不渲染任何说明行。

const agent = (overrides: Partial<OrgAgent> = {}): OrgAgent =>
  ({
    id: 7,
    name: "Eva",
    description: "工程总监",
    avatarColor: "agent-2",
    avatarDataUrl: "",
    systemBadge: "",
    departmentId: 1,
    parentAgentId: 0,
    agentBackendId: 51,
    sortOrder: 1,
    prompt: [],
    skills: [],
    // 账号级集合的顺序：51 在前。下面的可用性桩返回的本端解析顺序与它相反。
    execTargets: [
      { id: 1, agentBackendId: 51, skills: [] },
      { id: 2, agentBackendId: 52, skills: [] },
    ],
    createtime: 0,
    updatetime: 0,
    ...overrides,
  }) as OrgAgent;

const dept: OrgDepartment = {
  id: 1,
  name: "工程部",
  description: "",
  icon: "hammer",
  accentColor: "agent-2",
  parentId: 0,
  leadAgentId: 0,
  sortOrder: 1,
  directAgentCount: 1,
  subdepartmentCount: 0,
  memberCount: 1,
  createtime: 0,
  updatetime: 0,
} as OrgDepartment;

function backend(id: number, deviceId = ""): agent_backend_svc.BackendItem {
  return {
    id,
    type: "claudecode",
    name: `claude-${id}`,
    llmProviderKey: "",
    llmProviderName: "",
    llmProviderType: "",
    llmProviderModel: "",
    llmProviderActive: false,
    llmModelKey: "",
    cliPath: "",
    modelRoutes: "{}",
    sandbox: "",
    approval: "",
    envJson: "{}",
    reasoningEffort: "",
    defaultPermissionMode: "",
    defaultModel: "",
    openClawGatewayUrl: "",
    openClawAgentId: "",
    openClawDefaultModel: "",
    openClawSessionMode: "",
    hasToken: false,
    deviceId,
    deviceName: "",
    online: false,
    agentCount: 0,
    createtime: 0,
    updatetime: 0,
  } as unknown as agent_backend_svc.BackendItem;
}

// installGo 把 ListAgentExecTargetAvailability 换成给定的实现——后端按**解析后顺序**
// （R14）返回，所以响应数组本身就是本端派发顺序。hasOverride 仍照常返回：界面不再
// 消费它，本文件的断言之一就是「即使服务端说有覆盖，也不出现恢复入口」。
function installGo(listImpl: () => Promise<unknown>) {
  const fn = vi.fn().mockImplementation(listImpl);
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  (window as any).go = {
    app: {
      App: {
        ListAgentExecTargetAvailability: fn,
        GetBackendCapabilities: vi.fn().mockResolvedValue({}),
        ListAgentSkillPacks: vi.fn().mockResolvedValue({}),
      },
    },
  };
  return fn;
}

function availabilityStub(
  items: Array<{ agentBackendId: number; available: boolean }>,
) {
  return installGo(() =>
    Promise.resolve(
      items.map((it) => ({
        reason: "",
        hint: "",
        hasOverride: true,
        ...it,
      })),
    ),
  );
}

afterEach(() => {
  // 先卸载再撤桩：一次「移除档位」在测试结束后还会有一次保存回调引发的重渲染，
  // 此时列表已从多档变回单档，行会重新挂载并再问一次 backend 能力（技能折在行内，
  // 行自己要知道这一档支不支持技能）。撤桩排在卸载之前的话，那一问就打在空的
  // window.go 上。
  cleanup();
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  delete (window as any).go;
});

function renderPanel(
  onUpdate: (req: agent_svc.UpdateAgentRequest) => Promise<unknown>,
  backends: agent_backend_svc.BackendItem[] = [backend(51), backend(52)],
) {
  render(
    <MemoryRouter initialEntries={["/org"]}>
      <OrgDetailAgent
        agent={agent()}
        departments={[dept]}
        agents={[]}
        backends={backends}
        isLeadOf={null}
        availableTools={[]}
        onUpdate={onUpdate}
        onMove={vi.fn().mockResolvedValue(undefined)}
        onDelete={vi.fn().mockResolvedValue(undefined)}
        onUploadAvatar={vi.fn().mockResolvedValue(undefined)}
        onDeleteAvatar={vi.fn().mockResolvedValue(undefined)}
        onClose={vi.fn()}
      />
    </MemoryRouter>,
  );
}

// backendIdsOf 取一次保存请求里账号级执行目标集合的顺序。
function backendIdsOf(req: agent_svc.UpdateAgentRequest): number[] {
  return (req.execTargets ?? []).map((t) => t.agentBackendId);
}

describe("OrgDetailAgent execution targets (single list)", () => {
  it("Given a resolved device order, When the panel opens, Then it renders one list in that order with no scope switcher, no restore action and no explanatory line", async () => {
    availabilityStub([
      { agentBackendId: 52, available: true },
      { agentBackendId: 51, available: true },
    ]);
    renderPanel(vi.fn().mockResolvedValue(undefined));

    // 列表就是解析后的本端顺序：52 在第一行。
    const firstRow = await screen.findByTestId("exec-target-row-0");
    expect(firstRow).toHaveTextContent("claude-52");
    expect(screen.getByTestId("exec-target-row-1")).toHaveTextContent(
      "claude-51",
    );

    expect(screen.queryByRole("button", { name: "This device" })).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Account default" }),
    ).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Restore account default order" }),
    ).toBeNull();
    expect(screen.queryByText(/not synced to other devices/i)).toBeNull();
  });

  it("Given a multi-target list, When a row is moved down, Then only the local order override is written", async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    availabilityStub([
      { agentBackendId: 52, available: true },
      { agentBackendId: 51, available: true },
    ]);
    renderPanel(onUpdate);

    const user = userEvent.setup();
    // 排序控件只有柄一个：聚焦它按 ↓ 就是「下移一格」。
    const handles = await screen.findAllByLabelText(/Reorder target/);
    handles[0].focus();
    await user.keyboard("{ArrowDown}");

    await waitFor(() => expect(onUpdate).toHaveBeenCalled());
    expect(onUpdate).toHaveBeenCalledTimes(1);
    expect(onUpdate).toHaveBeenCalledWith(
      expect.objectContaining({ orderOverride: [51, 52] }),
    );
  });

  it("Given a device order that differs from the account order, When a target is added from the list, Then the account set gains it in account order and no order override is written", async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    availabilityStub([
      { agentBackendId: 52, available: true },
      { agentBackendId: 51, available: true },
    ]);
    renderPanel(onUpdate, [backend(51), backend(52), backend(53)]);

    const user = userEvent.setup();
    await screen.findByTestId("exec-target-row-0");
    await user.click(screen.getByRole("button", { name: "Add" }));
    await user.click(await screen.findByText(/claude-53/));

    await waitFor(() => expect(onUpdate).toHaveBeenCalled());
    const req = onUpdate.mock.calls[0][0] as agent_svc.UpdateAgentRequest;
    // 账号顺序（51,52）保持不动，新档追加在尾部——本端排序不得回写账号顺序。
    expect(backendIdsOf(req)).toEqual([51, 52, 53]);
    expect(req.orderOverride).toBeUndefined();
  });

  it("Given a device order that differs from the account order, When a target is removed from the list, Then only that target leaves the account set and its order is preserved", async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    availabilityStub([
      { agentBackendId: 52, available: true },
      { agentBackendId: 51, available: true },
    ]);
    renderPanel(onUpdate);

    const user = userEvent.setup();
    const firstRow = await screen.findByTestId("exec-target-row-0");
    await user.click(within(firstRow).getByRole("button", { name: "Remove" }));

    await waitFor(() => expect(onUpdate).toHaveBeenCalled());
    const req = onUpdate.mock.calls[0][0] as agent_svc.UpdateAgentRequest;
    expect(backendIdsOf(req)).toEqual([51]);
    expect(req.orderOverride).toBeUndefined();
  });

  it("Given the order data has not arrived yet, When the panel renders, Then the list shows skeleton rows instead of the empty state", () => {
    installGo(() => new Promise(() => {}));
    renderPanel(vi.fn().mockResolvedValue(undefined));

    expect(screen.getByTestId("exec-target-skeleton")).toBeInTheDocument();
    expect(screen.queryByText("No execution targets yet")).toBeNull();
  });

  // 骨架说的是「顺序还在路上」。读回来了却没拿到顺序（读失败），列表就不能再停在
  // 骨架上：增删/更换与顺序无关，任何状态下都得可用（规格「增删恒可用」）。此时回落
  // 账号级集合自己的顺序——列表照常有内容，用户照常能加/删/换。
  it("Given the order read fails, When the panel renders, Then the list falls back to the account order and add/remove stay available", async () => {
    installGo(() => Promise.reject(new Error("availability read failed")));
    renderPanel(vi.fn().mockResolvedValue(undefined));

    expect(
      await screen.findByRole("button", { name: "Add" }),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("exec-target-skeleton")).toBeNull();
    expect(screen.queryByText("No execution targets yet")).toBeNull();
    // 账号级集合的顺序是 51,52。
    expect(screen.getByTestId("exec-target-row-0")).toHaveTextContent(
      "claude-51",
    );
    expect(screen.getByTestId("exec-target-row-1")).toHaveTextContent(
      "claude-52",
    );
    expect(screen.getAllByRole("button", { name: "Remove" })).toHaveLength(2);
  });
});

describe("exec target availability is read once per panel", () => {
  // 面板与它内部的列表原先各自实例化一次 useExecTargetAvailability，同一个 agentId
  // 与同一份（排序后相同的）targetsKey 因此每次都发两遍相同的 Wails 调用。除了白跑
  // 一趟，两份副本还各自独立地经历「加载中 → 落定」：列表那份先落定时会用它自己的
  // 判定渲染「全部不可用」横幅，而面板那份还没到，行还是骨架——横幅压在骨架之上。
  it("Given the panel renders, When availability is read, Then the backend is called once rather than once per component", async () => {
    const fn = availabilityStub([
      { agentBackendId: 52, available: true },
      { agentBackendId: 51, available: true },
    ]);
    renderPanel(vi.fn().mockResolvedValue(undefined));

    await screen.findByTestId("exec-target-row-0");
    expect(fn).toHaveBeenCalledTimes(1);
  });
});
