import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ComponentProps } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { truncateFlashText } from "../agent-backends-utils";

const appMocks = vi.hoisted(() => ({
  CancelTestAgentBackend: vi.fn(),
  CreateAgentBackend: vi.fn(),
  CreateOpenClawAgentBackend: vi.fn(),
  DeleteAgentBackend: vi.fn(),
  GetGatewayStatus: vi.fn(),
  ListAgentBackends: vi.fn(),
  ListLLMModels: vi.fn(),
  ListLLMProviders: vi.fn(),
  RemoteDeviceFingerprint: vi.fn(),
  RemoteDeviceList: vi.fn(),
  RemoteDeviceListProviders: vi.fn(),
  RemoteDeviceSyncProvider: vi.fn(),
  ResolveAgentBackendCLIPath: vi.fn(),
  ScanAndCreateAgentBackends: vi.fn(),
  ServerListDevices: vi.fn(),
  TestAgentBackend: vi.fn(),
  TestOpenClawAgentBackend: vi.fn(),
  UpdateAgentBackend: vi.fn(),
  UpdateOpenClawAgentBackend: vi.fn(),
}));

vi.mock("../../../../wailsjs/go/app/App", () => appMocks);

// remote.device.state 是 agentred 上下线的既有推送。运行设备下拉按 online 禁用选项，
// 收编刚发生时那台机器还在拨号（online=false），没有订阅就只能靠关掉重开。
const runtimeMocks = vi.hoisted(() => {
  const handlers = new Map<string, Set<(payload: unknown) => void>>();
  return {
    handlers,
    EventsOn: vi.fn((event: string, cb: (payload: unknown) => void) => {
      const set = handlers.get(event) ?? new Set<(payload: unknown) => void>();
      set.add(cb);
      handlers.set(event, set);
      return () => set.delete(cb);
    }),
    emit(event: string, payload: unknown) {
      for (const cb of [...(handlers.get(event) ?? [])]) cb(payload);
    },
  };
});

vi.mock("../../../../wailsjs/runtime/runtime", () => ({
  EventsOn: runtimeMocks.EventsOn,
}));

import { AgentBackendsPanel as AgentBackendsPanelBase } from "../agent-backends";

// 页级操作(自动识别 / 新建后端)不再由面板自己摆在卡片里，而是通过 renderHeader
// 交给宿主的 H1 行(settings.tsx 的 SettingsPageHeader actions 槽)。用例统一套一层
// 与宿主同形的页头槽，既保留 screen.getByRole 的找法，也不必逐个用例重写 renderHeader。
function AgentBackendsPanel(
  props: ComponentProps<typeof AgentBackendsPanelBase>,
) {
  return (
    <AgentBackendsPanelBase
      renderHeader={(actions) => <div data-testid="page-header">{actions}</div>}
      {...props}
    />
  );
}

type AnyFn = (...args: unknown[]) => unknown;

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

type AppMockShape = {
  ListAgentBackends: AnyFn;
  ListLLMProviders: AnyFn;
  ListLLMModels: AnyFn;
  CreateAgentBackend?: AnyFn;
  CreateOpenClawAgentBackend?: AnyFn;
  UpdateAgentBackend?: AnyFn;
  UpdateOpenClawAgentBackend?: AnyFn;
  DeleteAgentBackend?: AnyFn;
  TestAgentBackend?: AnyFn;
  TestOpenClawAgentBackend?: AnyFn;
  CancelTestAgentBackend?: AnyFn;
  GetGatewayStatus?: AnyFn;
  ScanAndCreateAgentBackends?: AnyFn;
  ResolveAgentBackendCLIPath?: AnyFn;
  RemoteDeviceFingerprint?: AnyFn;
  RemoteDeviceList?: AnyFn;
  RemoteDeviceListProviders?: AnyFn;
  RemoteDeviceSyncProvider?: AnyFn;
  ServerListDevices?: AnyFn;
};

// mockModel 构造一条启用模型记录（modelKey 稳定，modelId 可展示）。
function mockModel(providerId: number, modelKey: string, modelId: string) {
  return {
    id: providerId * 100,
    providerId,
    providerKey: `key-${providerId}`,
    modelKey,
    modelId,
    name: "",
    contextWindow: 0,
    maxOutput: 0,
    enabled: true,
    isDefault: false,
    createtime: 0,
    updatetime: 0,
  };
}

// modelsById 默认给每个 provider 返回一条启用模型（默认模型）。
function defaultModelsById(id: number) {
  if (id === 1) return [mockModel(1, "mk-1", "claude-sonnet-4-6")];
  if (id === 2) return [mockModel(2, "mk-2", "gpt-5")];
  if (id === 3) return [mockModel(3, "mk-3", "gpt-5-codex")];
  return [];
}

function installAppMock(overrides: Partial<AppMockShape> = {}) {
  const base: AppMockShape = {
    ListAgentBackends: vi.fn(() =>
      Promise.resolve({
        items: [
          {
            id: 1,
            type: "builtin",
            name: "默认助手",
            llmProviderKey: "key-1",
            llmProviderName: "Anthropic",
            llmProviderType: "anthropic",
            llmProviderModel: "claude-sonnet-4-6",
            llmProviderActive: true,
            cliPath: "",
            agentCount: 3,
            createtime: 0,
            updatetime: 0,
          },
        ],
      }),
    ),
    ListLLMProviders: vi.fn(() =>
      Promise.resolve({
        items: [
          {
            id: 1,
            type: "anthropic",
            name: "Anthropic",
            providerKey: "key-1",
            baseUrl: "",
            maskedApiKey: "sk-•••",
            hasApiKey: true,
            enabled: true,
            defaultModelKey: "mk-1",
            createtime: 0,
            updatetime: 0,
          },
        ],
      }),
    ),
    // 默认每个 provider 返回一条启用默认模型（Picker 目录需要）。
    ListLLMModels: vi.fn((...args: unknown[]) => {
      const req = args[0] as { id?: number } | undefined;
      return Promise.resolve({ items: defaultModelsById(Number(req?.id)) });
    }),
    CreateAgentBackend: vi.fn(() => Promise.resolve({ item: { id: 2 } })),
    CreateOpenClawAgentBackend: vi.fn(() =>
      Promise.resolve({ item: { id: 3 } }),
    ),
    UpdateAgentBackend: vi.fn(() => Promise.resolve({ item: { id: 1 } })),
    UpdateOpenClawAgentBackend: vi.fn(() =>
      Promise.resolve({ item: { id: 3 } }),
    ),
    DeleteAgentBackend: vi.fn(() => Promise.resolve({})),
    TestAgentBackend: vi.fn(() =>
      Promise.resolve({ ok: true, latencyMs: 0, message: "" }),
    ),
    TestOpenClawAgentBackend: vi.fn(() =>
      Promise.resolve({
        ok: true,
        code: "",
        message: "",
        latencyMs: 3,
        gatewayVersion: "2026.7.1-2",
        protocol: 4,
        grantedScopes: [
          "operator.read",
          "operator.write",
          "operator.approvals",
        ],
        methods: [],
        events: [],
        openClawAgents: [],
        openClawModels: [],
      }),
    ),
    CancelTestAgentBackend: vi.fn(() => Promise.resolve({ canceled: true })),
    GetGatewayStatus: vi.fn(() =>
      Promise.resolve({
        status: "running",
        listenURL: "http://127.0.0.1:60080",
        reason: "",
        routes: [],
      }),
    ),
    // 默认让 ResolveAgentBackendCLIPath 兜底返回 found=false，避免每个用例都得显式注入。
    // 单独验证自动识别行为的用例会在 overrides 里覆盖这个 mock。
    ResolveAgentBackendCLIPath: vi.fn(() =>
      Promise.resolve({ path: "", found: false }),
    ),
    // 自动识别默认「什么都没扫到」，只有专门验证扫描的用例才覆盖它。
    ScanAndCreateAgentBackends: vi.fn(() => Promise.resolve({ results: [] })),
    RemoteDeviceFingerprint: vi.fn(() =>
      Promise.resolve("sha256:local-desktop"),
    ),
    RemoteDeviceList: vi.fn(() => Promise.resolve([])),
    RemoteDeviceListProviders: vi.fn(() => Promise.resolve([])),
    RemoteDeviceSyncProvider: vi.fn(() => Promise.resolve(undefined)),
    ServerListDevices: vi.fn(() => Promise.resolve([])),
  };
  const merged = { ...base, ...overrides } as Required<AppMockShape>;
  for (const key of Object.keys(appMocks) as Array<keyof typeof appMocks>) {
    const mock = appMocks[key] as ReturnType<typeof vi.fn>;
    const fn = merged[key as keyof Required<AppMockShape>] as AnyFn;
    mock.mockReset();
    mock.mockImplementation((...args: unknown[]) => fn(...args));
  }
  return merged;
}

afterEach(() => {
  vi.clearAllMocks();
  runtimeMocks.handlers.clear();
});

describe("AgentBackendsPanel runtime device list", () => {
  // 收编发生在 ServerListDevices 内部（app.ServerListDevices → AdoptAccountDevices
  // 写库）。两个调用并发时 RemoteDeviceList 大概率先返回，这一次就拿不到刚收编的
  // 那一行，用户得关掉弹窗重开才看得见。
  it("Given account devices are still loading, When the editor opens, Then the paired list is read only after adoption has run", async () => {
    const user = userEvent.setup();
    const gate = deferred<unknown[]>();
    const mocks = installAppMock({
      ServerListDevices: vi.fn(() => gate.promise),
      RemoteDeviceList: vi.fn(() => Promise.resolve([])),
    });
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));
    await screen.findByRole("dialog");

    await waitFor(() => expect(mocks.ServerListDevices).toHaveBeenCalled());
    expect(mocks.RemoteDeviceList).not.toHaveBeenCalled();

    gate.resolve([]);
    await waitFor(() => expect(mocks.RemoteDeviceList).toHaveBeenCalled());
  });

  // 刚收编的那一行 watcher 才开始拨号，那一刻 online=false，下拉把它渲染成灰的
  // 不可选项。在线态推送到了就该就地变可选，而不是等用户关掉弹窗重开。
  it("Given an offline runtime device, When it comes online, Then its option becomes selectable in place", async () => {
    const user = userEvent.setup();
    installAppMock({
      RemoteDeviceList: vi.fn(() =>
        Promise.resolve([{ id: 7, name: "linux-srv", online: false }]),
      ),
    });
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(
      within(dialog).getByRole("radio", { name: /Claude Code CLI/ }),
    );
    await user.click(
      within(dialog).getByRole("combobox", { name: "Runtime Device" }),
    );

    const offline = await screen.findByRole("option", { name: /linux-srv/ });
    expect(offline).toHaveAttribute("aria-disabled", "true");

    act(() =>
      runtimeMocks.emit("remote.device.state", {
        id: 7,
        name: "linux-srv",
        online: true,
        lastSeenAt: 1_700_000_000_000,
        lastError: "",
      }),
    );

    await waitFor(() =>
      expect(
        screen.getByRole("option", { name: /linux-srv/ }),
      ).not.toHaveAttribute("aria-disabled", "true"),
    );
  });
});

