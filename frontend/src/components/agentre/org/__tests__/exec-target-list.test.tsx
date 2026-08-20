import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ExecTargetList } from "../exec-target-list";
import type {
  agent_backend_svc,
  chat_svc,
} from "../../../../../wailsjs/go/models";

function backend(
  overrides: Partial<agent_backend_svc.BackendItem> = {},
): agent_backend_svc.BackendItem {
  return {
    id: 51,
    type: "claudecode",
    name: "claude-fable-5",
    llmProviderKey: "",
    llmProviderName: "",
    llmProviderType: "",
    llmProviderModel: "",
    llmProviderActive: false,
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
    deviceId: "",
    deviceName: "",
    online: false,
    agentCount: 0,
    createtime: 0,
    updatetime: 0,
    ...overrides,
  } as agent_backend_svc.BackendItem;
}

const localBackend = (overrides: Partial<agent_backend_svc.BackendItem> = {}) =>
  backend({ id: 51, name: "claude-fable-5", ...overrides });
const remoteBackend = (
  overrides: Partial<agent_backend_svc.BackendItem> = {},
) =>
  backend({
    id: 52,
    name: "claude-opus-5",
    deviceId: "3",
    deviceName: "构建机",
    online: true,
    ...overrides,
  });

function availabilityStub(
  items: Array<{
    agentBackendId: number;
    available: boolean;
    reason?: string;
    hint?: string;
  }>,
) {
  // 列表不再自持 useExecTargetAvailability，判定由面板下传，所以这里造的是一份
  // 现成的 Map，而不是再去桩 Wails 绑定。
  availability = new Map(
    items.map((it) => [
      it.agentBackendId,
      { reason: "", hint: "", ...it },
    ]) as Array<[number, chat_svc.ExecTargetAvailabilityView]>,
  );
  return availability;
}

let availability = new Map<number, chat_svc.ExecTargetAvailabilityView>();

// 技能折在行内，所以每一行都要知道自己那一档的 backend 支不支持技能（R15e 的
// ExecTargetSkillsBlock 一直是这么问的，只是现在由行来问）：claudecode 支持，
// 其余类型不支持。
beforeEach(() => {
  window.go = {
    app: {
      App: {
        GetBackendCapabilities: vi
          .fn()
          .mockImplementation((req: { backendType?: string }) =>
            Promise.resolve({
              capabilities: req?.backendType === "claudecode" ? ["skills"] : [],
              permissionModeMeta: null,
            }),
          ),
        ListAgentSkillPacks: vi.fn().mockResolvedValue({ packs: [] }),
      },
    },
  };
});

afterEach(() => {
  availability = new Map();
  delete window.go;
});