describe("AgentBackendsPanel", () => {
  it("renders backends fetched from Wails bindings", async () => {
    installAppMock();
    render(<AgentBackendsPanel />);

    const list = await screen.findByRole("list", {
      name: "Agent backend list",
    });
    await waitFor(() => {
      expect(within(list).getByText("默认助手")).toBeInTheDocument();
      expect(within(list).getByText("Anthropic")).toBeInTheDocument();
      expect(within(list).getByText("claude-sonnet-4-6")).toBeInTheDocument();
      expect(within(list).getByText("Follow default")).toBeInTheDocument();
    });
    expect(
      within(list).getByRole("img", { name: "Agentre" }),
    ).toBeInTheDocument();
    expect(
      within(list).getByRole("img", { name: "Anthropic" }),
    ).toBeInTheDocument();
  });

  it("flags rows whose LLM provider is inactive", async () => {
    installAppMock({
      ListAgentBackends: vi.fn(() =>
        Promise.resolve({
          items: [
            {
              id: 1,
              type: "builtin",
              name: "孤儿后端",
              llmProviderKey: "key-7",
              llmProviderName: "",
              llmProviderType: "",
              llmProviderModel: "",
              llmProviderActive: false,
              cliPath: "",
              agentCount: 0,
              createtime: 0,
              updatetime: 0,
            },
          ],
        }),
      ),
    });
    render(<AgentBackendsPanel />);

    const list = await screen.findByRole("list", {
      name: "Agent backend list",
    });
    await waitFor(() => {
      expect(within(list).getByText("孤儿后端")).toBeInTheDocument();
      expect(within(list).getByText("Needs action")).toBeInTheDocument();
    });
  });

  it("Given follow-default, fixed, CLI-login and invalid backends, When the list renders, Then each row shows an independent binding summary and change-binding action", async () => {
    const user = userEvent.setup();
    installAppMock({
      ListAgentBackends: vi.fn(() =>
        Promise.resolve({
          items: [
            {
              id: 11,
              type: "claudecode",
              name: "Follow backend",
              llmProviderKey: "key-1",
              llmModelKey: "",
              llmProviderName: "Anthropic",
              llmProviderType: "anthropic",
              llmProviderModel: "claude-sonnet-4-6",
              llmProviderActive: true,
              cliPath: "/usr/bin/claude",
              agentCount: 2,
            },
            {
              id: 12,
              type: "claudecode",
              name: "Fixed backend",
              llmProviderKey: "key-1",
              llmModelKey: "mk-opus",
              llmProviderName: "Anthropic",
              llmProviderType: "anthropic",
              llmProviderModel: "claude-opus-4-1",
              llmProviderActive: true,
              cliPath: "/usr/bin/claude",
              agentCount: 1,
            },
            {
              id: 13,
              type: "claudecode",
              name: "CLI backend",
              llmProviderKey: "",
              llmModelKey: "",
              llmProviderName: "",
              llmProviderType: "",
              llmProviderModel: "",
              llmProviderActive: false,
              cliPath: "/usr/bin/claude",
              agentCount: 0,
            },
            {
              id: 14,
              type: "claudecode",
              name: "Invalid backend",
              llmProviderKey: "key-gone",
              llmModelKey: "mk-gone",
              llmProviderName: "Removed provider",
              llmProviderType: "anthropic",
              llmProviderModel: "claude-removed",
              llmProviderActive: false,
              cliPath: "/usr/bin/claude",
              agentCount: 0,
            },
          ],
        }),
      ),
    });
    render(<AgentBackendsPanel />);

    const followRow = (await screen.findByText("Follow backend")).closest(
      '[role="listitem"]',
    ) as HTMLElement;
    expect(within(followRow).getByText("Anthropic")).toBeInTheDocument();
    expect(
      within(followRow).getByText("claude-sonnet-4-6"),
    ).toBeInTheDocument();
    expect(within(followRow).getByText("Follow default")).toBeInTheDocument();
    expect(
      within(followRow).getByRole("img", { name: "Anthropic" }),
    ).toBeInTheDocument();

    // 绑定是元数据行上的一段面包屑，与运行设备 / 引用数同处一行，不再是独立的第三层块。
    const followMeta = within(followRow).getByTestId("backend-meta");
    const followBinding = within(followRow).getByTestId("backend-binding");
    expect(followMeta).toContainElement(followBinding);
    expect(followBinding).toHaveTextContent("Anthropic");
    expect(followBinding).toHaveTextContent("claude-sonnet-4-6");
    expect(followBinding).toHaveTextContent("Follow default");
    expect(followMeta).toHaveTextContent(/Local/);
    expect(followMeta).toHaveTextContent(/2 agents in use/);
    // 后端类型回到名字旁的 chip，不再降级成元数据行里的纯文本。
    expect(
      within(followRow).getByTestId("backend-type-chip"),
    ).toHaveTextContent("Claude Code CLI");
    expect(followMeta).not.toHaveTextContent(/Claude Code CLI/);

    const fixedRow = screen
      .getByText("Fixed backend")
      .closest('[role="listitem"]') as HTMLElement;
    expect(within(fixedRow).getByText("claude-opus-4-1")).toBeInTheDocument();
    expect(within(fixedRow).getByText("Fixed")).toBeInTheDocument();

    const cliRow = screen
      .getByText("CLI backend")
      .closest('[role="listitem"]') as HTMLElement;
    expect(within(cliRow).getByText("Use CLI login state")).toBeInTheDocument();

    const invalidRow = screen
      .getByText("Invalid backend")
      .closest('[role="listitem"]') as HTMLElement;
    expect(
      within(invalidRow).getByText(/binding is invalid/i),
    ).toBeInTheDocument();
    const invalidChange = within(invalidRow).getByRole("button", {
      name: "Reselect binding for Invalid backend",
    });
    expect(invalidChange).toHaveTextContent("Reselect");
    expect(invalidChange).toHaveAttribute("data-variant", "default");

    await user.click(
      within(followRow).getByRole("button", {
        name: "Change binding for Follow backend",
      }),
    );
    const dialog = await screen.findByRole("dialog", {
      name: "Edit Agent Backend",
    });
    expect(
      within(dialog).getByRole("heading", { name: "Model binding" }),
    ).toBeInTheDocument();
    expect(
      await screen.findByRole("listbox", { name: "Model binding" }),
    ).toBeInTheDocument();
  });

  it("Given a Claude backend editor, When binding mode changes, Then the model-binding block keeps tier routes collapsed and only shows custom model for CLI login", async () => {
    const user = userEvent.setup();
    installAppMock({
      ListLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [
            mockModel(1, "mk-1", "claude-sonnet-4-6"),
            mockModel(1, "mk-opus", "claude-opus-4-1"),
          ],
        }),
      ),
    });
    render(<AgentBackendsPanel />);
    await screen.findByText("默认助手");
    await user.click(screen.getByRole("button", { name: /New Backend/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(
      within(dialog).getByRole("radio", { name: /Claude Code CLI/ }),
    );

    const block = within(dialog).getByTestId("model-binding-block");
    expect(
      within(block).getByRole("heading", { name: "Model binding" }),
    ).toBeInTheDocument();
    // 区块标题「模型绑定」只出现一次；紧随其后的就是 Picker 触发器，没有同名字段标签。
    expect(within(block).getAllByText("Model binding")).toHaveLength(1);
    const routesToggle = within(block).getByRole("button", {
      name: /Model tier routes/,
    });
    expect(routesToggle).toHaveAttribute("aria-expanded", "false");
    expect(routesToggle).toHaveTextContent(/OPUS.*main binding/i);
    expect(routesToggle).toHaveTextContent(/SONNET.*main binding/i);
    expect(routesToggle).toHaveTextContent(/HAIKU.*main binding/i);
    // 折叠头的右槽是一句短提示 chip，不再塞实现术语的环境变量名。
    expect(routesToggle).not.toHaveTextContent(/ANTHROPIC_DEFAULT/);
    expect(routesToggle).toHaveTextContent(/no change needed/i);
    expect(within(block).getByLabelText("Custom Model")).toBeInTheDocument();
    expect(
      within(block).getByText(/only applies with CLI login/i),
    ).toBeInTheDocument();

    await user.click(
      within(block).getByRole("button", { name: "Model binding" }),
    );
    await user.click(
      await screen.findByRole("option", {
        name: /Follow this provider's default/,
      }),
    );
    expect(
      within(block).queryByLabelText("Custom Model"),
    ).not.toBeInTheDocument();
    expect(
      within(block).getByText(/retained but ignored/i),
    ).toBeInTheDocument();

    await user.click(routesToggle);
    expect(routesToggle).toHaveAttribute("aria-expanded", "true");
    expect(
      within(block).getAllByRole("button", { name: /Claude tier route/ }),
    ).toHaveLength(3);
  });

  it("Given an expanded Claude tier route, When its picker opens, Then its follow-default rows read the same as the main binding picker's", async () => {
    const user = userEvent.setup();
    installAppMock();
    render(<AgentBackendsPanel />);
    await screen.findByText("默认助手");
    await user.click(screen.getByRole("button", { name: /New Backend/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(
      within(dialog).getByRole("radio", { name: /Claude Code CLI/ }),
    );
    const block = within(dialog).getByTestId("model-binding-block");

    // 「跟随默认会不会变」现在写在每一行 provider-default 的副行上（「当前 <模型>」），
    // 不再有弹层顶部图例 —— 分级路由是第三个共用这颗 Picker 的场景，这套副行不能
    // 只在会话/后端里出现。
    await user.click(
      within(block).getByRole("button", { name: "Model binding" }),
    );
    const mainDefault = await screen.findByRole("option", {
      name: /Follow this provider's default/,
    });
    expect(mainDefault).toHaveTextContent("Currently claude-sonnet-4-6");
    await user.keyboard("{Escape}");

    await user.click(
      within(block).getByRole("button", { name: /Model tier routes/ }),
    );
    await user.click(
      within(block).getAllByRole("button", { name: /Claude tier route/ })[0],
    );
    const routeDefault = await screen.findByRole("option", {
      name: /Follow this provider's default/,
    });
    expect(routeDefault).toHaveTextContent("Currently claude-sonnet-4-6");
  });

  it("Given the model-binding block renders, When its heading is read, Then it goes straight to the picker without a paragraph restating what the nesting already shows", async () => {
    const user = userEvent.setup();
    installAppMock();
    render(<AgentBackendsPanel />);
    await screen.findByText("默认助手");
    await user.click(screen.getByRole("button", { name: /New Backend/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(
      within(dialog).getByRole("radio", { name: /Claude Code CLI/ }),
    );
    const block = within(dialog).getByTestId("model-binding-block");

    // 「分级路由/自定义模型从属于主绑定」这件事已经由区块嵌套 + 路由摘要「3 项都继承
    // 主绑定」+ 自定义模型的行内说明讲了三遍，mockup ?view=backend 的区块头没有这段话。
    expect(
      within(block).queryByText(/Choose the model source first/i),
    ).not.toBeInTheDocument();
    expect(
      within(block).getByRole("heading", { name: "Model binding" }),
    ).toBeInTheDocument();
  });

  it("Given the model-binding block renders, When the tier-routes toggle is inspected, Then it carries a pointer cursor like every other clickable control", async () => {
    const user = userEvent.setup();
    installAppMock();
    render(<AgentBackendsPanel />);
    await screen.findByText("默认助手");
    await user.click(screen.getByRole("button", { name: /New Backend/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(
      within(dialog).getByRole("radio", { name: /Claude Code CLI/ }),
    );
    const block = within(dialog).getByTestId("model-binding-block");

    // 裸 <button> 拿不到 shadcn Button 基类里的 cursor-pointer，而 tailwind v4 把
    // button 的默认光标改成了 default —— 折叠头点得动却不像能点。
    expect(
      within(block).getByRole("button", { name: /Model tier routes/ }),
    ).toHaveClass("cursor-pointer");
  });

  it("Given the main binding resolves to a provider, When a Claude tier route picker opens, Then its inherit option shows the same arrow + brand mark + monospaced model id as the session picker", async () => {
    const user = userEvent.setup();
    installAppMock();
    render(<AgentBackendsPanel />);
    await screen.findByText("默认助手");
    await user.click(screen.getByRole("button", { name: /New Backend/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(
      within(dialog).getByRole("radio", { name: /Claude Code CLI/ }),
    );
    const block = within(dialog).getByTestId("model-binding-block");

    // 主绑定先落到一个真实供应商，继承项才有解析结果可写。
    await user.click(
      within(block).getByRole("button", { name: "Model binding" }),
    );
    await user.click(
      await screen.findByRole("option", {
        name: /Follow this provider's default/,
      }),
    );

    await user.click(
      within(block).getByRole("button", { name: /Model tier routes/ }),
    );
    await user.click(
      within(block).getAllByRole("button", { name: /Claude tier route/ })[0],
    );

    const inherit = await screen.findByRole("option", {
      name: /Inherit main binding/,
    });
    const resolution = within(inherit).getByTestId("special-resolution");
    expect(resolution).toHaveTextContent("→");
    expect(
      within(resolution).getByRole("img", { name: "Anthropic" }),
    ).toBeInTheDocument();
    expect(resolution).toHaveTextContent("Anthropic");
    expect(within(resolution).getByText("claude-sonnet-4-6")).toHaveClass(
      "font-mono",
    );
  });

  it("Given the main binding is CLI login, When a Claude tier route picker opens, Then its inherit option keeps the plain CLI resolution wording", async () => {
    const user = userEvent.setup();
    installAppMock();
    render(<AgentBackendsPanel />);
    await screen.findByText("默认助手");
    await user.click(screen.getByRole("button", { name: /New Backend/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(
      within(dialog).getByRole("radio", { name: /Claude Code CLI/ }),
    );
    const block = within(dialog).getByTestId("model-binding-block");

    await user.click(
      within(block).getByRole("button", { name: /Model tier routes/ }),
    );
    await user.click(
      within(block).getAllByRole("button", { name: /Claude tier route/ })[0],
    );

    const inherit = await screen.findByRole("option", {
      name: /Inherit main binding/,
    });
    expect(inherit).toHaveTextContent(
      "Effective model is determined by the CLI's own login account",
    );
    expect(inherit).not.toHaveTextContent("→");
    expect(
      within(inherit).queryByTestId("special-resolution"),
    ).not.toBeInTheDocument();
  });

  it("Given a selected provider target, When the editor renders, Then the picker trigger uses target-and-mode plus effective-model consequence on two lines", async () => {
    const user = userEvent.setup();
    installAppMock();
    render(<AgentBackendsPanel />);
    const row = (await screen.findByText("默认助手")).closest(
      '[role="listitem"]',
    ) as HTMLElement;
    await user.click(within(row).getByRole("button", { name: /Edit/ }));
    const dialog = await screen.findByRole("dialog");
    const trigger = within(dialog).getByRole("button", {
      name: "Model binding",
    });
    expect(trigger).toHaveTextContent("Anthropic");
    expect(trigger).toHaveTextContent("Follow default");
    expect(trigger).toHaveTextContent("claude-sonnet-4-6");
    expect(trigger).toHaveTextContent(/next turn/i);
    expect(
      within(trigger).getByTestId("model-target-trigger-sub"),
    ).toBeVisible();
  });

  it("Given a provider-bound backend, When the editor renders, Then the trigger's first line carries the brand logo, the provider name and a follow/fixed chip", async () => {
    const user = userEvent.setup();
    installAppMock();
    render(<AgentBackendsPanel />);
    const row = (await screen.findByText("默认助手")).closest(
      '[role="listitem"]',
    ) as HTMLElement;
    await user.click(within(row).getByRole("button", { name: /Edit/ }));
    const dialog = await screen.findByRole("dialog");
    const trigger = within(dialog).getByRole("button", {
      name: "Model binding",
    });

    // mockup 06-backend 的触发器主行 = 品牌标识 + 供应商名 + 模式徽标，
    // 与列表行的绑定面包屑（backend-binding）同一套处理，不是一串扁平文案。
    expect(
      within(trigger).getByRole("img", { name: "Anthropic" }),
    ).toBeInTheDocument();
    expect(within(trigger).getByTestId("binding-mode-chip")).toHaveTextContent(
      "Follow default",
    );
    // 主行变成节点后，无障碍名仍只由显式 aria-label 决定，不被品牌标识念出来。
    expect(trigger).toHaveAccessibleName("Model binding");

    // 边界：固定到具体模型后，同一个徽标翻成「固定」。
    await user.click(trigger);
    const listbox = await screen.findByRole("listbox", {
      name: "Model binding",
    });
    const fixedOption = within(listbox)
      .getAllByRole("option")
      .find((option) => option.getAttribute("data-kind") === "fixed");
    await user.click(fixedOption as HTMLElement);
    await waitFor(() => {
      expect(
        within(trigger).getByTestId("binding-mode-chip"),
      ).toHaveTextContent("Fixed");
    });
    expect(trigger).toHaveAccessibleName("Model binding");
  });

  it("Given remote catalog requests overlap, When an old device responds last, Then it cannot replace the current device catalog", async () => {
    const user = userEvent.setup();
    const oldCatalog = deferred<unknown[]>();
    installAppMock({
      RemoteDeviceList: vi.fn(() =>
        Promise.resolve([
          {
            id: 7,
            name: "old-daemon",
            daemonFingerprint: "sha256:old-daemon",
            online: true,
            supportsLLMModelTarget: true,
          },
          {
            id: 8,
            name: "current-daemon",
            daemonFingerprint: "sha256:current-daemon",
            online: true,
            supportsLLMModelTarget: true,
          },
        ]),
      ),
      RemoteDeviceListProviders: vi.fn((...args: unknown[]) =>
        Number(args[0]) === 7
          ? oldCatalog.promise
          : Promise.resolve([
              {
                key: "key-1",
                name: "Anthropic",
                type: "anthropic",
                defaultModelKey: "mk-1",
                models: [
                  {
                    key: "mk-1",
                    modelId: "claude-sonnet-4-6",
                    enabled: true,
                  },
                ],
              },
            ]),
      ),
    });
    render(<AgentBackendsPanel />);
    await screen.findByText("默认助手");
    await user.click(screen.getByRole("button", { name: /New Backend/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(
      within(dialog).getByRole("radio", { name: /Claude Code CLI/ }),
    );
    await user.click(
      within(dialog).getByRole("combobox", { name: "Runtime Device" }),
    );
    await user.click(await screen.findByRole("option", { name: /old-daemon/ }));
    await user.click(
      within(dialog).getByRole("combobox", { name: "Runtime Device" }),
    );
    await user.click(
      await screen.findByRole("option", { name: /current-daemon/ }),
    );
    await waitFor(() =>
      expect(appMocks.RemoteDeviceListProviders).toHaveBeenCalledWith(8),
    );
    oldCatalog.resolve([]);
    await Promise.resolve();
    await user.click(
      within(dialog).getByRole("button", { name: "Model binding" }),
    );

    expect(
      screen.queryByRole("button", {
        name: "Sync Anthropic to this device",
      }),
    ).not.toBeInTheDocument();
  });

  it("Given a paired remote daemon has no provider catalog and lacks fixed-model capability, When the binding picker opens, Then fixed models fail closed", async () => {
    const user = userEvent.setup();
    installAppMock({
      RemoteDeviceList: vi.fn(() =>
        Promise.resolve([
          {
            id: 7,
            name: "legacy-daemon",
            daemonFingerprint: "sha256:legacy-daemon",
            online: true,
            supportsLLMModelTarget: false,
          },
        ]),
      ),
      RemoteDeviceListProviders: vi.fn(() => Promise.resolve([])),
    });
    render(<AgentBackendsPanel />);
    await screen.findByText("默认助手");
    await user.click(screen.getByRole("button", { name: /New Backend/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(
      within(dialog).getByRole("radio", { name: /Claude Code CLI/ }),
    );
    await user.click(
      within(dialog).getByRole("combobox", { name: "Runtime Device" }),
    );
    await user.click(
      await screen.findByRole("option", { name: /legacy-daemon/ }),
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Model binding" }),
    );

    const fixedOption = (
      await screen.findAllByRole("option", {
        name: /claude-sonnet-4-6/,
      })
    ).find((option) => option.getAttribute("data-kind") === "fixed");
    expect(fixedOption).toHaveAttribute("aria-disabled", "true");
  });

  it("Given an editor draft, When runtime and target change, Then the effective configuration summary updates live and explains whether saving is possible", async () => {
    const user = userEvent.setup();
    installAppMock({
      RemoteDeviceList: vi.fn(() =>
        Promise.resolve([
          {
            id: 7,
            name: "linux-srv",
            daemonFingerprint: "sha256:linux-srv",
            online: true,
            supportsLLMModelTarget: true,
          },
        ]),
      ),
      RemoteDeviceListProviders: vi.fn(() =>
        Promise.resolve([
          {
            key: "key-1",
            name: "Anthropic",
            type: "anthropic",
            defaultModelKey: "mk-1",
            models: [
              {
                key: "mk-1",
                modelId: "claude-sonnet-4-6",
                enabled: true,
              },
              {
                key: "mk-opus",
                modelId: "claude-opus-4-1",
                enabled: true,
              },
            ],
          },
        ]),
      ),
      ListLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [
            mockModel(1, "mk-1", "claude-sonnet-4-6"),
            mockModel(1, "mk-opus", "claude-opus-4-1"),
          ],
        }),
      ),
    });
    render(<AgentBackendsPanel />);
    await screen.findByText("默认助手");
    await user.click(screen.getByRole("button", { name: /New Backend/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(
      within(dialog).getByRole("radio", { name: /Claude Code CLI/ }),
    );
    const summary = within(dialog).getByTestId("effective-config-summary");
    expect(summary).toHaveTextContent(/Local/);
    expect(summary).toHaveTextContent(/CLI login state/);
    // 校验通过时给一条正向结论行，而不是一个"可保存"小徽标。
    expect(summary).toHaveTextContent(/Ready to save · all checks passed/i);
    expect(summary).toHaveTextContent(/0 agents/);

    // 运行位置要说清"到底会跑哪个可执行文件"，自定义 CLI 路径必须回显。
    await user.type(
      within(dialog).getByPlaceholderText("/usr/local/bin/claude"),
      "/opt/homebrew/bin/claude",
    );
    expect(within(summary).getByTestId("summary-runtime")).toHaveTextContent(
      "/opt/homebrew/bin/claude",
    );

    await user.click(
      within(dialog).getByRole("combobox", { name: "Runtime Device" }),
    );
    await user.click(await screen.findByRole("option", { name: /linux-srv/ }));
    expect(summary).toHaveTextContent("linux-srv");
    expect(within(dialog).getByLabelText("Name")).toHaveValue(
      "linux-srv · Claude Code",
    );

    await user.click(
      within(dialog).getByRole("button", { name: "Model binding" }),
    );
    await user.click(
      await screen.findByRole("option", { name: /claude-opus-4-1/ }),
    );
    expect(summary).toHaveTextContent(/Anthropic/);
    expect(summary).toHaveTextContent(/claude-opus-4-1/);
    expect(summary).toHaveTextContent(/Fixed/);

    await user.clear(within(dialog).getByLabelText("Name"));
    expect(summary).toHaveTextContent(/Cannot save/);
    expect(summary).toHaveTextContent(/Enter a backend name/i);
  });

  it("does not show implementation compatibility terminology and only explains the empty-compatible-provider case with a settings action", async () => {
    const user = userEvent.setup();
    const onOpenLlmProviders = vi.fn();
    installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({
          items: [
            {
              id: 2,
              type: "openai-response",
              name: "OpenAI",
              providerKey: "key-2",
              enabled: true,
              defaultModelKey: "mk-2",
            },
          ],
        }),
      ),
    });
    render(<AgentBackendsPanel onOpenLlmProviders={onOpenLlmProviders} />);
    await screen.findByText("默认助手");
    await user.click(screen.getByRole("button", { name: /New Backend/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(
      within(dialog).getByRole("radio", { name: /Claude Code CLI/ }),
    );
    expect(within(dialog).queryByText(/Strict match/)).not.toBeInTheDocument();
    expect(
      within(dialog).getByText(
        /No providers compatible with this backend type/,
      ),
    ).toBeInTheDocument();
    await user.click(
      within(dialog).getByRole("button", { name: "Configure LLM providers" }),
    );
    expect(onOpenLlmProviders).toHaveBeenCalledTimes(1);
  });

  it("shows a provider-empty hint + configure button when the provider list is empty (task 8)", async () => {
    const user = userEvent.setup();
    const onOpenLlmProviders = vi.fn();
    installAppMock({
      ListLLMProviders: vi.fn(() => Promise.resolve({ items: [] })),
    });
    render(<AgentBackendsPanel onOpenLlmProviders={onOpenLlmProviders} />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));

    const dialog = await screen.findByRole("dialog");
    expect(
      within(dialog).getByText("No LLM providers available yet"),
    ).toBeInTheDocument();
    await user.click(
      within(dialog).getByRole("button", { name: "Configure LLM providers" }),
    );
    expect(onOpenLlmProviders).toHaveBeenCalledTimes(1);
  });

  it("submits create dialog with builtin type", async () => {
    const user = userEvent.setup();
    const mocks = installAppMock();
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));

    const dialog = await screen.findByRole("dialog");
    // The Input is inside a <label> whose text node says "名称". Use placeholder
    // to grab it directly since shadcn Input doesn't tie label via htmlFor here.
    const nameInput = within(dialog).getByPlaceholderText(
      "Example: Local · Claude Code",
    );
    fireEvent.change(nameInput, { target: { value: "新助手" } });

    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(mocks.CreateAgentBackend).toHaveBeenCalledWith(
        expect.objectContaining({
          type: "builtin",
          name: "新助手",
          llmProviderKey: "key-1",
          cliPath: "",
        }),
      );
    });
  });

  it("clicking 测试连接 on a row shows success flash with latency + reply", async () => {
    const mocks = installAppMock({
      TestAgentBackend: vi.fn(() =>
        Promise.resolve({ ok: true, latencyMs: 128, message: "pong" }),
      ),
    });
    render(<AgentBackendsPanel />);
    await screen.findByText("默认助手");

    const row = screen
      .getByText("默认助手")
      .closest('[role="listitem"]') as HTMLElement;
    fireEvent.click(
      within(row).getByRole("button", { name: /Test connection/ }),
    );

    await waitFor(() => {
      expect(screen.getByText(/128ms/)).toBeInTheDocument();
      expect(screen.getByText(/pong/)).toBeInTheDocument();
    });
    expect(mocks.TestAgentBackend).toHaveBeenCalledWith(
      expect.objectContaining({
        id: 1,
        useDraft: false,
        type: "",
        name: "",
        llmProviderKey: "",
        cliPath: "",
      }),
    );
  });

  it("clicking 测试连接 on a row shows error flash on OK=false", async () => {
    installAppMock({
      TestAgentBackend: vi.fn(() =>
        Promise.resolve({
          ok: false,
          latencyMs: 30,
          message: "401 Unauthorized",
        }),
      ),
    });
    render(<AgentBackendsPanel />);
    await screen.findByText("默认助手");

    const row = screen
      .getByText("默认助手")
      .closest('[role="listitem"]') as HTMLElement;
    fireEvent.click(
      within(row).getByRole("button", { name: /Test connection/ }),
    );

    await waitFor(() =>
      expect(screen.getByText(/401 Unauthorized/)).toBeInTheDocument(),
    );
  });

  it("clicking 测试连接 in dialog sends draft fields", async () => {
    const mocks = installAppMock({
      TestAgentBackend: vi.fn(() =>
        Promise.resolve({ ok: true, latencyMs: 99, message: "pong" }),
      ),
    });
    render(<AgentBackendsPanel />);
    await screen.findByText("默认助手");

    fireEvent.click(screen.getByRole("button", { name: /New Backend/ }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByPlaceholderText(/Claude Code/), {
      target: { value: "draft-name" },
    });

    fireEvent.click(
      within(dialog).getByRole("button", { name: /Test Connection/ }),
    );

    await waitFor(() =>
      expect(mocks.TestAgentBackend).toHaveBeenCalledWith(
        expect.objectContaining({
          id: 0,
          useDraft: true,
          type: "builtin",
          name: "draft-name",
          llmProviderKey: expect.any(String),
          cliPath: "",
        }),
      ),
    );
  });

  it("dialog 测试连接 result is shown inside the dialog (not hidden behind overlay)", async () => {
    installAppMock({
      TestAgentBackend: vi.fn(() =>
        Promise.resolve({ ok: true, latencyMs: 87, message: "pong" }),
      ),
    });
    render(<AgentBackendsPanel />);
    await screen.findByText("默认助手");

    fireEvent.click(screen.getByRole("button", { name: /New Backend/ }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByPlaceholderText(/Claude Code/), {
      target: { value: "draft-name" },
    });
    fireEvent.click(
      within(dialog).getByRole("button", { name: /Test Connection/ }),
    );

    await waitFor(() => {
      expect(within(dialog).getByText(/87ms/)).toBeInTheDocument();
      expect(within(dialog).getByText(/pong/)).toBeInTheDocument();
    });
  });

  it("dialog 测试结果落在 footer，不在 body 滚动区里，避免长表单时被挤到看不到", async () => {
    installAppMock({
      TestAgentBackend: vi.fn(() =>
        Promise.resolve({ ok: true, latencyMs: 87, message: "pong" }),
      ),
    });
    render(<AgentBackendsPanel />);
    await screen.findByText("默认助手");

    fireEvent.click(screen.getByRole("button", { name: /New Backend/ }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByPlaceholderText(/Claude Code/), {
      target: { value: "draft-name" },
    });
    fireEvent.click(
      within(dialog).getByRole("button", { name: /Test Connection/ }),
    );

    const pong = await within(dialog).findByText(/pong/);
    const footer = dialog.querySelector(
      '[data-slot="dialog-footer"]',
    ) as HTMLElement | null;
    const body = dialog.querySelector(
      '[data-slot="dialog-body"]',
    ) as HTMLElement | null;
    expect(footer).not.toBeNull();
    expect(body).not.toBeNull();
    expect(footer!.contains(pong)).toBe(true);
    expect(body!.contains(pong)).toBe(false);
  });

  it("新建保存失败时错误显示在弹窗内，而不是表格提示区", async () => {
    const user = userEvent.setup();
    installAppMock({
      CreateAgentBackend: vi.fn(() =>
        Promise.reject(new Error("backend name exists")),
      ),
    });
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));

    const dialog = await screen.findByRole("dialog");
    fireEvent.change(
      within(dialog).getByPlaceholderText("Example: Local · Claude Code"),
      { target: { value: "Duplicate backend" } },
    );

    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(
        within(dialog).getByText("backend name exists"),
      ).toBeInTheDocument();
    });

    expect(
      screen.getAllByRole("status").filter((node) => !dialog.contains(node)),
    ).toHaveLength(0);
  });

  it("claudecode/codex 行未关联供应商时显示「走 CLI 自身登录」而非需处理", async () => {
    installAppMock({
      ListAgentBackends: vi.fn(() =>
        Promise.resolve({
          items: [
            {
              id: 9,
              type: "claudecode",
              name: "无 provider 的 claude",
              llmProviderKey: "",
              llmProviderName: "",
              llmProviderType: "",
              llmProviderModel: "",
              llmProviderActive: false,
              cliPath: "",
              agentCount: 0,
              createtime: 0,
              updatetime: 0,
            },
          ],
        }),
      ),
    });
    render(<AgentBackendsPanel />);

    const list = await screen.findByRole("list", {
      name: "Agent backend list",
    });
    await waitFor(() => {
      expect(
        within(list).getByText("无 provider 的 claude"),
      ).toBeInTheDocument();
      expect(within(list).getByText(/Use CLI login/)).toBeInTheDocument();
      expect(within(list).queryByText("Needs action")).not.toBeInTheDocument();
    });
  });

  it.each([
    ["claudecode", "无 provider 的 claude", "Anthropic", "anthropic"],
    ["codex", "无 provider 的 codex", "OpenAI", "openai-response"],
    ["piagent", "Pi Agent", "", ""],
  ])(
    "编辑 %s 且未关联供应商时不显示原供应商停用提示",
    async (type, name, providerName, providerType) => {
      const user = userEvent.setup();
      installAppMock({
        ListAgentBackends: vi.fn(() =>
          Promise.resolve({
            items: [
              {
                id: 9,
                type,
                name,
                llmProviderKey: "",
                llmProviderName: "",
                llmProviderType: "",
                llmProviderModel: "",
                llmProviderActive: false,
                cliPath: "",
                agentCount: 0,
                createtime: 0,
                updatetime: 0,
              },
            ],
          }),
        ),
        ListLLMProviders: vi.fn(() =>
          Promise.resolve({
            items: [
              {
                id: 1,
                type: providerType,
                name: providerName,
                providerKey: "key-1",
                baseUrl: "",
                maskedApiKey: "sk-•••",
                hasApiKey: true,
                model:
                  providerType === "anthropic"
                    ? "claude-sonnet-4-6"
                    : "gpt-5-codex",
                maxOutput: 0,
                contextWindow: 0,
                createtime: 0,
                updatetime: 0,
              },
            ],
          }),
        ),
      });
      render(<AgentBackendsPanel />);

      await screen.findByText(name);
      const row = screen
        .getByText(name)
        .closest('[role="listitem"]') as HTMLElement;
      await user.click(within(row).getByRole("button", { name: /Edit/ }));

      const dialog = await screen.findByRole("dialog");
      expect(
        within(dialog).queryByText(/original LLM provider is disabled/),
      ).not.toBeInTheDocument();
      expect(
        within(dialog).getByRole("button", { name: "Model binding" }),
      ).toHaveTextContent(/CLI login state/);
    },
  );

  it("新建 pi-agent 时不需要 provider，保存时提交 type=piagent 和 cliPath", async () => {
    const user = userEvent.setup();
    const mocks = installAppMock({
      ResolveAgentBackendCLIPath: vi.fn(() =>
        Promise.resolve({ path: "/opt/homebrew/bin/pi", found: true }),
      ),
    });
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));

    const dialog = await screen.findByRole("dialog");
    fireEvent.change(
      within(dialog).getByPlaceholderText("Example: Local · Claude Code"),
      { target: { value: "本机 Pi" } },
    );
    await user.click(
      within(dialog).getByRole("radio", { name: /Pi Agent CLI/ }),
    );

    const input = within(dialog).getByPlaceholderText(
      "/usr/local/bin/pi",
    ) as HTMLInputElement;
    await waitFor(() => expect(input.value).toBe("/opt/homebrew/bin/pi"));
    // piagent 现在显示可选的 provider 选择器（默认未关联走 CLI 自身登录）。
    expect(
      within(dialog).getByRole("button", { name: "Model binding" }),
    ).toBeInTheDocument();

    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(mocks.CreateAgentBackend).toHaveBeenCalledWith(
        expect.objectContaining({
          type: "piagent",
          name: "本机 Pi",
          llmProviderKey: "",
          cliPath: "/opt/homebrew/bin/pi",
        }),
      );
    });
  });

  it("piagent 编辑器列出三类 LLM 供应商，可选其一保存绑定", async () => {
    const user = userEvent.setup();
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({
          items: [
            {
              id: 1,
              type: "anthropic",
              name: "Anthropic",
              providerKey: "k-anthropic",
              baseUrl: "",
              maskedApiKey: "sk-•••",
              hasApiKey: true,
              enabled: true,
              defaultModelKey: "mk-anthropic",
              createtime: 0,
              updatetime: 0,
            },
            {
              id: 2,
              type: "openai-chat",
              name: "OpenAI Chat",
              providerKey: "k-chat",
              baseUrl: "",
              maskedApiKey: "sk-•••",
              hasApiKey: true,
              enabled: true,
              defaultModelKey: "mk-chat",
              createtime: 0,
              updatetime: 0,
            },
            {
              id: 3,
              type: "openai-response",
              name: "OpenAI Response",
              providerKey: "k-response",
              baseUrl: "",
              maskedApiKey: "sk-•••",
              hasApiKey: true,
              enabled: true,
              defaultModelKey: "mk-response",
              createtime: 0,
              updatetime: 0,
            },
          ],
        }),
      ),
      ListLLMModels: vi.fn((...args: unknown[]) => {
        const req = args[0] as { id?: number } | undefined;
        const byId: Record<number, unknown[]> = {
          1: [mockModel(1, "mk-anthropic", "claude-sonnet-4-6")],
          2: [mockModel(2, "mk-chat", "gpt-5")],
          3: [mockModel(3, "mk-response", "gpt-5-codex")],
        };
        return Promise.resolve({ items: byId[Number(req?.id)] ?? [] });
      }),
    });
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));

    const dialog = await screen.findByRole("dialog");
    fireEvent.change(
      within(dialog).getByPlaceholderText("Example: Local · Claude Code"),
      { target: { value: "Pi 绑供应商" } },
    );
    await user.click(
      within(dialog).getByRole("radio", { name: /Pi Agent CLI/ }),
    );

    await user.click(
      within(dialog).getByRole("button", { name: "Model binding" }),
    );
    // piagent 三类全收：anthropic / openai-chat / openai-response 都要列出来。
    const defaultOptions = await screen.findAllByRole("option", {
      name: /Follow this provider's default/,
    });
    expect(defaultOptions).toHaveLength(3);
    expect(screen.getAllByText("Anthropic").length).toBeGreaterThan(0);
    expect(screen.getAllByText("OpenAI Chat").length).toBeGreaterThan(0);
    expect(screen.getAllByText("OpenAI Response").length).toBeGreaterThan(0);
    await user.click(defaultOptions[2]);

    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(mocks.CreateAgentBackend).toHaveBeenCalledWith(
        expect.objectContaining({
          type: "piagent",
          name: "Pi 绑供应商",
          llmProviderKey: "k-response",
        }),
      );
    });
  });

  it("piagent 绑定 Model 为空的供应商时显示校验提示并阻止保存", async () => {
    const user = userEvent.setup();
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({
          items: [
            {
              id: 1,
              type: "anthropic",
              name: "Anthropic",
              providerKey: "k-empty-model",
              baseUrl: "",
              maskedApiKey: "sk-•••",
              hasApiKey: true,
              enabled: true,
              defaultModelKey: "mk-missing",
              createtime: 0,
              updatetime: 0,
            },
          ],
        }),
      ),
      // 目录里没有该 provider 的模型 → provider-default 解析不出默认模型。
      ListLLMModels: vi.fn(() => Promise.resolve({ items: [] })),
    });
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));

    const dialog = await screen.findByRole("dialog");
    fireEvent.change(
      within(dialog).getByPlaceholderText("Example: Local · Claude Code"),
      { target: { value: "Pi 空模型" } },
    );
    await user.click(
      within(dialog).getByRole("radio", { name: /Pi Agent CLI/ }),
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Model binding" }),
    );
    await user.click(
      screen.getByRole("option", { name: /Follow this provider's default/ }),
    );

    // 选中的供应商 Model 为空 → 可见校验提示 + Save 被禁用。
    expect(
      within(dialog).getByText("Provider has no default model"),
    ).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Save" })).toBeDisabled();
    expect(mocks.CreateAgentBackend).not.toHaveBeenCalled();
  });

  it("新建 claudecode 时允许不选 provider 提交 llmProviderKey 空串", async () => {
    const user = userEvent.setup();
    const mocks = installAppMock();
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));

    const dialog = await screen.findByRole("dialog");
    fireEvent.change(
      within(dialog).getByPlaceholderText("Example: Local · Claude Code"),
      { target: { value: "claude 走自身登录" } },
    );

    // 切换到 Claude Code CLI 类型 → provider 默认为空（CLI 自身登录）。
    await user.click(
      within(dialog).getByRole("radio", { name: /Claude Code CLI/ }),
    );

    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(mocks.CreateAgentBackend).toHaveBeenCalledWith(
        expect.objectContaining({
          type: "claudecode",
          name: "claude 走自身登录",
          llmProviderKey: "",
        }),
      );
    });
  });

  it("新建 claudecode 保持本地运行时提交 deviceId 为空串", async () => {
    const user = userEvent.setup();
    const mocks = installAppMock({
      RemoteDeviceList: vi.fn(() =>
        Promise.resolve([{ id: 7, name: "mac-mini", online: true }]),
      ),
    });
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));

    const dialog = await screen.findByRole("dialog");
    fireEvent.change(
      within(dialog).getByPlaceholderText("Example: Local · Claude Code"),
      { target: { value: "本地 claude" } },
    );
    await user.click(
      within(dialog).getByRole("radio", { name: /Claude Code CLI/ }),
    );

    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(mocks.CreateAgentBackend).toHaveBeenCalledWith(
        expect.objectContaining({
          type: "claudecode",
          name: "本地 claude",
          deviceId: "",
        }),
      );
    });
  });

  it("保存远端 claudecode 且远端缺少 provider 时提示同步，确认后先同步再保存", async () => {
    const user = userEvent.setup();
    let synced = false;
    const mocks = installAppMock({
      RemoteDeviceList: vi.fn(() =>
        Promise.resolve([
          {
            id: 7,
            name: "linux-srv",
            daemonFingerprint: "sha256:remote-daemon",
            online: true,
            supportsLLMModelTarget: true,
          },
        ]),
      ),
      RemoteDeviceListProviders: vi.fn(() =>
        Promise.resolve(
          synced
            ? [
                {
                  key: "key-1",
                  name: "Anthropic",
                  type: "anthropic",
                  defaultModelKey: "mk-1",
                  models: [
                    {
                      key: "mk-1",
                      modelId: "claude-sonnet-4-6",
                      enabled: true,
                    },
                  ],
                },
              ]
            : [],
        ),
      ),
      RemoteDeviceSyncProvider: vi.fn(() => {
        synced = true;
        return Promise.resolve(undefined);
      }),
    });
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));

    const dialog = await screen.findByRole("dialog");
    fireEvent.change(
      within(dialog).getByPlaceholderText("Example: Local · Claude Code"),
      { target: { value: "远端 claude" } },
    );
    await user.click(
      within(dialog).getByRole("radio", { name: /Claude Code CLI/ }),
    );

    await user.click(
      within(dialog).getByRole("combobox", { name: "Runtime Device" }),
    );
    await user.click(screen.getByRole("option", { name: /linux-srv/ }));

    await user.click(
      within(dialog).getByRole("button", { name: "Model binding" }),
    );
    await user.click(
      screen.getByRole("button", {
        name: "Sync Anthropic to this device",
      }),
    );

    const syncDialog = await screen.findByRole("dialog", {
      name: /Sync Remote LLM Provider/,
    });
    expect(
      within(syncDialog).getByText(
        /API key, default model and model catalog to the remote agentred state file/,
      ),
    ).toBeInTheDocument();
    expect(mocks.CreateAgentBackend).not.toHaveBeenCalled();

    await user.click(
      within(syncDialog).getByRole("button", { name: "Sync to Remote" }),
    );
    await waitFor(() =>
      expect(mocks.RemoteDeviceSyncProvider).toHaveBeenCalledWith(7, "key-1"),
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Model binding" }),
    );
    await user.click(
      await screen.findByRole("option", {
        name: /Follow this provider's default/,
      }),
    );
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(mocks.CreateAgentBackend).toHaveBeenCalledWith(
        expect.objectContaining({
          type: "claudecode",
          name: "远端 claude",
          deviceId: "sha256:remote-daemon",
          llmProviderKey: "key-1",
        }),
      );
    });
  });

  it("选择远端 provider 后在编辑弹窗里显示同步入口，手动同步成功后刷新目录并保留草稿", async () => {
    const user = userEvent.setup();
    const mocks = installAppMock({
      RemoteDeviceList: vi.fn(() =>
        Promise.resolve([{ id: 7, name: "linux-srv", online: true }]),
      ),
    });
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));

    const editorDialog = await screen.findByRole("dialog");
    fireEvent.change(
      within(editorDialog).getByPlaceholderText("Example: Local · Claude Code"),
      { target: { value: "远端 claude" } },
    );
    await user.click(
      within(editorDialog).getByRole("radio", { name: /Claude Code CLI/ }),
    );
    await user.click(
      within(editorDialog).getByRole("combobox", { name: "Runtime Device" }),
    );
    await user.click(screen.getByRole("option", { name: /linux-srv/ }));
    await user.click(
      within(editorDialog).getByRole("button", { name: "Model binding" }),
    );
    await user.click(
      screen.getByRole("button", {
        name: "Sync Anthropic to this device",
      }),
    );

    const syncDialog = await screen.findByRole("dialog", {
      name: /Sync Remote LLM Provider/,
    });
    await user.click(
      within(syncDialog).getByRole("button", { name: "Sync to Remote" }),
    );

    await waitFor(() => {
      expect(mocks.RemoteDeviceSyncProvider).toHaveBeenCalledWith(7, "key-1");
      expect(mocks.CreateAgentBackend).not.toHaveBeenCalled();
      expect(screen.getByText(/Remote provider synced/)).toBeInTheDocument();
      expect(editorDialog).toBeInTheDocument();
    });
  });

  it("手动同步失败时错误显示在同步弹窗内，不刷到表格顶部", async () => {
    const user = userEvent.setup();
    const mocks = installAppMock({
      RemoteDeviceList: vi.fn(() =>
        Promise.resolve([{ id: 7, name: "linux-srv", online: true }]),
      ),
      RemoteDeviceSyncProvider: vi.fn(() =>
        Promise.reject(new Error("remote sync failed")),
      ),
    });
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));

    const editorDialog = await screen.findByRole("dialog");
    fireEvent.change(
      within(editorDialog).getByPlaceholderText("Example: Local · Claude Code"),
      { target: { value: "远端 claude" } },
    );
    await user.click(
      within(editorDialog).getByRole("radio", { name: /Claude Code CLI/ }),
    );
    await user.click(
      within(editorDialog).getByRole("combobox", { name: "Runtime Device" }),
    );
    await user.click(screen.getByRole("option", { name: /linux-srv/ }));
    await user.click(
      within(editorDialog).getByRole("button", { name: "Model binding" }),
    );
    await user.click(
      screen.getByRole("button", {
        name: "Sync Anthropic to this device",
      }),
    );

    const syncDialog = await screen.findByRole("dialog", {
      name: /Sync Remote LLM Provider/,
    });
    await user.click(
      within(syncDialog).getByRole("button", { name: "Sync to Remote" }),
    );

    await waitFor(() => {
      expect(mocks.RemoteDeviceSyncProvider).toHaveBeenCalledWith(7, "key-1");
      expect(within(syncDialog).getByText("Sync Failed")).toBeInTheDocument();
      expect(
        within(syncDialog).getByText(/remote sync failed/),
      ).toBeInTheDocument();
      expect(screen.getAllByText(/remote sync failed/)).toHaveLength(1);
      expect(mocks.CreateAgentBackend).not.toHaveBeenCalled();
    });
  });

  it("手动同步遇到旧版远端 Secret Service 缺失时提示升级到状态文件存储", async () => {
    const user = userEvent.setup();
    installAppMock({
      RemoteDeviceList: vi.fn(() =>
        Promise.resolve([{ id: 7, name: "linux-srv", online: true }]),
      ),
      RemoteDeviceSyncProvider: vi.fn(() =>
        Promise.reject(
          new Error(
            "remote llm.upsert: keychain set: The name org.freedesktop.secrets was not provided by any .service files",
          ),
        ),
      ),
    });
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));

    const editorDialog = await screen.findByRole("dialog");
    fireEvent.change(
      within(editorDialog).getByPlaceholderText("Example: Local · Claude Code"),
      { target: { value: "远端 claude" } },
    );
    await user.click(
      within(editorDialog).getByRole("radio", { name: /Claude Code CLI/ }),
    );
    await user.click(
      within(editorDialog).getByRole("combobox", { name: "Runtime Device" }),
    );
    await user.click(screen.getByRole("option", { name: /linux-srv/ }));
    await user.click(
      within(editorDialog).getByRole("button", { name: "Model binding" }),
    );
    await user.click(
      screen.getByRole("button", {
        name: "Sync Anthropic to this device",
      }),
    );

    const syncDialog = await screen.findByRole("dialog", {
      name: /Sync Remote LLM Provider/,
    });
    await user.click(
      within(syncDialog).getByRole("button", { name: "Sync to Remote" }),
    );

    await waitFor(() => {
      expect(within(syncDialog).getByText("Sync Failed")).toBeInTheDocument();
      expect(
        within(syncDialog).getByText(
          /older remote agentred is still writing to the system keychain/i,
        ),
      ).toBeInTheDocument();
      expect(
        within(syncDialog).getByText(
          /current version writes directly to the agentred state file/i,
        ),
      ).toBeInTheDocument();
      expect(
        within(syncDialog).getByText(/org\.freedesktop\.secrets/),
      ).toBeInTheDocument();
    });
    expect(
      screen.queryAllByText(
        /older remote agentred is still writing to the system keychain/i,
      ),
    ).toHaveLength(1);
  });

  it("Given a saved remote backend DTO, When it is edited and saved, Then its deviceId stays selected and remote Provider sync remains available", async () => {
    const user = userEvent.setup();
    const mocks = installAppMock({
      ListAgentBackends: vi.fn(() =>
        Promise.resolve({
          items: [
            {
              id: 12,
              type: "claudecode",
              name: "saved remote claude",
              deviceId: "sha256:remote-daemon",
              llmProviderKey: "key-1",
              llmModelKey: "",
              llmProviderName: "Anthropic",
              llmProviderType: "anthropic",
              llmProviderModel: "claude-sonnet-4-6",
              llmProviderActive: true,
              cliPath: "claude",
              modelRoutes: {},
              envJson: "{}",
              agentCount: 0,
              createtime: 0,
              updatetime: 0,
            },
          ],
        }),
      ),
      RemoteDeviceList: vi.fn(() =>
        Promise.resolve([
          {
            id: 7,
            name: "linux-srv",
            daemonFingerprint: "sha256:remote-daemon",
            online: true,
          },
        ]),
      ),
      RemoteDeviceListProviders: vi.fn(() =>
        Promise.resolve([
          {
            key: "key-1",
            name: "Anthropic",
            type: "anthropic",
            defaultModelKey: "mk-1",
            models: [
              {
                key: "mk-1",
                modelId: "claude-sonnet-4-6",
                enabled: true,
              },
            ],
          },
        ]),
      ),
    });
    render(<AgentBackendsPanel />);

    const row = (await screen.findByText("saved remote claude")).closest(
      '[role="listitem"]',
    ) as HTMLElement;
    await user.click(within(row).getByRole("button", { name: /Edit/ }));

    const dialog = await screen.findByRole("dialog");
    await waitFor(() => {
      expect(
        within(dialog).getByRole("combobox", { name: "Runtime Device" }),
      ).toHaveTextContent("linux-srv");
      expect(
        within(dialog).getByText("Remote Provider Sync"),
      ).toBeInTheDocument();
    });

    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(mocks.UpdateAgentBackend).toHaveBeenCalledWith(
        expect.objectContaining({
          id: 12,
          deviceId: "sha256:remote-daemon",
        }),
      );
    });
  });

  it("Given a remote provider-default key points to a disabled model, When Save is clicked, Then persistence fails closed", async () => {
    const user = userEvent.setup();
    const mocks = installAppMock({
      ListAgentBackends: vi.fn(() =>
        Promise.resolve({
          items: [
            {
              id: 13,
              type: "claudecode",
              name: "disabled remote default",
              deviceId: "sha256:remote-daemon",
              deviceName: "Remote daemon",
              llmProviderKey: "key-1",
              llmModelKey: "",
              cliPath: "claude",
              modelRoutes: {},
              envJson: "{}",
              agentCount: 0,
              createtime: 0,
              updatetime: 0,
            },
          ],
        }),
      ),
      RemoteDeviceList: vi.fn(() =>
        Promise.resolve([
          {
            id: 7,
            name: "Remote daemon",
            daemonFingerprint: "sha256:remote-daemon",
            online: true,
            supportsLLMModelTarget: true,
          },
        ]),
      ),
      RemoteDeviceListProviders: vi.fn(() =>
        Promise.resolve([
          {
            key: "key-1",
            name: "Anthropic",
            type: "anthropic",
            defaultModelKey: "mk-1",
            models: [
              {
                key: "mk-1",
                modelId: "claude-sonnet-4-6",
                enabled: false,
              },
            ],
          },
        ]),
      ),
    });
    render(<AgentBackendsPanel />);

    const row = (await screen.findByText("disabled remote default")).closest(
      '[role="listitem"]',
    ) as HTMLElement;
    await user.click(within(row).getByRole("button", { name: /Edit/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(mocks.UpdateAgentBackend).not.toHaveBeenCalled(),
    );
    expect(dialog).toHaveTextContent(/sync to this device first/i);
  });

  it("Given an existing remote fixed-model binding targets a daemon without fixed-model capability, When Save is clicked, Then persistence fails closed", async () => {
    const user = userEvent.setup();
    const mocks = installAppMock({
      ListAgentBackends: vi.fn(() =>
        Promise.resolve({
          items: [
            {
              id: 13,
              type: "claudecode",
              name: "legacy fixed",
              deviceId: "sha256:legacy-daemon",
              deviceName: "Legacy daemon",
              llmProviderKey: "key-1",
              llmModelKey: "mk-1",
              cliPath: "claude",
              modelRoutes: {},
              envJson: "{}",
              agentCount: 0,
              createtime: 0,
              updatetime: 0,
            },
          ],
        }),
      ),
      RemoteDeviceList: vi.fn(() =>
        Promise.resolve([
          {
            id: 7,
            name: "Legacy daemon",
            daemonFingerprint: "sha256:legacy-daemon",
            online: true,
            supportsLLMModelTarget: false,
          },
        ]),
      ),
      RemoteDeviceListProviders: vi.fn(() =>
        Promise.resolve([
          {
            key: "key-1",
            name: "Anthropic",
            type: "anthropic",
            defaultModelKey: "mk-1",
            models: [
              {
                key: "mk-1",
                modelId: "claude-sonnet-4-6",
                enabled: true,
              },
            ],
          },
        ]),
      ),
    });
    render(<AgentBackendsPanel />);

    const row = (await screen.findByText("legacy fixed")).closest(
      '[role="listitem"]',
    ) as HTMLElement;
    await user.click(within(row).getByRole("button", { name: /Edit/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(mocks.UpdateAgentBackend).not.toHaveBeenCalled(),
    );
    expect(dialog).toHaveTextContent(/does not support fixed models/i);
  });

  it("Given a saved account-desktop fingerprint without a paired agentred row, When it is edited, Then the named device remains visible and preserved", async () => {
    const user = userEvent.setup();
    const mocks = installAppMock({
      ListAgentBackends: vi.fn(() =>
        Promise.resolve({
          items: [
            {
              id: 13,
              type: "claudecode",
              name: "studio claude",
              deviceId: "sha256:studio-desktop",
              deviceName: "",
              llmProviderKey: "",
              llmModelKey: "",
              cliPath: "claude",
              modelRoutes: {},
              envJson: "{}",
              agentCount: 0,
              createtime: 0,
              updatetime: 0,
            },
          ],
        }),
      ),
      RemoteDeviceList: vi.fn(() => Promise.resolve([])),
      ServerListDevices: vi.fn(() =>
        Promise.resolve([
          {
            Fingerprint: "sha256:studio-desktop",
            Name: "Studio Mac",
          },
        ]),
      ),
    });
    render(<AgentBackendsPanel />);

    const row = (await screen.findByText("studio claude")).closest(
      '[role="listitem"]',
    ) as HTMLElement;
    await user.click(within(row).getByRole("button", { name: /Edit/ }));

    const dialog = await screen.findByRole("dialog");
    await waitFor(() => {
      expect(
        within(dialog).getByRole("combobox", { name: "Runtime Device" }),
      ).toHaveTextContent("Studio Mac");
      expect(
        within(dialog).getByTestId("effective-config-summary"),
      ).toHaveTextContent("Studio Mac");
    });
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(mocks.UpdateAgentBackend).toHaveBeenCalledWith(
        expect.objectContaining({
          id: 13,
          deviceId: "sha256:studio-desktop",
        }),
      ),
    );
  });

  it("Given a bound backend on an account desktop with no paired agentred row, When it is saved, Then it persists and no unusable sync entry is offered", async () => {
    const user = userEvent.setup();
    const mocks = installAppMock({
      ListAgentBackends: vi.fn(() =>
        Promise.resolve({
          items: [
            {
              id: 14,
              type: "claudecode",
              name: "studio bound claude",
              deviceId: "sha256:studio-desktop",
              deviceName: "",
              llmProviderKey: "key-1",
              llmModelKey: "",
              llmProviderName: "Anthropic",
              llmProviderType: "anthropic",
              llmProviderModel: "claude-sonnet-4-6",
              llmProviderActive: true,
              cliPath: "claude",
              modelRoutes: {},
              envJson: "{}",
              agentCount: 0,
              createtime: 0,
              updatetime: 0,
            },
          ],
        }),
      ),
      RemoteDeviceList: vi.fn(() => Promise.resolve([])),
      ServerListDevices: vi.fn(() =>
        Promise.resolve([
          { Fingerprint: "sha256:studio-desktop", Name: "Studio Mac" },
        ]),
      ),
    });
    render(<AgentBackendsPanel />);

    const row = (await screen.findByText("studio bound claude")).closest(
      '[role="listitem"]',
    ) as HTMLElement;
    await user.click(within(row).getByRole("button", { name: /Edit/ }));

    const dialog = await screen.findByRole("dialog");
    await waitFor(() =>
      expect(
        within(dialog).getByRole("combobox", { name: "Runtime Device" }),
      ).toHaveTextContent("Studio Mac"),
    );
    expect(
      within(dialog).queryByText("Remote Provider Sync"),
    ).not.toBeInTheDocument();

    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(mocks.UpdateAgentBackend).toHaveBeenCalledWith(
        expect.objectContaining({
          id: 14,
          deviceId: "sha256:studio-desktop",
          llmProviderKey: "key-1",
        }),
      ),
    );
    expect(
      screen.queryByRole("dialog", { name: /Sync Remote LLM Provider/ }),
    ).not.toBeInTheDocument();
  });

  it("Given a backend whose device id is this machine's own fingerprint, When the editor opens, Then no remote provider sync entry is offered", async () => {
    const user = userEvent.setup();
    installAppMock({
      ListAgentBackends: vi.fn(() =>
        Promise.resolve({
          items: [
            {
              id: 15,
              type: "claudecode",
              name: "self claude",
              deviceId: "sha256:local-desktop",
              deviceName: "",
              llmProviderKey: "key-1",
              llmModelKey: "",
              llmProviderName: "Anthropic",
              llmProviderType: "anthropic",
              llmProviderModel: "claude-sonnet-4-6",
              llmProviderActive: true,
              cliPath: "claude",
              modelRoutes: {},
              envJson: "{}",
              agentCount: 0,
              createtime: 0,
              updatetime: 0,
            },
          ],
        }),
      ),
    });
    render(<AgentBackendsPanel />);

    const row = (await screen.findByText("self claude")).closest(
      '[role="listitem"]',
    ) as HTMLElement;
    await user.click(within(row).getByRole("button", { name: /Edit/ }));

    const dialog = await screen.findByRole("dialog");
    await waitFor(() =>
      expect(
        within(dialog).getByRole("combobox", { name: "Runtime Device" }),
      ).toHaveTextContent("Local"),
    );
    expect(
      within(dialog).queryByText("Remote Provider Sync"),
    ).not.toBeInTheDocument();
  });

  it("Given a bypass backend pinned to this machine's own fingerprint, When the editor opens, Then the remote agentred root hint is not shown", async () => {
    const user = userEvent.setup();
    installAppMock({
      ListAgentBackends: vi.fn(() =>
        Promise.resolve({
          items: [
            {
              id: 16,
              type: "claudecode",
              name: "self bypass",
              deviceId: "sha256:local-desktop",
              deviceName: "",
              defaultPermissionMode: "bypassPermissions",
              llmProviderKey: "",
              llmModelKey: "",
              cliPath: "claude",
              modelRoutes: {},
              envJson: "{}",
              agentCount: 0,
              createtime: 0,
              updatetime: 0,
            },
          ],
        }),
      ),
    });
    render(<AgentBackendsPanel />);

    const row = (await screen.findByText("self bypass")).closest(
      '[role="listitem"]',
    ) as HTMLElement;
    await user.click(within(row).getByRole("button", { name: /Edit/ }));

    const dialog = await screen.findByRole("dialog");
    await waitFor(() =>
      expect(
        within(dialog).getByRole("combobox", { name: "Runtime Device" }),
      ).toHaveTextContent("Local"),
    );
    // The hint is about agentred running as root on another machine; the R13
    // canonical fingerprint of this desktop is not another machine.
    expect(within(dialog).queryByText(/remote agentred runs as root/i)).toBe(
      null,
    );
  });

  it("Given a fixed remote binding, When the picker's sync entry is opened and canceled, Then the draft binding is untouched", async () => {
    const user = userEvent.setup();
    installAppMock({
      ListAgentBackends: vi.fn(() =>
        Promise.resolve({
          items: [
            {
              id: 16,
              type: "claudecode",
              name: "remote fixed claude",
              deviceId: "sha256:remote-daemon",
              deviceName: "linux-srv",
              llmProviderKey: "key-1",
              llmModelKey: "mk-9",
              llmProviderName: "Anthropic",
              llmProviderType: "anthropic",
              llmProviderModel: "claude-opus-4-5",
              llmProviderActive: true,
              cliPath: "claude",
              modelRoutes: {},
              envJson: "{}",
              agentCount: 0,
              createtime: 0,
              updatetime: 0,
            },
          ],
        }),
      ),
      ListLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [
            mockModel(1, "mk-1", "claude-sonnet-4-6"),
            mockModel(1, "mk-9", "claude-opus-4-5"),
          ],
        }),
      ),
      RemoteDeviceList: vi.fn(() =>
        Promise.resolve([
          {
            id: 7,
            name: "linux-srv",
            daemonFingerprint: "sha256:remote-daemon",
            online: true,
            supportsLLMModelTarget: true,
          },
        ]),
      ),
      RemoteDeviceListProviders: vi.fn(() => Promise.resolve([])),
    });
    render(<AgentBackendsPanel />);

    const row = (await screen.findByText("remote fixed claude")).closest(
      '[role="listitem"]',
    ) as HTMLElement;
    await user.click(within(row).getByRole("button", { name: /Edit/ }));

    const dialog = await screen.findByRole("dialog");
    await user.click(
      within(dialog).getByRole("button", { name: "Model binding" }),
    );
    await user.click(
      await screen.findByRole("button", {
        name: "Sync Anthropic to this device",
      }),
    );
    const syncDialog = await screen.findByRole("dialog", {
      name: /Sync Remote LLM Provider/,
    });
    await user.click(
      within(syncDialog).getByRole("button", { name: /Cancel/ }),
    );

    await waitFor(() =>
      expect(
        within(dialog).getByTestId("effective-config-summary"),
      ).toHaveTextContent("claude-opus-4-5"),
    );
    expect(
      within(dialog).getByRole("button", { name: "Model binding" }),
    ).toHaveTextContent("claude-opus-4-5");
  });

  it("保存远端 claudecode 且 provider 已在远端时直接保存，不弹同步提示", async () => {
    const user = userEvent.setup();
    const mocks = installAppMock({
      RemoteDeviceList: vi.fn(() =>
        Promise.resolve([{ id: 7, name: "linux-srv", online: true }]),
      ),
      RemoteDeviceListProviders: vi.fn(() =>
        Promise.resolve([
          {
            key: "key-1",
            name: "Anthropic",
            type: "anthropic",
            defaultModelKey: "mk-1",
            models: [
              {
                key: "mk-1",
                modelId: "claude-sonnet-4-6",
                name: "claude-sonnet-4-6",
                enabled: true,
              },
            ],
          },
        ]),
      ),
    });
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));

    const dialog = await screen.findByRole("dialog");
    fireEvent.change(
      within(dialog).getByPlaceholderText("Example: Local · Claude Code"),
      { target: { value: "已同步 claude" } },
    );
    await user.click(
      within(dialog).getByRole("radio", { name: /Claude Code CLI/ }),
    );
    await user.click(
      within(dialog).getByRole("combobox", { name: "Runtime Device" }),
    );
    await user.click(screen.getByRole("option", { name: /linux-srv/ }));
    await user.click(
      within(dialog).getByRole("button", { name: "Model binding" }),
    );
    await user.click(
      screen.getByRole("option", { name: /Follow this provider's default/ }),
    );

    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(mocks.CreateAgentBackend).toHaveBeenCalledWith(
        expect.objectContaining({
          deviceId: "7",
          llmProviderKey: "key-1",
        }),
      );
    });
    expect(mocks.RemoteDeviceSyncProvider).not.toHaveBeenCalled();
    expect(
      screen.queryByRole("dialog", { name: /Sync Remote LLM Provider/ }),
    ).not.toBeInTheDocument();
  });

  it("编辑 claudecode 时可清除 provider 关联并提交 llmProviderKey 空串", async () => {
    const user = userEvent.setup();
    const mocks = installAppMock({
      ListAgentBackends: vi.fn(() =>
        Promise.resolve({
          items: [
            {
              id: 11,
              type: "claudecode",
              name: "走 gateway 的 claude",
              llmProviderKey: "key-1",
              llmProviderName: "Anthropic",
              llmProviderType: "anthropic",
              llmProviderModel: "claude-sonnet-4-6",
              llmProviderActive: true,
              cliPath: "",
              agentCount: 0,
              createtime: 0,
              updatetime: 0,
            },
          ],
        }),
      ),
    });
    render(<AgentBackendsPanel />);

    await screen.findByText("走 gateway 的 claude");
    const row = screen
      .getByText("走 gateway 的 claude")
      .closest('[role="listitem"]') as HTMLElement;
    await user.click(within(row).getByRole("button", { name: /Edit/ }));

    const dialog = await screen.findByRole("dialog");
    // 通过 Picker 顶部特殊项（CLI 自身登录态）清除 provider 关联。
    await user.click(
      within(dialog).getByRole("button", { name: "Model binding" }),
    );
    await user.click(
      await screen.findByRole("option", { name: /CLI login state/ }),
    );
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(mocks.UpdateAgentBackend).toHaveBeenCalledWith(
        expect.objectContaining({
          id: 11,
          llmProviderKey: "",
        }),
      );
    });
  });

  it("新建时切到 claudecode → 自动调 ResolveAgentBackendCLIPath 把识别到的路径填入 input", async () => {
    const user = userEvent.setup();
    const resolveFn = vi.fn(() =>
      Promise.resolve({ path: "/opt/homebrew/bin/claude", found: true }),
    );
    installAppMock({ ResolveAgentBackendCLIPath: resolveFn });
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(
      within(dialog).getByRole("radio", { name: /Claude Code CLI/ }),
    );

    await waitFor(() => {
      expect(resolveFn).toHaveBeenCalledWith(
        expect.objectContaining({ type: "claudecode" }),
      );
      const input = within(dialog).getByPlaceholderText(
        "/usr/local/bin/claude",
      ) as HTMLInputElement;
      expect(input.value).toBe("/opt/homebrew/bin/claude");
    });
  });

  it("切到 codex 时自动识别命中 → input 显示 codex 的绝对路径", async () => {
    const user = userEvent.setup();
    const resolveFn = vi.fn(() =>
      Promise.resolve({ path: "/usr/local/bin/codex", found: true }),
    );
    installAppMock({ ResolveAgentBackendCLIPath: resolveFn });
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("radio", { name: /Codex CLI/ }));

    await waitFor(() => {
      expect(resolveFn).toHaveBeenCalledWith(
        expect.objectContaining({ type: "codex" }),
      );
      const input = within(dialog).getByPlaceholderText(
        "/usr/local/bin/codex",
      ) as HTMLInputElement;
      expect(input.value).toBe("/usr/local/bin/codex");
    });
  });

  it("Codex approval options match codex-cli 0.145.0 and omit on-failure", async () => {
    const user = userEvent.setup();
    installAppMock();
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("radio", { name: /Codex CLI/ }));
    await user.click(
      within(dialog).getByRole("combobox", { name: "Approval Policy" }),
    );

    expect(
      screen.getByRole("option", { name: /trusted tools/ }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("option", { name: /model requests/ }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("option", { name: /Never ask/ }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("option", { name: /tool fails/ }),
    ).not.toBeInTheDocument();
  });

  it("codex 思考力度开放 xhigh，保存时透传 reasoningEffort=xhigh", async () => {
    const user = userEvent.setup();
    const mocks = installAppMock();
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));

    const dialog = await screen.findByRole("dialog");
    fireEvent.change(
      within(dialog).getByPlaceholderText("Example: Local · Claude Code"),
      { target: { value: "codex xhigh" } },
    );
    await user.click(within(dialog).getByRole("radio", { name: /Codex CLI/ }));

    await user.click(
      within(dialog).getByRole("combobox", { name: "Reasoning Effort" }),
    );
    expect(screen.getByRole("option", { name: /xhigh/ })).toBeInTheDocument();
    expect(
      screen.queryByRole("option", { name: /max/ }),
    ).not.toBeInTheDocument();
    await user.click(screen.getByRole("option", { name: /xhigh/ }));

    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(mocks.CreateAgentBackend).toHaveBeenCalledWith(
        expect.objectContaining({
          type: "codex",
          name: "codex xhigh",
          reasoningEffort: "xhigh",
        }),
      );
    });
  });

  it("自动识别未命中时不写入 input，input 维持空值（用户回退手填）", async () => {
    const user = userEvent.setup();
    installAppMock({
      ResolveAgentBackendCLIPath: vi.fn(() =>
        Promise.resolve({ path: "", found: false }),
      ),
    });
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(
      within(dialog).getByRole("radio", { name: /Claude Code CLI/ }),
    );

    // 给一点时机让 ResolveCLIPath 的 Promise 完成；命中分支已被其它用例覆盖，这里仅断终态。
    const input = within(dialog).getByPlaceholderText(
      "/usr/local/bin/claude",
    ) as HTMLInputElement;
    await waitFor(() => expect(input.value).toBe(""));
  });

  it("点「自动识别」按钮 → 用当前类型重跑探测并覆盖 input", async () => {
    const user = userEvent.setup();
    let nextPath = "/first/claude";
    const resolveFn = vi.fn(() =>
      Promise.resolve({ path: nextPath, found: true }),
    );
    installAppMock({ ResolveAgentBackendCLIPath: resolveFn });
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(
      within(dialog).getByRole("radio", { name: /Claude Code CLI/ }),
    );

    const input = within(dialog).getByPlaceholderText(
      "/usr/local/bin/claude",
    ) as HTMLInputElement;
    await waitFor(() => expect(input.value).toBe("/first/claude"));

    // 用户手改了值，然后点按钮重识别 → 按钮要覆盖手填值。
    fireEvent.change(input, { target: { value: "/wrong/path" } });
    nextPath = "/second/claude";
    // 打开对话框时会对三个 CLI 各探一次，所以只能比「点按钮前后」的增量，不能比总次数。
    const callsBeforeDetect = resolveFn.mock.calls.length;
    await user.click(within(dialog).getByRole("button", { name: /Detect/ }));

    await waitFor(() => expect(input.value).toBe("/second/claude"));
    expect(resolveFn.mock.calls.length).toBe(callsBeforeDetect + 1);
  });

  it("自动识别按钮未命中时显示 $PATH 提示且不改 input", async () => {
    const user = userEvent.setup();
    installAppMock({
      ResolveAgentBackendCLIPath: vi.fn(() =>
        Promise.resolve({ path: "", found: false }),
      ),
    });
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("radio", { name: /Codex CLI/ }));

    const input = within(dialog).getByPlaceholderText(
      "/usr/local/bin/codex",
    ) as HTMLInputElement;
    fireEvent.change(input, { target: { value: "/manual/codex" } });

    await user.click(within(dialog).getByRole("button", { name: /Detect/ }));

    await waitFor(() =>
      expect(
        within(dialog).getByText(/codex was not found in \$PATH/),
      ).toBeInTheDocument(),
    );
    // miss 时不能覆盖用户手填的值。
    expect(input.value).toBe("/manual/codex");
  });

  it("测试中显示取消按钮，点取消 → 调 CancelTestAgentBackend 同一 requestId", async () => {
    // 用一个永远不 resolve 的 Promise 模拟"卡住的测试"。
    let capturedRequestId = "";
    const cancelFn = vi.fn(() => Promise.resolve({ canceled: true }));
    installAppMock({
      TestAgentBackend: vi.fn((...args: unknown[]) => {
        const req = args[0] as { requestId?: string };
        capturedRequestId = req?.requestId ?? "";
        return new Promise(() => {}); // 永远 pending
      }),
      CancelTestAgentBackend: cancelFn,
    });
    render(<AgentBackendsPanel />);
    await screen.findByText("默认助手");

    const row = screen
      .getByText("默认助手")
      .closest('[role="listitem"]') as HTMLElement;
    fireEvent.click(
      within(row).getByRole("button", { name: /Test connection/ }),
    );

    // 按钮 title 切换为"取消测试"
    const cancelBtn = await within(row).findByRole("button", {
      name: /Cancel test/,
    });
    expect(capturedRequestId).not.toBe("");
    fireEvent.click(cancelBtn);

    await waitFor(() =>
      expect(cancelFn).toHaveBeenCalledWith(
        expect.objectContaining({ requestId: capturedRequestId }),
      ),
    );
    // UI 应恢复成"Test Connection"
    await waitFor(() =>
      expect(
        within(row).getByRole("button", { name: /Test connection/ }),
      ).toBeInTheDocument(),
    );
  });

  it("flash banner 长 message 被截断到 80 字 + …，完整内容放 title", async () => {
    const long = "x".repeat(300);
    installAppMock({
      TestAgentBackend: vi.fn(() =>
        Promise.resolve({ ok: false, latencyMs: 12, message: long }),
      ),
    });
    render(<AgentBackendsPanel />);
    await screen.findByText("默认助手");

    const row = screen
      .getByText("默认助手")
      .closest('[role="listitem"]') as HTMLElement;
    fireEvent.click(
      within(row).getByRole("button", { name: /Test connection/ }),
    );

    const banner = await screen.findByRole("status");
    // banner 文本应短于完整 message
    const span = banner.querySelector("span[title]") as HTMLElement | null;
    expect(span).not.toBeNull();
    expect(span!.textContent!.length).toBeLessThan(long.length);
    expect(span!.textContent!.endsWith("…")).toBe(true);
    // title 应包含完整 message
    expect(span!.getAttribute("title")).toContain(long);
  });

  it("远端 claudecode + bypassPermissions 显示 IS_SANDBOX 提示;点按钮把 IS_SANDBOX=1 一键写进 env_json", async () => {
    const user = userEvent.setup();
    const mocks = installAppMock({
      RemoteDeviceList: vi.fn(() =>
        Promise.resolve([{ id: 7, name: "linux-srv", online: true }]),
      ),
    });
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));

    const dialog = await screen.findByRole("dialog");
    fireEvent.change(
      within(dialog).getByPlaceholderText("Example: Local · Claude Code"),
      { target: { value: "远端 claude" } },
    );
    await user.click(
      within(dialog).getByRole("radio", { name: /Claude Code CLI/ }),
    );

    // 选远端 device
    await user.click(
      within(dialog).getByRole("combobox", { name: "Runtime Device" }),
    );
    await user.click(screen.getByRole("option", { name: /linux-srv/ }));

    // 选 bypassPermissions
    await user.click(
      within(dialog).getByRole("combobox", { name: "Default Permission Mode" }),
    );
    await user.click(screen.getByRole("option", { name: /bypassPermissions/ }));

    // 提示出现 + 按钮可点
    expect(
      within(dialog).getByText(/remote agentred runs as root\/sudo/),
    ).toBeInTheDocument();
    const addBtn = within(dialog).getByRole("button", {
      name: /Add IS_SANDBOX=1/,
    });

    await user.click(addBtn);

    // 按钮变成「Configured in env_json」灰态
    expect(
      within(dialog).getByText(/Configured in env_json/),
    ).toBeInTheDocument();

    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(mocks.CreateAgentBackend).toHaveBeenCalledWith(
        expect.objectContaining({
          type: "claudecode",
          deviceId: "7",
          defaultPermissionMode: "bypassPermissions",
          envJson: expect.stringContaining(`"IS_SANDBOX":"1"`),
        }),
      );
    });
  });

  it("本地 claudecode + bypassPermissions 不显示 IS_SANDBOX 提示(只有远端才需要)", async () => {
    const user = userEvent.setup();
    installAppMock();
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));

    const dialog = await screen.findByRole("dialog");
    await user.click(
      within(dialog).getByRole("radio", { name: /Claude Code CLI/ }),
    );

    // 不改 device → 保持本地
    await user.click(
      within(dialog).getByRole("combobox", { name: "Default Permission Mode" }),
    );
    await user.click(screen.getByRole("option", { name: /bypassPermissions/ }));

    // 危险提示仍在(沙箱/CI 那句),但 root/sudo 提示不应出现
    expect(
      within(dialog).queryByText(/remote agentred runs as root\/sudo/),
    ).not.toBeInTheDocument();
    expect(
      within(dialog).queryByRole("button", { name: /Add IS_SANDBOX=1/ }),
    ).not.toBeInTheDocument();
  });

  it("creates an OpenClaw backend through dedicated Wails bindings and loads probe discovery", async () => {
    const user = userEvent.setup();
    const credential = "t".repeat(48);
    const mocks = installAppMock({
      TestOpenClawAgentBackend: vi.fn(() =>
        Promise.resolve({
          ok: true,
          code: "",
          message: "",
          latencyMs: 9,
          gatewayVersion: "2026.7.1-2",
          protocol: 4,
          grantedScopes: [
            "operator.read",
            "operator.write",
            "operator.approvals",
          ],
          methods: ["agent", "agent.wait"],
          events: ["agent", "exec.approval.requested"],
          openClawAgents: [
            {
              id: "main",
              name: "Main",
              primaryModel: "anthropic/claude-sonnet-4-6",
              fallbacks: [],
              default: true,
            },
          ],
          openClawModels: [
            {
              id: "anthropic/claude-sonnet-4-6",
              name: "Claude Sonnet 4.6",
              provider: "anthropic",
              available: true,
            },
          ],
        }),
      ),
    });
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(
      within(dialog).getByRole("radio", { name: "OpenClaw Gateway" }),
    );

    await user.clear(within(dialog).getByLabelText("Name"));
    await user.type(within(dialog).getByLabelText("Name"), "Local OpenClaw");
    await user.type(within(dialog).getByLabelText("Gateway token"), credential);
    await user.click(
      within(dialog).getByRole("button", { name: "Test Connection" }),
    );

    await waitFor(() => {
      expect(mocks.TestOpenClawAgentBackend).toHaveBeenCalledWith(
        expect.objectContaining({
          type: "openclaw",
          openClawGatewayUrl: "ws://127.0.0.1:18789",
          openClawSessionMode: "per-agentre-session",
        }),
        credential,
      );
    });
    expect(within(dialog).getByText("2026.7.1-2")).toBeInTheDocument();
    expect(within(dialog).getByText("Protocol 4")).toBeInTheDocument();
    expect(within(dialog).getByText("operator.approvals")).toBeInTheDocument();
    expect(within(dialog).getAllByText(/Main/).length).toBeGreaterThan(0);
    expect(
      within(dialog).getAllByText(/Claude Sonnet 4.6/).length,
    ).toBeGreaterThan(0);

    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(mocks.CreateOpenClawAgentBackend).toHaveBeenCalledWith(
        expect.objectContaining({
          type: "openclaw",
          name: "Local OpenClaw",
          openClawGatewayUrl: "ws://127.0.0.1:18789",
          openClawAgentId: "main",
          openClawDefaultModel: "anthropic/claude-sonnet-4-6",
          openClawSessionMode: "per-agentre-session",
        }),
        credential,
      );
    });
    expect(mocks.CreateAgentBackend).not.toHaveBeenCalled();
  });

  it("does not echo a saved OpenClaw token and supports explicit clearing", async () => {
    const user = userEvent.setup();
    const mocks = installAppMock({
      ListAgentBackends: vi.fn(() =>
        Promise.resolve({
          items: [
            {
              id: 8,
              type: "openclaw",
              name: "OpenClaw Local",
              openClawGatewayUrl: "ws://127.0.0.1:18789",
              openClawAgentId: "main",
              openClawDefaultModel: "anthropic/claude-sonnet-4-6",
              openClawSessionMode: "per-agentre-session",
              hasToken: true,
              deviceId: "",
              agentCount: 1,
              createtime: 0,
              updatetime: 0,
            },
          ],
        }),
      ),
    });
    render(<AgentBackendsPanel />);

    const row = (await screen.findByText("OpenClaw Local")).closest(
      '[role="listitem"]',
    ) as HTMLElement;
    await user.click(within(row).getByRole("button", { name: /Edit/ }));
    const dialog = await screen.findByRole("dialog");
    const token = within(dialog).getByLabelText("Gateway token");
    expect(token).toHaveValue("");
    expect(token).toHaveAttribute(
      "placeholder",
      "Token is stored securely. Enter a new value to replace it.",
    );

    await user.click(
      within(dialog).getByRole("switch", { name: "Clear stored token" }),
    );
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(mocks.UpdateOpenClawAgentBackend).toHaveBeenCalledWith(
        expect.objectContaining({ id: 8, name: "OpenClaw Local" }),
        "",
        true,
      );
    });
    expect(mocks.UpdateAgentBackend).not.toHaveBeenCalled();
  });

  it("maps structured OpenClaw scope errors and explains the remote boundary", async () => {
    const user = userEvent.setup();
    installAppMock({
      TestOpenClawAgentBackend: vi.fn(() =>
        Promise.resolve({
          ok: false,
          code: "OPENCLAW_SCOPE_MISSING",
          message: "missing scope",
          latencyMs: 1,
        }),
      ),
    });
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(
      within(dialog).getByRole("radio", { name: "OpenClaw Gateway" }),
    );
    expect(
      within(dialog).getByText(
        "Remote agentred support is unavailable until secure secret enrollment is implemented.",
      ),
    ).toBeInTheDocument();

    await user.click(
      within(dialog).getByRole("button", { name: "Test Connection" }),
    );
    expect(
      await within(dialog).findByText(
        "The Gateway did not grant all required operator scopes.",
      ),
    ).toBeInTheDocument();
  });

  it("dialog 测试连接 shows error message inside the dialog", async () => {
    installAppMock({
      TestAgentBackend: vi.fn(() =>
        Promise.resolve({
          ok: false,
          latencyMs: 12,
          message: "401 Unauthorized",
        }),
      ),
    });
    render(<AgentBackendsPanel />);
    await screen.findByText("默认助手");

    fireEvent.click(screen.getByRole("button", { name: /New Backend/ }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByPlaceholderText(/Claude Code/), {
      target: { value: "draft-name" },
    });
    fireEvent.click(
      within(dialog).getByRole("button", { name: /Test Connection/ }),
    );

    await waitFor(() =>
      expect(within(dialog).getByText(/401 Unauthorized/)).toBeInTheDocument(),
    );
  });
});