describe("ExecTargetList", () => {
  it("single target: no sequence badge, no drag handle, shows Replace not per-row remove", async () => {
    availabilityStub([{ agentBackendId: 51, available: true }]);
    render(
      <ExecTargetList
        availability={availability}
        agentName="开发"
        targets={[{ agentBackendId: 51 }]}
        backends={[localBackend()]}
        onChange={vi.fn()}
        onReorder={vi.fn()}
        agentId={7}
        onSkillsChange={vi.fn()}
      />,
    );
    expect(await screen.findByText("Local machine")).toBeInTheDocument();
    expect(screen.getByText(/claude-fable-5/)).toBeInTheDocument();
    expect(screen.queryByText("1")).toBeNull();
    expect(screen.getByRole("button", { name: /Replace/ })).toBeInTheDocument();
    expect(screen.queryByLabelText(/Move target down/)).toBeNull();
    expect(screen.queryByRole("button", { name: "Remove" })).toBeNull();
    // R20：只有一项时「当前生效」徽标也不出现——只有一档时说它是废话。
    expect(screen.queryByText("Currently active")).toBeNull();
    // R20：拖拽柄同批隐去——一档没有可排的对象，留个柄等于假的可供性。
    expect(screen.queryByLabelText(/Reorder target/)).toBeNull();
  });

  it("multiple targets: renders sequence badges and per-row move buttons", async () => {
    availabilityStub([
      { agentBackendId: 51, available: true },
      { agentBackendId: 52, available: true },
    ]);
    render(
      <ExecTargetList
        availability={availability}
        agentName="开发"
        targets={[{ agentBackendId: 51 }, { agentBackendId: 52 }]}
        backends={[localBackend(), remoteBackend()]}
        onChange={vi.fn()}
        onReorder={vi.fn()}
        agentId={7}
        onSkillsChange={vi.fn()}
      />,
    );
    await screen.findByText("Local machine");
    expect(screen.getByText("1")).toBeInTheDocument();
    expect(screen.getByText("2")).toBeInTheDocument();
    // 排序控件是**一个柄**，不是一对上下箭头（mockup 的一档一行只有 ⣿）。
    expect(
      screen.getAllByRole("button", { name: /Reorder target/ }),
    ).toHaveLength(2);
    expect(screen.queryByRole("button", { name: /Move target down/ })).toBeNull();
  });

  it("shows Currently active for the first available target and Online for a later available remote target", async () => {
    availabilityStub([
      { agentBackendId: 51, available: true },
      { agentBackendId: 52, available: true },
    ]);
    render(
      <ExecTargetList
        availability={availability}
        agentName="开发"
        targets={[{ agentBackendId: 51 }, { agentBackendId: 52 }]}
        backends={[localBackend(), remoteBackend()]}
        onChange={vi.fn()}
        onReorder={vi.fn()}
        agentId={7}
        onSkillsChange={vi.fn()}
      />,
    );
    expect(await screen.findByText("Currently active")).toBeInTheDocument();
    expect(screen.getByText("Online")).toBeInTheDocument();
  });

  it("shows the block reason for an unavailable target", async () => {
    availabilityStub([
      {
        agentBackendId: 51,
        available: false,
        reason: "backend-requires-provider",
      },
    ]);
    render(
      <ExecTargetList
        availability={availability}
        agentName="开发"
        targets={[{ agentBackendId: 51 }]}
        backends={[localBackend()]}
        onChange={vi.fn()}
        onReorder={vi.fn()}
        agentId={7}
        onSkillsChange={vi.fn()}
      />,
    );
    expect(
      await screen.findByText("An LLM provider must be specified"),
    ).toBeInTheDocument();
  });

  it("Given an unresolved fingerprint target, When it is rendered, Then the internal sha256 identifier is not shown as the machine name", async () => {
    const fingerprint =
      "sha256:4bba4ebecbb1e2ecb75c21b031d3b4319ecc25fb1ec811cdef634d4f6d7be906";
    availabilityStub([
      {
        agentBackendId: 51,
        available: false,
        reason: "exec-target-unpaired",
      },
    ]);
    render(
      <ExecTargetList
        availability={availability}
        agentName="开发"
        targets={[{ agentBackendId: 51 }]}
        backends={[localBackend({ deviceId: fingerprint, deviceName: "" })]}
        onChange={vi.fn()}
        onReorder={vi.fn()}
        agentId={7}
        onSkillsChange={vi.fn()}
      />,
    );

    expect(
      (await screen.findAllByText("This computer isn't paired with it")).length,
    ).toBeGreaterThan(0);
    expect(screen.queryByText(fingerprint)).toBeNull();
  });

  it("shows an all-unavailable banner listing each target's reason", async () => {
    availabilityStub([
      {
        agentBackendId: 51,
        available: false,
        reason: "backend-requires-provider",
      },
      { agentBackendId: 52, available: false, reason: "exec-target-offline" },
    ]);
    render(
      <ExecTargetList
        availability={availability}
        agentName="开发"
        targets={[{ agentBackendId: 51 }, { agentBackendId: 52 }]}
        backends={[localBackend(), remoteBackend({ online: false })]}
        onChange={vi.fn()}
        onReorder={vi.fn()}
        agentId={7}
        onSkillsChange={vi.fn()}
      />,
    );
    expect(
      await screen.findByText(
        '"开发" has no available execution target right now',
      ),
    ).toBeInTheDocument();
    expect(screen.getAllByText("Offline").length).toBeGreaterThan(0);
  });

  // 空态是**一条**提示：它同时说明「不能对话」与「至少要有一项」，不再由面板
  // 另起一条 Alert 说同一件事（规格「今天说了两遍的状态」）。
  it("empty list: shows the CTA and, when saveRejected, the rejection message", () => {
    render(
      <ExecTargetList
        availability={availability}
        agentName="开发"
        targets={[]}
        backends={[localBackend()]}
        onChange={vi.fn()}
        onReorder={vi.fn()}
        agentId={7}
        onSkillsChange={vi.fn()}
        saveRejected
      />,
    );
    expect(screen.getByText("This agent can't chat yet")).toBeInTheDocument();
    expect(
      screen.getByText(/At least one is required before this agent/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Save rejected: add at least one execution target\./),
    ).toBeInTheDocument();
  });

  it("adding a target: opens the picker, disables the one already in the list, and appends the chosen one", async () => {
    availabilityStub([{ agentBackendId: 51, available: true }]);
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <ExecTargetList
        availability={availability}
        agentName="开发"
        targets={[{ agentBackendId: 51 }]}
        backends={[
          localBackend(),
          backend({ id: 52, name: "gpt-5-codex", type: "codex" }),
        ]}
        onChange={onChange}
        onReorder={vi.fn()}
        agentId={7}
        onSkillsChange={vi.fn()}
      />,
    );
    await screen.findByText("Local machine");
    await user.click(screen.getByRole("button", { name: "Add" }));
    expect(await screen.findByText("Already in the list")).toBeInTheDocument();
    await user.click(screen.getByText(/gpt-5-codex/));
    expect(onChange).toHaveBeenCalledWith([
      { agentBackendId: 51 },
      { agentBackendId: 52 },
    ]);
  });

  it("Given an unpaired fingerprint backend, When the add picker opens, Then its group heading does not expose the internal identifier", async () => {
    const fingerprint =
      "sha256:4bba4ebecbb1e2ecb75c21b031d3b4319ecc25fb1ec811cdef634d4f6d7be906";
    availabilityStub([{ agentBackendId: 51, available: true }]);
    const user = userEvent.setup();
    render(
      <ExecTargetList
        availability={availability}
        agentName="开发"
        targets={[{ agentBackendId: 51 }]}
        backends={[
          localBackend(),
          backend({
            id: 52,
            name: "fingerprint backend",
            deviceId: fingerprint,
            deviceName: "",
          }),
        ]}
        onChange={vi.fn()}
        onReorder={vi.fn()}
        agentId={7}
        onSkillsChange={vi.fn()}
      />,
    );

    await user.click(await screen.findByRole("button", { name: "Add" }));
    expect((await screen.findAllByText("Not paired")).length).toBeGreaterThan(
      0,
    );
    expect(screen.queryByText(fingerprint)).toBeNull();
  });

  it("single target: Replace swaps the sole target instead of appending", async () => {
    availabilityStub([{ agentBackendId: 51, available: true }]);
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <ExecTargetList
        availability={availability}
        agentName="开发"
        targets={[{ agentBackendId: 51 }]}
        backends={[
          localBackend(),
          backend({ id: 52, name: "gpt-5-codex", type: "codex" }),
        ]}
        onChange={onChange}
        onReorder={vi.fn()}
        agentId={7}
        onSkillsChange={vi.fn()}
      />,
    );
    await screen.findByText("Local machine");
    await user.click(screen.getByRole("button", { name: /Replace/ }));
    await user.click(await screen.findByText(/gpt-5-codex/));
    expect(onChange).toHaveBeenCalledWith([{ agentBackendId: 52 }]);
  });

  it("removing a target (multi-target) drops it from the list", async () => {
    availabilityStub([
      { agentBackendId: 51, available: true },
      { agentBackendId: 52, available: true },
    ]);
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <ExecTargetList
        availability={availability}
        agentName="开发"
        targets={[{ agentBackendId: 51 }, { agentBackendId: 52 }]}
        backends={[localBackend(), remoteBackend()]}
        onChange={onChange}
        onReorder={vi.fn()}
        agentId={7}
        onSkillsChange={vi.fn()}
      />,
    );
    await screen.findByText("Local machine");
    await user.click(screen.getAllByRole("button", { name: "Remove" })[0]);
    expect(onChange).toHaveBeenCalledWith([{ agentBackendId: 52 }]);
  });

  // 重排与增删是两条写路径：重排只写本端顺序（onReorder），增删/更换写账号级执行
  // 目标集合（onChange）。列表知道刚才发生的是哪一件事，不让调用方去猜。
  it("keyboard equivalent: ArrowDown on the handle reorders via onReorder (never the set) and announces via role=status", async () => {
    availabilityStub([
      { agentBackendId: 51, available: true },
      { agentBackendId: 52, available: true },
    ]);
    const user = userEvent.setup();
    const onChange = vi.fn();
    const onReorder = vi.fn();
    render(
      <ExecTargetList
        availability={availability}
        agentName="开发"
        targets={[{ agentBackendId: 51 }, { agentBackendId: 52 }]}
        backends={[localBackend(), remoteBackend()]}
        onChange={onChange}
        onReorder={onReorder}
        agentId={7}
        onSkillsChange={vi.fn()}
      />,
    );
    await screen.findByText("Local machine");
    screen.getAllByRole("button", { name: /Reorder target/ })[0].focus();
    await user.keyboard("{ArrowDown}");
    expect(onReorder).toHaveBeenCalledWith([
      { agentBackendId: 52 },
      { agentBackendId: 51 },
    ]);
    expect(onChange).not.toHaveBeenCalled();
    await waitFor(() =>
      expect(screen.getByTestId("exec-target-announcer")).not.toHaveTextContent(
        "",
      ),
    );
  });

  // R15：「拖拽排序必须有键盘等价物（聚焦后 ↑/↓）」。dnd-kit 的 PointerSensor 对
  // 合成鼠标事件无反应，键盘是 e2e 里唯一能自动化的路径，所以拖拽柄自己必须可聚焦
  // 并直接响应方向键，而不是只能先按空格「提起」。
  it("Given a multi-target list, When the first row's drag handle is focused and ArrowDown is pressed, Then the row moves down", async () => {
    availabilityStub([
      { agentBackendId: 51, available: true },
      { agentBackendId: 52, available: true },
    ]);
    const user = userEvent.setup();
    const onReorder = vi.fn();
    render(
      <ExecTargetList
        availability={availability}
        agentName="开发"
        targets={[{ agentBackendId: 51 }, { agentBackendId: 52 }]}
        backends={[localBackend(), remoteBackend()]}
        onChange={vi.fn()}
        onReorder={onReorder}
        agentId={7}
        onSkillsChange={vi.fn()}
      />,
    );
    await screen.findByText("Local machine");
    const handles = screen.getAllByRole("button", { name: /Reorder target/ });
    expect(handles).toHaveLength(2);
    handles[0].focus();
    await user.keyboard("{ArrowDown}");
    expect(onReorder).toHaveBeenCalledWith([
      { agentBackendId: 52 },
      { agentBackendId: 51 },
    ]);
    await waitFor(() =>
      expect(screen.getByTestId("exec-target-announcer")).not.toHaveTextContent(
        "",
      ),
    );
  });

  it("Given a multi-target list, When the second row's drag handle is focused and ArrowUp is pressed, Then it produces the same order the first row's ArrowDown does", async () => {
    availabilityStub([
      { agentBackendId: 51, available: true },
      { agentBackendId: 52, available: true },
    ]);
    const user = userEvent.setup();
    const viaKeyboard = vi.fn();
    const { unmount } = render(
      <ExecTargetList
        availability={availability}
        agentName="开发"
        targets={[{ agentBackendId: 51 }, { agentBackendId: 52 }]}
        backends={[localBackend(), remoteBackend()]}
        onChange={vi.fn()}
        onReorder={viaKeyboard}
        agentId={7}
        onSkillsChange={vi.fn()}
      />,
    );
    await screen.findByText("Local machine");
    screen.getAllByRole("button", { name: /Reorder target/ })[1].focus();
    await user.keyboard("{ArrowUp}");
    expect(viaKeyboard).toHaveBeenCalledTimes(1);
    unmount();

    const viaButton = vi.fn();
    render(
      <ExecTargetList
        availability={availability}
        agentName="开发"
        targets={[{ agentBackendId: 51 }, { agentBackendId: 52 }]}
        backends={[localBackend(), remoteBackend()]}
        onChange={vi.fn()}
        onReorder={viaButton}
        agentId={7}
        onSkillsChange={vi.fn()}
      />,
    );
    await screen.findByText("Local machine");
    screen.getAllByRole("button", { name: /Reorder target/ })[0].focus();
    await user.keyboard("{ArrowDown}");
    // 第 1 档下移与第 2 档上移是同一次重排，两条手势必须给出同一个新次序。
    expect(viaButton.mock.calls[0][0]).toEqual(viaKeyboard.mock.calls[0][0]);
  });

  it("Given the topmost drag handle, When ArrowUp is pressed, Then nothing moves", async () => {
    availabilityStub([
      { agentBackendId: 51, available: true },
      { agentBackendId: 52, available: true },
    ]);
    const user = userEvent.setup();
    const onReorder = vi.fn();
    render(
      <ExecTargetList
        availability={availability}
        agentName="开发"
        targets={[{ agentBackendId: 51 }, { agentBackendId: 52 }]}
        backends={[localBackend(), remoteBackend()]}
        onChange={vi.fn()}
        onReorder={onReorder}
        agentId={7}
        onSkillsChange={vi.fn()}
      />,
    );
    await screen.findByText("Local machine");
    screen.getAllByRole("button", { name: /Reorder target/ })[0].focus();
    await user.keyboard("{ArrowUp}");
    expect(onReorder).not.toHaveBeenCalled();
  });

  // 加载窗口与空态是两件事：空态说的是「这个 Agent 没有执行目标」，顺序还没到达时
  // 说它就是假话（此前两句文案还会同时出现，互相否定）。
  it("Given the order data has not arrived, When the list renders, Then it shows skeleton rows instead of the empty state", () => {
    render(
      <ExecTargetList
        availability={availability}
        agentName="开发"
        targets={[]}
        backends={[localBackend()]}
        onChange={vi.fn()}
        onReorder={vi.fn()}
        agentId={7}
        onSkillsChange={vi.fn()}
        loading
      />,
    );
    expect(screen.getByTestId("exec-target-skeleton")).toBeInTheDocument();
    expect(screen.queryByText("This agent can't chat yet")).toBeNull();
    expect(screen.queryByText(/At least one is required/)).toBeNull();
    // 骨架期间不给增删入口：此刻列表还不知道自己有哪些档，基于空列表的一次「添加」
    // 会把账号级集合整份写成新加的那一项。
    expect(screen.queryByRole("button", { name: "Add" })).toBeNull();
    expect(
      screen.queryByRole("button", { name: /Add execution target/ }),
    ).toBeNull();
  });

  // 一个列表、一套能力：不再有「只能重排、不能增删」的第二个视图，也没有任何解释
  // 作用域的说明行（规格决策 5）。
  it("Given a multi-target list, Then add/remove are available and no scope explainer or restore action is rendered", async () => {
    availabilityStub([
      { agentBackendId: 51, available: true },
      { agentBackendId: 52, available: true },
    ]);
    render(
      <ExecTargetList
        availability={availability}
        agentName="开发"
        targets={[{ agentBackendId: 51 }, { agentBackendId: 52 }]}
        backends={[localBackend(), remoteBackend()]}
        onChange={vi.fn()}
        onReorder={vi.fn()}
        agentId={7}
        onSkillsChange={vi.fn()}
      />,
    );
    await screen.findByText("Local machine");
    expect(screen.getByRole("button", { name: "Add" })).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Remove" })).toHaveLength(2);
    expect(
      screen.queryByRole("button", { name: /Restore account default order/ }),
    ).toBeNull();
    expect(screen.queryByText(/not synced to other devices/i)).toBeNull();
  });
});

// 一档一行、技能折在行内：四种行状态各自可辨 —— 当前生效、不支持技能（无展开
// 入口）、离线（能展开，只减不增）、不可用（留在列表里并给出原因）。
describe("ExecTargetList rows fold their own skills", () => {
  const renderList = (
    props: Partial<Parameters<typeof ExecTargetList>[0]> = {},
  ) =>
    render(
      <ExecTargetList
        agentId={7}
        availability={availability}
        agentName="开发"
        targets={[{ agentBackendId: 51, skills: [] }]}
        backends={[localBackend()]}
        onChange={vi.fn()}
        onReorder={vi.fn()}
        onSkillsChange={vi.fn()}
        {...props}
      />,
    );

  it("Given a backend that supports skills, When the row's skills fold opens, Then its own grants are managed inside the row", async () => {
    availabilityStub([
      { agentBackendId: 51, available: true },
      { agentBackendId: 52, available: true },
    ]);
    const user = userEvent.setup();
    renderList({
      targets: [
        { agentBackendId: 51, skills: [{ id: "opsctl@x", enabled: true }] },
        { agentBackendId: 52, skills: [] },
      ],
      backends: [localBackend(), remoteBackend()],
    });
    const row = screen.getByTestId("exec-target-row-0");
    const toggle = await within(row).findByRole("button", { name: /Skills/ });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    await user.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(await within(row).findByText("opsctl")).toBeInTheDocument();
    expect(
      within(row).getByRole("button", { name: "Manage skills" }),
    ).toBeInTheDocument();
  });

  // 单档时折叠没有意义：一档没有「先扫一眼列表」这一步，默认就展开
  // （R20：单档退化成今天的样子，今天的技能区本来就是直接铺开的）。
  it("Given a single target, When the row renders, Then its skills are already unfolded", async () => {
    availabilityStub([{ agentBackendId: 51, available: true }]);
    renderList({
      targets: [
        { agentBackendId: 51, skills: [{ id: "opsctl@x", enabled: true }] },
      ],
    });
    const toggle = await screen.findByRole("button", { name: /Skills/ });
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(await screen.findByText("opsctl")).toBeInTheDocument();
  });

  it("Given a backend without skills support, When the row renders, Then it has no fold to open and says why", async () => {
    availabilityStub([{ agentBackendId: 53, available: true }]);
    renderList({
      targets: [{ agentBackendId: 53, skills: [] }],
      backends: [backend({ id: 53, type: "codex", name: "gpt-5-codex" })],
    });
    expect(
      await screen.findByText("This backend doesn't support skills"),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Skills/ })).toBeNull();
  });

  it("Given an offline target, When its fold opens, Then granted skills can be removed but no new one can be added", async () => {
    availabilityStub([
      { agentBackendId: 54, available: false, reason: "exec-target-offline" },
    ]);
    const user = userEvent.setup();
    const onSkillsChange = vi.fn();
    renderList({
      targets: [
        { agentBackendId: 54, skills: [{ id: "opsctl@x", enabled: true }] },
      ],
      backends: [remoteBackend({ id: 54, online: false, type: "claudecode" })],
      onSkillsChange,
    });
    const row = screen.getByTestId("exec-target-row-0");
    // 单档默认已展开，离线的一档照样能展开看已授权的技能。
    expect(
      await within(row).findByRole("button", { name: /Skills/ }),
    ).toHaveAttribute("aria-expanded", "true");
    expect(
      await within(row).findByRole("button", { name: "Manage skills" }),
    ).toBeDisabled();
    await user.click(
      within(row).getByRole("button", { name: "Remove opsctl" }),
    );
    expect(onSkillsChange).toHaveBeenCalledWith(54, []);
  });

  it("Given the four row states, When they are rendered together, Then each is told apart on its own row", async () => {
    availabilityStub([
      { agentBackendId: 51, available: true },
      { agentBackendId: 53, available: true },
      { agentBackendId: 54, available: false, reason: "exec-target-offline" },
      {
        agentBackendId: 55,
        available: false,
        reason: "backend-requires-provider",
      },
    ]);
    renderList({
      targets: [
        { agentBackendId: 51, skills: [] },
        { agentBackendId: 53, skills: [] },
        { agentBackendId: 54, skills: [] },
        { agentBackendId: 55, skills: [] },
      ],
      backends: [
        localBackend(),
        backend({ id: 53, type: "codex", name: "gpt-5-codex" }),
        remoteBackend({ id: 54, online: false, type: "claudecode" }),
        backend({ id: 55, type: "builtin", name: "built-in" }),
      ],
    });
    await screen.findAllByText("Local machine");
    // ① 当前生效
    expect(
      within(screen.getByTestId("exec-target-row-0")).getByText(
        "Currently active",
      ),
    ).toBeInTheDocument();
    // ② 不支持技能：没有展开入口
    const unsupported = screen.getByTestId("exec-target-row-1");
    await waitFor(() =>
      expect(
        within(unsupported).getByText("This backend doesn't support skills"),
      ).toBeInTheDocument(),
    );
    expect(
      within(unsupported).queryByRole("button", { name: /Skills/ }),
    ).toBeNull();
    // ③ 离线：仍可展开
    const offline = screen.getByTestId("exec-target-row-2");
    expect(within(offline).getByText("Offline")).toBeInTheDocument();
    await waitFor(() =>
      expect(
        within(offline).getByRole("button", { name: /Skills/ }),
      ).toBeInTheDocument(),
    );
    // ④ 不可用：留在列表里并给出原因
    expect(
      within(screen.getByTestId("exec-target-row-3")).getByText(
        "An LLM provider must be specified",
      ),
    ).toBeInTheDocument();
  });
});