// 与 LLM 供应商页同一条规则：页级操作属于 H1 标题行，卡片里不再重复一层
// 「已配置的后端 / 共 N 个」页头。本页没有 mockup，对齐的是规则不是图。
describe("AgentBackendsPanel page header", () => {
  it("Given backends exist, When the panel loads, Then both page actions are handed to the page header slot and the card's own header and count line are gone", async () => {
    installAppMock();
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });

    const header = screen.getByTestId("page-header");
    expect(
      within(header).getByRole("button", { name: /New Backend/ }),
    ).toBeInTheDocument();
    expect(
      within(header).getByRole("button", { name: /Auto Scan/ }),
    ).toBeInTheDocument();
    // 整页各只剩一个入口，卡片里那层 strip 不再重复渲染它们。
    expect(screen.getAllByRole("button", { name: /New Backend/ })).toHaveLength(
      1,
    );
    expect(screen.getAllByRole("button", { name: /Auto Scan/ })).toHaveLength(
      1,
    );
    expect(screen.queryByText("Configured Backends")).toBeNull();
    expect(screen.queryByText("1 total")).toBeNull();
  });

  it("Given two actions share the title row, When they render, Then New Backend stays visually primary and Auto Scan stays secondary", async () => {
    installAppMock();
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });

    const header = screen.getByTestId("page-header");
    expect(
      within(header).getByRole("button", { name: /New Backend/ }),
    ).toHaveClass("bg-primary");
    expect(
      within(header).getByRole("button", { name: /Auto Scan/ }),
    ).not.toHaveClass("bg-primary");
  });

  it("Given the page header slot renders New Backend, When it is clicked, Then the create dialog opens", async () => {
    const user = userEvent.setup();
    installAppMock();
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(
      within(screen.getByTestId("page-header")).getByRole("button", {
        name: /New Backend/,
      }),
    );

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
  });

  it("Given the page header slot renders Auto Scan, When it is clicked, Then the scan still runs from the title row", async () => {
    const user = userEvent.setup();
    const mocks = installAppMock({
      ScanAndCreateAgentBackends: vi.fn(() =>
        Promise.resolve({
          results: [
            { name: "Claude Code", found: true, created: true, skipped: false },
          ],
        }),
      ),
    });
    render(<AgentBackendsPanel />);

    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(
      within(screen.getByTestId("page-header")).getByRole("button", {
        name: /Auto Scan/,
      }),
    );

    await waitFor(() =>
      expect(mocks.ScanAndCreateAgentBackends).toHaveBeenCalledTimes(1),
    );
    expect(
      await screen.findByText("Created 1 backend(s): Claude Code"),
    ).toBeInTheDocument();
  });

  it("Given no backends exist, When the panel loads, Then Auto Scan stays on the title row and the empty state keeps the only add entry", async () => {
    installAppMock({
      ListAgentBackends: vi.fn(() => Promise.resolve({ items: [] })),
    });
    render(<AgentBackendsPanel />);

    await screen.findByRole("button", { name: "Add First Backend" });

    const header = screen.getByTestId("page-header");
    // 自动识别在空态下最有用，留在标题行；新建入口交给空态 CTA，全页仍只有一个。
    expect(
      within(header).getByRole("button", { name: /Auto Scan/ }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /New Backend/ })).toBeNull();
  });
});

describe("Agent backend type picker", () => {
  async function openCreateDialog(user: ReturnType<typeof userEvent.setup>) {
    render(<AgentBackendsPanel />);
    await screen.findByRole("list", { name: "Agent backend list" });
    await user.click(screen.getByRole("button", { name: /New Backend/ }));
    return screen.findByRole("dialog");
  }

  it("Given the create dialog, When it opens, Then the five types render as a single-choice radiogroup with the current type checked", async () => {
    const user = userEvent.setup();
    installAppMock();

    const dialog = await openCreateDialog(user);
    const group = within(dialog).getByRole("radiogroup", { name: "Type" });

    expect(within(group).getAllByRole("radio")).toHaveLength(5);
    expect(
      within(group).getByRole("radio", { name: /Built-in Agent/ }),
    ).toBeChecked();
    expect(
      within(group).getByRole("radio", { name: /Claude Code CLI/ }),
    ).not.toBeChecked();

    await user.click(
      within(group).getByRole("radio", { name: /Claude Code CLI/ }),
    );
    expect(
      within(group).getByRole("radio", { name: /Claude Code CLI/ }),
    ).toBeChecked();
    expect(
      within(group).getByRole("radio", { name: /Built-in Agent/ }),
    ).not.toBeChecked();
  });

  it("Given local CLIs on $PATH, When the create dialog opens, Then every CLI type is probed and shows an installed / not-installed badge", async () => {
    const user = userEvent.setup();
    const resolveFn = vi.fn((req: unknown) => {
      const { type } = req as { type: string };
      return Promise.resolve(
        type === "piagent"
          ? { path: "", found: false }
          : { path: `/usr/local/bin/${type}`, found: true },
      );
    });
    installAppMock({ ResolveAgentBackendCLIPath: resolveFn });

    const dialog = await openCreateDialog(user);
    const group = within(dialog).getByRole("radiogroup", { name: "Type" });

    await waitFor(() => {
      expect(
        within(
          within(group).getByRole("radio", { name: /Claude Code CLI/ }),
        ).getByText("Installed"),
      ).toBeInTheDocument();
      expect(
        within(
          within(group).getByRole("radio", { name: /Pi Agent CLI/ }),
        ).getByText("Not installed"),
      ).toBeInTheDocument();
    });

    for (const type of ["claudecode", "codex", "piagent"]) {
      expect(resolveFn).toHaveBeenCalledWith(
        expect.objectContaining({ type, deviceId: "" }),
      );
    }
  });

  it("Given probes still in flight, When the create dialog has just opened, Then CLI types show a detecting badge and stay selectable", async () => {
    const user = userEvent.setup();
    let release: (v: { path: string; found: boolean }) => void = () => {};
    installAppMock({
      ResolveAgentBackendCLIPath: vi.fn(
        () =>
          new Promise<{ path: string; found: boolean }>((resolve) => {
            release = resolve;
          }),
      ),
    });

    const dialog = await openCreateDialog(user);
    const group = within(dialog).getByRole("radiogroup", { name: "Type" });
    const codex = within(group).getByRole("radio", { name: /Codex CLI/ });

    await waitFor(() =>
      expect(within(codex).getByText(/Detecting/)).toBeInTheDocument(),
    );
    expect(codex).toBeEnabled();

    await user.click(codex);
    expect(
      within(group).getByRole("radio", { name: /Codex CLI/ }),
    ).toBeChecked();

    release({ path: "/usr/local/bin/codex", found: true });
  });

  it("Given a probe that already answered, When that CLI type is selected, Then the path is reused without another round-trip", async () => {
    const user = userEvent.setup();
    const resolveFn = vi.fn((req: unknown) => {
      const { type } = req as { type: string };
      return Promise.resolve({ path: `/usr/local/bin/${type}`, found: true });
    });
    installAppMock({ ResolveAgentBackendCLIPath: resolveFn });

    const dialog = await openCreateDialog(user);
    const group = within(dialog).getByRole("radiogroup", { name: "Type" });
    await waitFor(() =>
      expect(
        within(
          within(group).getByRole("radio", { name: /Claude Code CLI/ }),
        ).getByText("Installed"),
      ).toBeInTheDocument(),
    );

    // 探测已经给出结论了，再点这个类型不该重新拨一次 —— 远端设备上这是一次真实的网络往返，
    // 而方向键会逐个 onChange，代价按键盘步数累加。
    const callsBeforeSelect = resolveFn.mock.calls.length;
    await user.click(
      within(group).getByRole("radio", { name: /Claude Code CLI/ }),
    );

    await waitFor(() => {
      const input = within(dialog).getByPlaceholderText(
        "/usr/local/bin/claude",
      ) as HTMLInputElement;
      expect(input.value).toBe("/usr/local/bin/claudecode");
    });
    expect(resolveFn.mock.calls.length).toBe(callsBeforeSelect);
  });

  it("Given non-CLI types, When the picker renders, Then Built-in carries a local-only badge and OpenClaw carries no badge", async () => {
    const user = userEvent.setup();
    installAppMock();

    const dialog = await openCreateDialog(user);
    const group = within(dialog).getByRole("radiogroup", { name: "Type" });

    expect(
      within(
        within(group).getByRole("radio", { name: /Built-in Agent/ }),
      ).getByText("Local only"),
    ).toBeInTheDocument();
    expect(
      within(group).getByRole("radio", { name: /OpenClaw Gateway/ }),
    ).toHaveAccessibleName("OpenClaw Gateway");
  });

  it("Given a remote device is selected, When the CLI probe re-runs, Then it targets that device and refreshes the badges", async () => {
    const user = userEvent.setup();
    const resolveFn = vi.fn((req: unknown) => {
      const { type, deviceId } = req as { type: string; deviceId: string };
      // 本机没装 codex，远端 linux-srv 装了 —— 徽标必须跟着设备变。
      return Promise.resolve(
        deviceId === "7" && type === "codex"
          ? { path: "/opt/codex", found: true }
          : { path: "", found: false },
      );
    });
    installAppMock({
      ResolveAgentBackendCLIPath: resolveFn,
      RemoteDeviceList: vi.fn(() =>
        Promise.resolve([{ id: 7, name: "linux-srv", online: true }]),
      ),
    });

    const dialog = await openCreateDialog(user);
    const group = within(dialog).getByRole("radiogroup", { name: "Type" });

    await waitFor(() =>
      expect(
        within(
          within(group).getByRole("radio", { name: /Codex CLI/ }),
        ).getByText("Not installed"),
      ).toBeInTheDocument(),
    );

    await user.click(
      within(group).getByRole("radio", { name: /Claude Code CLI/ }),
    );
    await user.click(
      within(dialog).getByRole("combobox", { name: "Runtime Device" }),
    );
    await user.click(screen.getByRole("option", { name: /linux-srv/ }));

    await waitFor(() => {
      expect(
        within(
          within(group).getByRole("radio", { name: /Codex CLI/ }),
        ).getByText("Installed"),
      ).toBeInTheDocument();
    });
    expect(resolveFn).toHaveBeenCalledWith(
      expect.objectContaining({ type: "codex", deviceId: "7" }),
    );
  });

  it("Given an unreachable remote device, When the probe rejects, Then the badge says the probe failed instead of claiming the CLI is missing", async () => {
    const user = userEvent.setup();
    installAppMock({
      ResolveAgentBackendCLIPath: vi.fn((req: unknown) => {
        const { deviceId } = req as { deviceId: string };
        return deviceId === "7"
          ? Promise.reject(new Error("device offline"))
          : Promise.resolve({ path: "", found: false });
      }),
      RemoteDeviceList: vi.fn(() =>
        Promise.resolve([{ id: 7, name: "linux-srv", online: true }]),
      ),
    });

    const dialog = await openCreateDialog(user);
    const group = within(dialog).getByRole("radiogroup", { name: "Type" });

    await user.click(
      within(group).getByRole("radio", { name: /Claude Code CLI/ }),
    );
    await user.click(
      within(dialog).getByRole("combobox", { name: "Runtime Device" }),
    );
    await user.click(screen.getByRole("option", { name: /linux-srv/ }));

    await waitFor(() => {
      expect(
        within(
          within(group).getByRole("radio", { name: /Codex CLI/ }),
        ).getByText("Probe failed"),
      ).toBeInTheDocument();
    });
    expect(
      within(
        within(group).getByRole("radio", { name: /Codex CLI/ }),
      ).queryByText("Not installed"),
    ).not.toBeInTheDocument();
  });

  it("Given the type field, When the create dialog renders, Then it precedes the name field", async () => {
    const user = userEvent.setup();
    installAppMock();

    const dialog = await openCreateDialog(user);
    const group = within(dialog).getByRole("radiogroup", { name: "Type" });
    const nameInput = within(dialog).getByPlaceholderText(
      "Example: Local · Claude Code",
    );

    expect(
      group.compareDocumentPosition(nameInput) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("Given an untouched name, When the type changes, Then the name is prefilled from the type and keeps following further changes", async () => {
    const user = userEvent.setup();
    installAppMock();

    const dialog = await openCreateDialog(user);
    const group = within(dialog).getByRole("radiogroup", { name: "Type" });
    const nameInput = within(dialog).getByPlaceholderText(
      "Example: Local · Claude Code",
    ) as HTMLInputElement;

    await user.click(
      within(group).getByRole("radio", { name: /Claude Code CLI/ }),
    );
    expect(nameInput.value).toBe("Local · Claude Code");

    await user.click(within(group).getByRole("radio", { name: /Codex CLI/ }));
    expect(nameInput.value).toBe("Local · Codex");
  });

  it("Given a name the user typed, When the type changes, Then the typed name is preserved", async () => {
    const user = userEvent.setup();
    installAppMock();

    const dialog = await openCreateDialog(user);
    const group = within(dialog).getByRole("radiogroup", { name: "Type" });
    const nameInput = within(dialog).getByPlaceholderText(
      "Example: Local · Claude Code",
    ) as HTMLInputElement;

    fireEvent.change(nameInput, { target: { value: "my backend" } });
    await user.click(
      within(group).getByRole("radio", { name: /Claude Code CLI/ }),
    );

    expect(nameInput.value).toBe("my backend");
  });

  it("Given an existing backend, When the edit dialog opens, Then the type is a read-only summary with a locked hint and no radiogroup", async () => {
    const user = userEvent.setup();
    installAppMock({
      ListAgentBackends: vi.fn(() =>
        Promise.resolve({
          items: [
            {
              id: 1,
              type: "claudecode",
              name: "本机 claude",
              llmProviderKey: "",
              cliPath: "/usr/local/bin/claude",
              agentCount: 0,
              createtime: 0,
              updatetime: 0,
            },
          ],
        }),
      ),
    });
    render(<AgentBackendsPanel />);

    await screen.findByText("本机 claude");
    const row = screen
      .getByText("本机 claude")
      .closest('[role="listitem"]') as HTMLElement;
    await user.click(within(row).getByRole("button", { name: /Edit/ }));

    const dialog = await screen.findByRole("dialog");
    expect(
      within(dialog).queryByRole("radiogroup", { name: "Type" }),
    ).not.toBeInTheDocument();
    expect(within(dialog).getByText("Claude Code CLI")).toBeInTheDocument();
    expect(
      within(dialog).getByText("Cannot be changed after creation"),
    ).toBeInTheDocument();
  });

  it("Given keyboard focus inside the group, When arrow keys are pressed, Then the checked type moves without a mouse", async () => {
    const user = userEvent.setup();
    installAppMock();

    const dialog = await openCreateDialog(user);
    const group = within(dialog).getByRole("radiogroup", { name: "Type" });

    within(group)
      .getByRole("radio", { name: /Built-in Agent/ })
      .focus();
    await user.keyboard("{ArrowDown}");

    expect(
      within(group).getByRole("radio", { name: /OpenClaw Gateway/ }),
    ).toBeChecked();
    expect(
      within(group).getByRole("radio", { name: /OpenClaw Gateway/ }),
    ).toHaveFocus();

    await user.keyboard("{ArrowUp}{ArrowUp}");
    expect(
      within(group).getByRole("radio", { name: /Pi Agent CLI/ }),
    ).toBeChecked();
  });
});

describe("truncateFlashText", () => {
  it("短文本原样返回，truncated=false", () => {
    const r = truncateFlashText("✅ 128ms · pong");
    expect(r.display).toBe("✅ 128ms · pong");
    expect(r.truncated).toBe(false);
    expect(r.full).toBe("✅ 128ms · pong");
  });

  it("超过 80 字时截断 + …，truncated=true，full 保留原文", () => {
    const long = "a".repeat(300);
    const r = truncateFlashText(long);
    expect(r.truncated).toBe(true);
    expect(r.display.endsWith("…")).toBe(true);
    expect(r.display.length).toBeLessThanOrEqual(81); // 80 + …
    expect(r.full).toBe(long);
  });

  it("换行/制表符压成单空格防止 flash 行高被撑起", () => {
    const r = truncateFlashText("line1\nline2\t\tline3");
    expect(r.display).toBe("line1 line2 line3");
  });
});
