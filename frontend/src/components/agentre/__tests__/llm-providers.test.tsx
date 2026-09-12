import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

const appMocks = vi.hoisted(() => ({
  ListLLMProviders: vi.fn(),
  CreateLLMProvider: vi.fn(),
  UpdateLLMProvider: vi.fn(),
  DeleteLLMProvider: vi.fn(),
  ListLLMModels: vi.fn(),
  PreviewLLMModels: vi.fn(),
  ImportLLMModels: vi.fn(),
  TestLLMProvider: vi.fn(),
  LookupLLMModel: vi.fn(),
  UpdateLLMModel: vi.fn(),
  DeleteLLMModel: vi.fn(),
  SetLLMModelDefault: vi.fn(),
  SetLLMModelEnabled: vi.fn(),
  SetLLMProviderEnabled: vi.fn(),
  LLMModelRefCounts: vi.fn(),
  LLMProviderRefCounts: vi.fn(),
}));

vi.mock("../../../../wailsjs/go/app/App", () => appMocks);

vi.mock("../../../../wailsjs/go/models", () => {
  class ModelClass {
    static createFrom(source: Record<string, unknown> = {}) {
      return new ModelClass(source);
    }
    constructor(init?: Record<string, unknown>) {
      if (init) Object.assign(this, init);
    }
  }
  const svc = {
    ModelInput: ModelClass,
    CreateProviderRequest: ModelClass,
    UpdateProviderRequest: ModelClass,
    DeleteProviderRequest: ModelClass,
    DeleteModelRequest: ModelClass,
    TestConnectionRequest: ModelClass,
    ListModelsRequest: ModelClass,
    PreviewModelsRequest: ModelClass,
    LookupModelRequest: ModelClass,
    ImportModelsRequest: ModelClass,
    SetModelDefaultRequest: ModelClass,
    SetModelEnabledRequest: ModelClass,
    SetProviderEnabledRequest: ModelClass,
    UpdateModelRequest: ModelClass,
    ModelRefCountsRequest: ModelClass,
    ProviderRefCountsRequest: ModelClass,
  };
  return { llm_provider_svc: svc };
});

import { LlmProvidersPanel } from "../llm-providers";

type AnyFn = (...args: unknown[]) => unknown;

type AppMockShape = {
  CreateLLMProvider: AnyFn;
  DeleteLLMProvider: AnyFn;
  DeleteLLMModel: AnyFn;
  ImportLLMModels: AnyFn;
  ListLLMModels: AnyFn;
  ListLLMProviders: AnyFn;
  LLMModelRefCounts: AnyFn;
  LLMProviderRefCounts: AnyFn;
  LookupLLMModel: AnyFn;
  PreviewLLMModels: AnyFn;
  SetLLMModelDefault: AnyFn;
  SetLLMModelEnabled: AnyFn;
  SetLLMProviderEnabled: AnyFn;
  TestLLMProvider: AnyFn;
  UpdateLLMModel: AnyFn;
  UpdateLLMProvider: AnyFn;
};

type ProviderItem = {
  baseUrl: string;
  defaultModelKey: string;
  enabled: boolean;
  hasApiKey: boolean;
  id: number;
  maskedApiKey: string;
  modelCount: number;
  name: string;
  providerKey: string;
  type: string;
};

type ModelItem = {
  contextWindow: number;
  enabled: boolean;
  id: number;
  isDefault: boolean;
  maxOutput: number;
  modelId: string;
  modelKey: string;
  name: string;
  providerId: number;
  providerKey: string;
};

function makeProvider(overrides: Partial<ProviderItem> = {}): ProviderItem {
  return {
    id: 1,
    type: "anthropic",
    providerKey: "pk-1",
    name: "Anthropic",
    baseUrl: "https://api.anthropic.com",
    maskedApiKey: "sk-••••••9XQ2",
    hasApiKey: true,
    enabled: true,
    defaultModelKey: "mk-default",
    modelCount: 3,
    ...overrides,
  };
}

function makeModel(overrides: Partial<ModelItem> = {}): ModelItem {
  return {
    id: 11,
    providerId: 1,
    providerKey: "pk-1",
    modelKey: "mk-default",
    modelId: "claude-sonnet-4-5",
    name: "Sonnet",
    contextWindow: 200000,
    maxOutput: 64000,
    enabled: true,
    isDefault: true,
    ...overrides,
  };
}

function installAppMock(overrides: Partial<AppMockShape> = {}) {
  const base: AppMockShape = {
    ListLLMProviders: vi.fn(() => Promise.resolve({ items: [] })),
    CreateLLMProvider: vi.fn(() =>
      Promise.resolve({ item: makeProvider({ id: 99 }) }),
    ),
    UpdateLLMProvider: vi.fn(() => Promise.resolve({ item: makeProvider() })),
    DeleteLLMProvider: vi.fn(() => Promise.resolve({})),
    ListLLMModels: vi.fn(() => Promise.resolve({ items: [] })),
    PreviewLLMModels: vi.fn(() => Promise.resolve({ items: [] })),
    ImportLLMModels: vi.fn(() =>
      Promise.resolve({ items: [], imported: 0, updated: 0 }),
    ),
    TestLLMProvider: vi.fn(() =>
      Promise.resolve({ ok: true, message: "", modelCount: 0 }),
    ),
    LookupLLMModel: vi.fn(() =>
      Promise.resolve({
        known: false,
        vendor: "",
        contextWindow: 0,
        maxOutput: 0,
      }),
    ),
    UpdateLLMModel: vi.fn(() => Promise.resolve({ item: makeModel() })),
    DeleteLLMModel: vi.fn(() => Promise.resolve({})),
    SetLLMModelDefault: vi.fn(() => Promise.resolve({ item: makeProvider() })),
    SetLLMModelEnabled: vi.fn(() => Promise.resolve({ item: makeModel() })),
    SetLLMProviderEnabled: vi.fn(() =>
      Promise.resolve({ item: makeProvider() }),
    ),
    LLMModelRefCounts: vi.fn(() =>
      Promise.resolve({ counts: { backends: 0, sessions: 0, routes: 0 } }),
    ),
    LLMProviderRefCounts: vi.fn(() =>
      Promise.resolve({ counts: { backends: 0, sessions: 0, routes: 0 } }),
    ),
  };
  const merged = { ...base, ...overrides };
  for (const key of Object.keys(appMocks) as Array<keyof typeof appMocks>) {
    const mock = appMocks[key] as ReturnType<typeof vi.fn>;
    const fn = merged[key as keyof AppMockShape] as AnyFn;
    mock.mockReset();
    mock.mockImplementation((...args: unknown[]) => fn(...args));
  }
  return merged;
}

const originalClipboard = navigator.clipboard;

function mockClipboard() {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText },
  });
  return writeText;
}

afterEach(() => {
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: originalClipboard,
  });
  vi.clearAllMocks();
});

// Radix DropdownMenu 在 jsdom 中需要关闭 pointerEvents 检查。
function setupMenuUser() {
  return userEvent.setup({ pointerEventsCheck: 0 });
}

function rowForModel(modelId: string): HTMLTableRowElement {
  const checkbox = screen.getByRole("checkbox", { name: `Select ${modelId}` });
  return checkbox.closest("tr") as HTMLTableRowElement;
}

// 工作区 region 只证明供应商列表这一段异步结束：模型区来自随后的 ListLLMModels，
// 此刻通常还停在「Loading models…」占位上。屏障停在 region 上、后面接同步查询，
// 就是在赌 React 的调度顺序——CI 上已经随机红过。所以屏障一律落在模型区自己的
// 终态上：有模型等表格，没模型等空态，两者都是正值断言，不会被中间态骗过去。
async function waitForModelTable(
  providerRegion: string | RegExp = / models$/,
): Promise<HTMLElement> {
  const workspace = await screen.findByRole("region", { name: providerRegion });
  await within(workspace).findByRole("table", { name: "Model list" });
  return workspace;
}

async function waitForEmptyModelSection(
  providerRegion: string | RegExp = / models$/,
): Promise<HTMLElement> {
  const workspace = await screen.findByRole("region", { name: providerRegion });
  await within(workspace).findByText("No models configured yet");
  return workspace;
}

// 默认模型（mk-default）+ 被引用模型（mk-opus）+ 可删除模型（mk-haiku）。
function installThreeModels(
  refs: Record<
    string,
    { backends: number; sessions: number; routes: number }
  > = {},
) {
  return installAppMock({
    ListLLMProviders: vi.fn(() => Promise.resolve({ items: [makeProvider()] })),
    ListLLMModels: vi.fn(() =>
      Promise.resolve({
        items: [
          makeModel(),
          makeModel({
            id: 12,
            modelKey: "mk-opus",
            modelId: "claude-opus-4-1",
            name: "",
            isDefault: false,
          }),
          makeModel({
            id: 13,
            modelKey: "mk-haiku",
            modelId: "claude-haiku",
            name: "",
            isDefault: false,
          }),
        ],
      }),
    ),
    LLMModelRefCounts: vi.fn((req: unknown) =>
      Promise.resolve({
        counts: refs[(req as { modelKey?: string }).modelKey ?? ""] ?? {
          backends: 0,
          sessions: 0,
          routes: 0,
        },
      }),
    ),
  });
}

describe("LlmProvidersPanel", () => {
  it("Given providers of different types, When the panel loads, Then the nav groups them by type, shows the endpoint, and marks only disabled providers", async () => {
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({
          items: [
            makeProvider({
              id: 1,
              type: "anthropic",
              name: "Anthropic Official",
              providerKey: "pk-anthropic",
              baseUrl: "api.anthropic.com",
              maskedApiKey: "sk-••••••9XQ2",
              hasApiKey: true,
              enabled: true,
              modelCount: 3,
            }),
            makeProvider({
              id: 2,
              type: "openai-chat",
              name: "DeepSeek Proxy",
              providerKey: "pk-deepseek",
              baseUrl: "llm.intra.example",
              maskedApiKey: "",
              hasApiKey: false,
              enabled: false,
              defaultModelKey: "",
              modelCount: 2,
            }),
          ],
        }),
      ),
    });
    render(<LlmProvidersPanel />);

    const nav = await screen.findByRole("complementary", {
      name: "Provider list",
    });
    const anthropic = await within(nav).findByRole("button", {
      name: /Anthropic Official/,
    });
    expect(
      within(anthropic).getByText(/api\.anthropic\.com/),
    ).toBeInTheDocument();
    expect(within(anthropic).getByText(/3 models/)).toBeInTheDocument();
    // 启用是常态，不再标注；只有停用的供应商带停用标记
    expect(within(anthropic).queryByText("Enabled")).not.toBeInTheDocument();

    const deepseek = within(nav).getByRole("button", {
      name: /DeepSeek Proxy/,
    });
    expect(
      within(deepseek).getByText(/llm\.intra\.example/),
    ).toBeInTheDocument();
    // 停用的供应商让位给「已停用」徽标：模型数不再和它抢空间
    expect(within(deepseek).queryByText(/2 models/)).not.toBeInTheDocument();
    expect(within(deepseek).getByText("Disabled")).toBeInTheDocument();

    // 每个类型有独立分组标题
    expect(mocks.ListLLMModels).toHaveBeenCalledWith(
      expect.objectContaining({ id: 1 }),
    );
  });

  it("Given a provider is selected, When its workspace loads, Then the model rows render and the header shows connection status", async () => {
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [
            makeModel(),
            makeModel({
              id: 12,
              modelKey: "mk-opus",
              modelId: "claude-opus-4-1",
              isDefault: false,
            }),
          ],
        }),
      ),
    });
    render(<LlmProvidersPanel />);

    const workspace = await waitForModelTable(/Anthropic models/);
    // 模型行由异步 ListLLMModels 渲染，等待真实模型控件出现而非 region。
    expect(
      (await within(workspace).findAllByText("claude-sonnet-4-5")).length,
    ).toBeGreaterThan(0);
    expect(within(workspace).getByText("claude-opus-4-1")).toBeInTheDocument();
    // 连接配置（endpoint + 掩码 key）在头部可见
    expect(
      within(workspace).getByText("https://api.anthropic.com"),
    ).toBeInTheDocument();
    expect(within(workspace).getByText("sk-••••••9XQ2")).toBeInTheDocument();
    // 默认模型徽标
    expect(within(workspace).getAllByText("Default").length).toBeGreaterThan(0);
    expect(mocks.ListLLMModels).toHaveBeenCalledWith(
      expect.objectContaining({ id: 1 }),
    );
  });

  it("Given models with and without display names, When the table renders, Then the main row shows the display name (falling back to model ID) and the sub row shows only the model ID", async () => {
    installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [
            makeModel(),
            makeModel({
              id: 12,
              modelKey: "mk-opus",
              modelId: "claude-opus-4-1",
              name: "",
              isDefault: false,
            }),
          ],
        }),
      ),
    });
    render(<LlmProvidersPanel />);

    const workspace = await waitForModelTable(/Anthropic models/);
    // 主行显示 display name（Sonnet），副行显示 modelId
    expect(await within(workspace).findByText("Sonnet")).toBeInTheDocument();
    expect(
      within(workspace).getAllByText("claude-sonnet-4-5").length,
    ).toBeGreaterThan(0);
    // 空 name 回落显示 modelId
    expect(
      within(workspace).getAllByText("claude-opus-4-1").length,
    ).toBeGreaterThan(0);
    // UUID modelKey 不再出现在行内
    expect(within(workspace).queryByText("mk-default")).not.toBeInTheDocument();
    expect(within(workspace).queryByText("mk-opus")).not.toBeInTheDocument();
  });

  it("Given a provider with models, When the table renders, Then the columns are ordered checkbox, model, context, max output, references, default, enable and row actions", async () => {
    installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() => Promise.resolve({ items: [makeModel()] })),
    });
    render(<LlmProvidersPanel />);

    const table = await screen.findByRole("table", { name: "Model list" });
    const texts = within(table)
      .getAllByRole("columnheader")
      .map((h) => (h.textContent ?? "").trim());
    expect(texts[0]).toBe("");
    expect(texts[1]).toBe("Model");
    expect(texts[2]).toBe("Context");
    expect(texts[3]).toBe("Max Output");
    expect(texts[4]).toBe("References");
    expect(texts[5]).toBe("Default");
    expect(texts[6]).toBe("Enable");
    expect(texts[7]).toBe("");
  });

  it("Given reference counts are still loading, When a model is selected, Then batch delete is available at once — counts inform, they no longer gate", async () => {
    const user = userEvent.setup();
    let resolveReferences: ((value: unknown) => void) | undefined;
    installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [
            makeModel({
              id: 13,
              modelKey: "mk-haiku",
              modelId: "claude-haiku",
              isDefault: false,
            }),
          ],
        }),
      ),
      LLMModelRefCounts: vi.fn(
        () =>
          new Promise((resolve) => {
            resolveReferences = resolve;
          }),
      ),
    });
    render(<LlmProvidersPanel />);

    const checkbox = await screen.findByRole("checkbox", {
      name: "Select claude-haiku",
    });
    await user.click(checkbox);

    expect(
      screen.getByRole("button", { name: "Delete selected" }),
    ).not.toBeDisabled();

    resolveReferences?.({
      counts: { backends: 0, sessions: 0, routes: 0 },
    });
    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: "Delete selected" }),
      ).not.toBeDisabled();
    });
  });

  it("Given a reference lookup fails, When a model is selected, Then batch delete is still offered — a missing count is not a reason to block", async () => {
    const user = userEvent.setup();
    installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [
            makeModel({
              id: 13,
              modelKey: "mk-haiku",
              modelId: "claude-haiku",
              isDefault: false,
            }),
          ],
        }),
      ),
      LLMModelRefCounts: vi.fn(() =>
        Promise.reject(new Error("reference lookup failed")),
      ),
    });
    render(<LlmProvidersPanel />);

    const checkbox = await screen.findByRole("checkbox", {
      name: "Select claude-haiku",
    });
    await user.click(checkbox);

    const deleteButton = screen.getByRole("button", {
      name: "Delete selected",
    });
    expect(deleteButton).not.toBeDisabled();
    expect(deleteButton).not.toHaveAttribute("title");
  });

  it("Given models with and without references, When the table renders, Then the reference column shows the count and a placeholder for unreferenced models", async () => {
    installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [
            makeModel(),
            makeModel({
              id: 12,
              modelKey: "mk-opus",
              modelId: "claude-opus-4-1",
              isDefault: false,
            }),
          ],
        }),
      ),
      LLMModelRefCounts: vi.fn((req: unknown) =>
        Promise.resolve({
          counts:
            (req as { modelKey?: string }).modelKey === "mk-opus"
              ? { backends: 1, sessions: 0, routes: 2 }
              : { backends: 0, sessions: 0, routes: 0 },
        }),
      ),
    });
    render(<LlmProvidersPanel />);

    const workspace = await waitForModelTable(/Anthropic models/);
    await within(workspace).findByRole("switch", {
      name: "Enable claude-opus-4-1",
    });

    const opusRow = screen
      .getByRole("switch", { name: "Enable claude-opus-4-1" })
      .closest("tr");
    const sonnetRow = screen
      .getByRole("switch", { name: "Enable claude-sonnet-4-5" })
      .closest("tr");
    expect(opusRow).toBeTruthy();
    expect(sonnetRow).toBeTruthy();

    // 被引用模型的引用列显示数量；无引用的模型显示占位符
    expect(
      await within(opusRow as HTMLTableRowElement).findByText("3"),
    ).toBeInTheDocument();
    expect(
      within(sonnetRow as HTMLTableRowElement).getByText("—"),
    ).toBeInTheDocument();
  });

  it("Given a provider with a default model, When the header Test is clicked, Then TestLLMProvider is called with an empty modelKey", async () => {
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() => Promise.resolve({ items: [makeModel()] })),
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    await user.click(screen.getByRole("button", { name: "Test Anthropic" }));

    await waitFor(() => {
      expect(mocks.TestLLMProvider).toHaveBeenCalledWith(
        expect.objectContaining({ id: 1, modelKey: "" }),
      );
    });
  });

  it("Given a provider test succeeds, When it completes, Then the transient flash reports success with the elapsed time", async () => {
    installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() => Promise.resolve({ items: [makeModel()] })),
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    await user.click(screen.getByRole("button", { name: "Test Anthropic" }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(/\d+ms/);
  });

  it("Given a non-default model row, When its row Test is clicked, Then TestLLMProvider is called with the concrete modelKey", async () => {
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [
            makeModel(),
            makeModel({
              id: 12,
              modelKey: "mk-opus",
              modelId: "claude-opus-4-1",
              isDefault: false,
            }),
          ],
        }),
      ),
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await user.click(
      await screen.findByRole("button", {
        name: "Test claude-opus-4-1",
      }),
    );

    await waitFor(() => {
      expect(mocks.TestLLMProvider).toHaveBeenCalledWith(
        expect.objectContaining({ id: 1, modelKey: "mk-opus" }),
      );
    });
  });

  it("Given a non-default enabled model, When its Set default radio is picked, Then the impact dialog opens and confirming calls SetLLMModelDefault", async () => {
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [
            makeModel(),
            makeModel({
              id: 12,
              modelKey: "mk-opus",
              modelId: "claude-opus-4-1",
              isDefault: false,
            }),
          ],
        }),
      ),
      LLMProviderRefCounts: vi.fn(() =>
        Promise.resolve({
          counts: { backends: 2, sessions: 0, routes: 1 },
        }),
      ),
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await user.click(
      await screen.findByRole("radio", {
        name: "Set claude-opus-4-1 as default",
      }),
    );

    // spec 2026-08-11「Provider management」：改默认模型前先展示动态影响并二次确认。
    await screen.findByRole("heading", {
      name: /Set default model to claude-opus-4-1/i,
    });
    await waitFor(() => {
      expect(mocks.LLMProviderRefCounts).toHaveBeenCalledWith(
        expect.objectContaining({ providerKey: "pk-1" }),
      );
    });

    await user.click(screen.getByRole("button", { name: "Set as default" }));

    await waitFor(() => {
      expect(mocks.SetLLMModelDefault).toHaveBeenCalledWith(
        expect.objectContaining({
          providerId: 1,
          modelKey: "mk-opus",
        }),
      );
    });
  });

  it("Given the default model, When delete or disable is attempted, Then the controls are blocked with a visible reason", async () => {
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() => Promise.resolve({ items: [makeModel()] })),
    });
    const user = setupMenuUser();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);

    // 默认模型：启用开关禁用并给出原因（需在打开菜单前查询，Radix 菜单打开时会 aria-hidden 其余内容）
    const enableSwitch = screen.getByRole("switch", {
      name: "Enable claude-sonnet-4-5",
    });
    expect(enableSwitch).toBeDisabled();
    expect(enableSwitch).toHaveAttribute(
      "title",
      expect.stringMatching(/default model/i),
    );

    await user.click(
      screen.getByRole("button", {
        name: "More actions for claude-sonnet-4-5",
      }),
    );
    const deleteItem = within(await screen.findByRole("menu")).getByRole(
      "menuitem",
      { name: "Delete claude-sonnet-4-5" },
    );
    expect(deleteItem).toHaveAttribute("aria-disabled", "true");
    // 被阻止的原因通过 title / tooltip 可见
    expect(deleteItem).toHaveAttribute(
      "title",
      expect.stringMatching(/default model/i),
    );

    expect(mocks.DeleteLLMModel).not.toHaveBeenCalled();
    expect(mocks.SetLLMModelEnabled).not.toHaveBeenCalled();
  });

  it("Given a referenced model, When its More menu is opened, Then the delete item is usable — the impact is disclosed in the dialog instead", async () => {
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [
            makeModel(),
            makeModel({
              id: 12,
              modelKey: "mk-opus",
              modelId: "claude-opus-4-1",
              isDefault: false,
            }),
          ],
        }),
      ),
      LLMModelRefCounts: vi.fn((req: unknown) =>
        Promise.resolve({
          counts:
            (req as { modelKey?: string }).modelKey === "mk-opus"
              ? { backends: 1, sessions: 0, routes: 2 }
              : { backends: 0, sessions: 0, routes: 0 },
        }),
      ),
    });
    const user = setupMenuUser();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    await user.click(
      screen.getByRole("button", {
        name: "More actions for claude-opus-4-1",
      }),
    );
    const menu = await screen.findByRole("menu");
    const deleteItem = within(menu).getByRole("menuitem", {
      name: "Delete claude-opus-4-1",
    });
    // 被引用不再挡住删除入口：只有默认模型才禁用
    expect(deleteItem).not.toHaveAttribute("aria-disabled", "true");
    expect(deleteItem).not.toHaveAttribute("title");
    expect(mocks.DeleteLLMModel).not.toHaveBeenCalled();
    // 引用计数按稳定 modelKey 查询
    expect(mocks.LLMModelRefCounts).toHaveBeenCalledWith(
      expect.objectContaining({ modelKey: "mk-opus" }),
    );
  });

  it("Given an unreferenced model, When delete is confirmed via the row menu, Then DeleteLLMModel is called", async () => {
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [
            makeModel(),
            makeModel({
              id: 12,
              modelKey: "mk-opus",
              modelId: "claude-opus-4-1",
              isDefault: false,
            }),
          ],
        }),
      ),
    });
    const user = setupMenuUser();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    await user.click(
      screen.getByRole("button", {
        name: "More actions for claude-opus-4-1",
      }),
    );
    await user.click(
      await screen.findByRole("menuitem", { name: "Delete claude-opus-4-1" }),
    );
    await user.click(
      await screen.findByRole("button", { name: "Delete model" }),
    );

    await waitFor(() => {
      expect(mocks.DeleteLLMModel).toHaveBeenCalledWith(
        expect.objectContaining({ id: 12 }),
      );
    });
  });

  it("Given a referenced model, When its Model ID is edited, Then the dialog shows impact counts and requires explicit confirmation before saving", async () => {
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [
            makeModel(),
            makeModel({
              id: 12,
              modelKey: "mk-opus",
              modelId: "claude-opus-4-1",
              isDefault: false,
            }),
          ],
        }),
      ),
      LLMModelRefCounts: vi.fn(() =>
        Promise.resolve({
          counts: { backends: 1, sessions: 1, routes: 0 },
        }),
      ),
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await user.click(
      await screen.findByRole("button", { name: "Edit claude-opus-4-1" }),
    );

    const dialog = await screen.findByRole("dialog", {
      name: /Edit model/,
    });
    // 稳定 modelKey 只读可选中复制（不是输入框），modelId 可编辑
    expect(within(dialog).queryByLabelText("Model Key")).toBeNull();
    expect(within(dialog).getByText("mk-opus")).toHaveAttribute(
      "data-selectable-text",
      "true",
    );
    const modelIdInput = within(dialog).getByLabelText("Model ID");
    expect(modelIdInput).toHaveValue("claude-opus-4-1");

    // 未改 modelId 时不需要引用确认；改动后才出现影响提示
    const save = within(dialog).getByRole("button", { name: "Save changes" });
    fireEvent.change(modelIdInput, { target: { value: "claude-opus-4-2" } });

    expect(
      within(dialog).getByText(/This model is referenced/i),
    ).toBeInTheDocument();
    const confirmBox = within(dialog).getByRole("checkbox", {
      name: /I understand the impact/i,
    });
    expect(save).toBeDisabled();

    await user.click(confirmBox);
    expect(save).toBeEnabled();
    await user.click(save);

    await waitFor(() => {
      expect(mocks.UpdateLLMModel).toHaveBeenCalledWith(
        expect.objectContaining({
          id: 12,
          modelId: "claude-opus-4-2",
          confirmReference: true,
        }),
      );
    });
    // 引用计数按稳定 modelKey 查询
    expect(mocks.LLMModelRefCounts).toHaveBeenCalledWith(
      expect.objectContaining({ modelKey: "mk-opus" }),
    );
  });

  it("Given the model edit dialog, When it renders, Then fields run display name → model ID → change warning → context/max output, with Model Key last as a read-only chip", async () => {
    installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [
            makeModel(),
            makeModel({
              id: 12,
              modelKey: "mk-opus",
              modelId: "claude-opus-4-1",
              isDefault: false,
            }),
          ],
        }),
      ),
      LLMModelRefCounts: vi.fn(() =>
        Promise.resolve({ counts: { backends: 1, sessions: 1, routes: 0 } }),
      ),
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await user.click(
      await screen.findByRole("button", { name: "Edit claude-opus-4-1" }),
    );
    const dialog = await screen.findByRole("dialog", { name: /Edit model/ });

    const nameInput = within(dialog).getByLabelText("Display name");
    const modelIdInput = within(dialog).getByLabelText("Model ID");
    const contextInput = within(dialog).getByLabelText("Context Window");
    const modelKeyChip = within(dialog).getByText("mk-opus");

    const follows = (a: Element, b: Element) =>
      Boolean(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING);

    expect(follows(nameInput, modelIdInput)).toBe(true);
    expect(follows(modelIdInput, contextInput)).toBe(true);
    expect(follows(contextInput, modelKeyChip)).toBe(true);

    // 改 ID 的影响提示紧跟模型 ID，而不是被挤到最后
    fireEvent.change(modelIdInput, { target: { value: "claude-opus-4-2" } });
    const warning = await within(dialog).findByText(
      /This model is referenced/i,
    );
    expect(follows(modelIdInput, warning)).toBe(true);
    expect(follows(warning, contextInput)).toBe(true);
  });

  it("Given an unreferenced model, When its Model ID is edited, Then UpdateLLMModel saves without reference confirmation", async () => {
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [
            makeModel(),
            makeModel({
              id: 12,
              modelKey: "mk-opus",
              modelId: "claude-opus-4-1",
              isDefault: false,
            }),
          ],
        }),
      ),
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await user.click(
      await screen.findByRole("button", { name: "Edit claude-opus-4-1" }),
    );
    const dialog = await screen.findByRole("dialog", {
      name: /Edit model/,
    });
    fireEvent.change(within(dialog).getByLabelText("Model ID"), {
      target: { value: "claude-opus-4-2" },
    });
    await user.click(
      within(dialog).getByRole("button", { name: "Save changes" }),
    );

    await waitFor(() => {
      expect(mocks.UpdateLLMModel).toHaveBeenCalledWith(
        expect.objectContaining({
          id: 12,
          modelId: "claude-opus-4-2",
          confirmReference: false,
        }),
      );
    });
  });

  it("Given a provider workspace, When Discover is opened, Then PreviewModels is scanned and selected discoveries are imported via ImportModels", async () => {
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [makeModel({ modelId: "deepseek-chat" })],
        }),
      ),
      PreviewLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [
            {
              id: "deepseek-chat",
              vendor: "deepseek",
              contextWindow: 64000,
              maxOutput: 4096,
              modalities: [],
              thinking: false,
              knownInCago: false,
            },
            {
              id: "deepseek-v3.2",
              vendor: "deepseek",
              contextWindow: 128000,
              maxOutput: 8192,
              modalities: [],
              thinking: false,
              knownInCago: false,
            },
          ],
        }),
      ),
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    await user.click(screen.getByRole("button", { name: "Discover models" }));

    const dialog = await screen.findByRole("dialog", {
      name: /Discover/,
    });
    // 扫描使用已保存连接（apiKey 为空 → 沿用保存值）
    expect(mocks.PreviewLLMModels).toHaveBeenCalledWith(
      expect.objectContaining({ id: 1, apiKey: "" }),
    );
    // 已存在项标记为跳过（preview 与 existingModels 均为异步加载，等待真实状态）
    expect(
      await within(dialog).findByText(/Already exists/),
    ).toBeInTheDocument();
    expect(within(dialog).getByText(/deepseek-chat/)).toBeInTheDocument();
    // 读取成功的状态 chip 落在弹窗头部
    expect(within(dialog).getByText("Loaded")).toBeInTheDocument();

    await user.click(
      within(dialog).getByRole("button", { name: "Import 1 model" }),
    );

    await waitFor(() => {
      expect(mocks.ImportLLMModels).toHaveBeenCalledWith(
        expect.objectContaining({
          providerId: 1,
          models: expect.arrayContaining([
            expect.objectContaining({ modelId: "deepseek-v3.2" }),
          ]),
        }),
      );
    });
  });

  it("Given a provider with no default model, When its enable switch is used, Then it is disabled with a visible reason", async () => {
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({
          items: [
            makeProvider({
              enabled: false,
              defaultModelKey: "",
            }),
          ],
        }),
      ),
      ListLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [makeModel({ isDefault: false })],
        }),
      ),
    });
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    const enableSwitch = screen.getByRole("switch", {
      name: "Enable Anthropic",
    });
    expect(enableSwitch).toBeDisabled();
    expect(enableSwitch).toHaveAttribute(
      "title",
      expect.stringMatching(/default model/i),
    );
    expect(mocks.SetLLMProviderEnabled).not.toHaveBeenCalled();
  });

  it("Given a provider with an enabled default, When its enable switch is toggled off, Then SetLLMProviderEnabled is called", async () => {
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() => Promise.resolve({ items: [makeModel()] })),
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    await user.click(screen.getByRole("switch", { name: "Enable Anthropic" }));

    await waitFor(() => {
      expect(mocks.SetLLMProviderEnabled).toHaveBeenCalledWith(
        expect.objectContaining({ id: 1, enabled: false }),
      );
    });
  });

  it("Given a referenced provider, When the More menu is opened, Then Delete is usable — the reference count is shown as meta, not as a veto", async () => {
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() => Promise.resolve({ items: [makeModel()] })),
      LLMProviderRefCounts: vi.fn(() =>
        Promise.resolve({
          counts: { backends: 2, sessions: 0, routes: 0 },
        }),
      ),
    });
    const user = setupMenuUser();
    render(<LlmProvidersPanel />);

    const workspace = await waitForModelTable(/Anthropic models/);
    // 引用计数落地后才判定：等元信息行显示出真实计数
    await within(workspace).findByText("2 backends");

    await user.click(screen.getByRole("button", { name: "More" }));
    const deleteItem = within(await screen.findByRole("menu")).getByRole(
      "menuitem",
      { name: "Delete Anthropic" },
    );
    expect(deleteItem).not.toHaveAttribute("aria-disabled", "true");
    expect(deleteItem).not.toHaveAttribute("title");
    expect(mocks.DeleteLLMProvider).not.toHaveBeenCalled();
    expect(mocks.LLMProviderRefCounts).toHaveBeenCalledWith(
      expect.objectContaining({ providerKey: "pk-1" }),
    );
  });

  it("Given the reference count went stale, When the delete dialog re-checks, Then it discloses the fresh impact and still offers both delete and disable", async () => {
    let referenced = false;
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [
            makeModel(),
            makeModel({
              id: 12,
              modelKey: "mk-opus",
              modelId: "claude-opus-4-1",
              name: "",
              isDefault: false,
            }),
          ],
        }),
      ),
      LLMModelRefCounts: vi.fn((req: unknown) =>
        Promise.resolve({
          counts:
            referenced && (req as { modelKey?: string }).modelKey === "mk-opus"
              ? { backends: 1, sessions: 2, routes: 0 }
              : { backends: 0, sessions: 0, routes: 0 },
        }),
      ),
    });
    const user = setupMenuUser();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    // 表格先读到「无引用」，行内删除项因此可点
    await user.click(
      screen.getByRole("checkbox", { name: "Select claude-opus-4-1" }),
    );
    await within(rowForModel("claude-opus-4-1")).findByText("Can delete");

    // 打开删除弹窗时后端已有新引用：弹窗自己复查，把新影响披露出来
    referenced = true;
    await user.click(
      screen.getByRole("button", { name: "More actions for claude-opus-4-1" }),
    );
    await user.click(
      await screen.findByRole("menuitem", { name: "Delete claude-opus-4-1" }),
    );

    const dialog = await screen.findByRole("dialog", { name: /Delete model/ });
    await within(dialog).findByText("1 backends · 2 sessions");
    const disableInstead = await within(dialog).findByRole("button", {
      name: "Disable instead",
    });
    // 两条路并排：删除照常可用，停用作为可恢复的次要出口留着
    expect(
      within(dialog).getByRole("button", { name: "Delete model" }),
    ).toBeEnabled();

    await user.click(disableInstead);

    await waitFor(() => {
      expect(mocks.SetLLMModelEnabled).toHaveBeenCalledWith(
        expect.objectContaining({ id: 12, enabled: false }),
      );
    });
    expect(mocks.DeleteLLMModel).not.toHaveBeenCalled();
  });

  it("Given an unreferenced provider, When delete is confirmed, Then DeleteLLMProvider is called", async () => {
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() => Promise.resolve({ items: [makeModel()] })),
    });
    const user = setupMenuUser();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    await user.click(screen.getByRole("button", { name: "More" }));
    await user.click(
      await screen.findByRole("menuitem", { name: "Delete Anthropic" }),
    );
    await user.click(
      await screen.findByRole("button", { name: "Delete provider" }),
    );

    await waitFor(() => {
      expect(mocks.DeleteLLMProvider).toHaveBeenCalledWith(
        expect.objectContaining({ id: 1 }),
      );
    });
  });

  it("Given the create dialog, When a provider with a default model is submitted, Then the request is sent and success is reported", async () => {
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() => Promise.resolve({ items: [] })),
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await screen.findByRole("button", { name: "Add First Provider" });
    await user.click(
      screen.getByRole("button", { name: "Add First Provider" }),
    );

    const dialog = await screen.findByRole("dialog", {
      name: "New LLM Provider",
    });
    fireEvent.change(within(dialog).getByLabelText("Name"), {
      target: { value: "My Provider" },
    });
    fireEvent.change(within(dialog).getByLabelText(/^API Key$/), {
      target: { value: "sk-test" },
    });
    fireEvent.change(within(dialog).getByLabelText(/^Base URL/), {
      target: { value: "https://api.example.com" },
    });

    // 手工添加一个模型并选择为默认
    await user.click(within(dialog).getByRole("button", { name: "Add model" }));
    fireEvent.change(within(dialog).getByLabelText("Model ID"), {
      target: { value: "claude-sonnet-4-5" },
    });
    fireEvent.change(within(dialog).getByLabelText("Context"), {
      target: { value: "200000" },
    });
    fireEvent.change(within(dialog).getByLabelText("Output"), {
      target: { value: "64000" },
    });
    await user.click(
      within(dialog).getByRole("radio", {
        name: "Set claude-sonnet-4-5 as default",
      }),
    );

    await user.click(within(dialog).getByRole("button", { name: "Create" }));

    await waitFor(() => {
      expect(mocks.CreateLLMProvider).toHaveBeenCalledWith(
        expect.objectContaining({
          type: "anthropic",
          name: "My Provider",
          apiKey: "sk-test",
          baseUrl: "https://api.example.com",
          defaultModelId: "claude-sonnet-4-5",
          models: expect.arrayContaining([
            expect.objectContaining({
              modelId: "claude-sonnet-4-5",
              contextWindow: 200000,
              maxOutput: 64000,
            }),
          ]),
        }),
      );
    });
    expect(
      await screen.findByText('Provider "My Provider" added'),
    ).toBeInTheDocument();
  });

  it("Given a provider with models, When the connection is edited, Then UpdateLLMProvider preserves the saved API key when untouched", async () => {
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() => Promise.resolve({ items: [makeModel()] })),
    });
    const user = setupMenuUser();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    await user.click(screen.getByRole("button", { name: "More" }));
    await user.click(
      await screen.findByRole("menuitem", {
        name: "Edit Anthropic connection",
      }),
    );
    const dialog = await screen.findByRole("dialog", {
      name: /Edit connection/,
    });
    // 掩码 key 预填，保存时归一化为空（保留已保存值）
    expect(within(dialog).getByLabelText(/^API Key$/)).toHaveValue(
      "sk-••••••9XQ2",
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Save changes" }),
    );

    await waitFor(() => {
      expect(mocks.UpdateLLMProvider).toHaveBeenCalledWith(
        expect.objectContaining({ id: 1, apiKey: "" }),
      );
    });
  });

  it("Given an empty provider list, When the panel loads, Then an empty state with a primary CTA is shown", async () => {
    installAppMock();
    render(<LlmProvidersPanel />);

    expect(
      await screen.findByRole("button", { name: "Add First Provider" }),
    ).toBeInTheDocument();
  });

  it("Given the provider list fails to load, When the panel mounts, Then an error flash is shown", async () => {
    installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.reject(new Error("database locked")),
      ),
    });
    render(<LlmProvidersPanel />);

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Load failed: database locked",
    );
  });

  it("Given providers exist, When the panel loads, Then its single New Provider entry is handed to the page header slot and the old toolbar and nav add entry are gone", async () => {
    installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() => Promise.resolve({ items: [makeModel()] })),
    });
    render(
      <LlmProvidersPanel
        renderHeader={(actions) => (
          <div data-testid="page-header">{actions}</div>
        )}
      />,
    );

    await waitForModelTable(/Anthropic models/);

    // mockup 注解①：唯一新增入口交给页头 slot 渲染，面板自己不再留一层 strip
    expect(
      within(screen.getByTestId("page-header")).getByRole("button", {
        name: "New Provider",
      }),
    ).toBeInTheDocument();
    expect(
      screen.getAllByRole("button", { name: "New Provider" }),
    ).toHaveLength(1);
    expect(screen.queryByRole("button", { name: "Add Provider" })).toBeNull();
    expect(screen.queryByText("Configured Providers")).toBeNull();
    // mockup 注解①：删掉「已配置的供应商 / 共 N 个」重复计数条，不再单独占一层 strip
    expect(screen.queryByText("1 total")).toBeNull();
  });

  it("Given the page header slot renders the New Provider entry, When it is clicked, Then the create dialog opens", async () => {
    const user = userEvent.setup();
    installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() => Promise.resolve({ items: [makeModel()] })),
    });
    render(
      <LlmProvidersPanel
        renderHeader={(actions) => (
          <div data-testid="page-header">{actions}</div>
        )}
      />,
    );

    await waitForModelTable(/Anthropic models/);
    await user.click(
      within(screen.getByTestId("page-header")).getByRole("button", {
        name: "New Provider",
      }),
    );

    expect(
      await screen.findByRole("dialog", { name: "New LLM Provider" }),
    ).toBeInTheDocument();
  });

  it("Given no providers exist, When the panel loads, Then the page header slot gets no add entry and the empty state keeps the only one", async () => {
    installAppMock({
      ListLLMProviders: vi.fn(() => Promise.resolve({ items: [] })),
    });
    render(
      <LlmProvidersPanel
        renderHeader={(actions) => (
          <div data-testid="page-header">{actions}</div>
        )}
      />,
    );

    await screen.findByRole("button", { name: "Add First Provider" });

    expect(
      within(screen.getByTestId("page-header")).queryByRole("button"),
    ).toBeNull();
    expect(screen.queryByRole("button", { name: "New Provider" })).toBeNull();
  });

  it("Given a provider is selected, When the workspace renders, Then the header splits into identity and metadata rows with the combined switch first, then Test Connection, Discover Models and More", async () => {
    installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({
          items: [makeProvider({ name: "Anthropic Official" })],
        }),
      ),
      ListLLMModels: vi.fn(() => Promise.resolve({ items: [makeModel()] })),
      LLMProviderRefCounts: vi.fn(() =>
        Promise.resolve({ counts: { backends: 2, sessions: 1, routes: 0 } }),
      ),
    });
    render(<LlmProvidersPanel />);

    const workspace = await waitForModelTable(/Anthropic Official models/);

    // 身份行：名称 + 协议类型
    expect(
      within(workspace).getByText("Anthropic Official"),
    ).toBeInTheDocument();
    expect(
      within(workspace).getByText("Anthropic", { selector: "span" }),
    ).toBeInTheDocument();

    // 元信息行：endpoint / 掩码 key / 默认模型 / 被引用
    expect(
      within(workspace).getByText("https://api.anthropic.com"),
    ).toBeInTheDocument();
    expect(within(workspace).getByText("sk-••••••9XQ2")).toBeInTheDocument();
    expect(within(workspace).getByText("Default model")).toBeInTheDocument();
    expect(
      (await within(workspace).findAllByText("claude-sonnet-4-5")).length,
    ).toBeGreaterThan(0);
    expect(within(workspace).getByText("Referenced")).toBeInTheDocument();
    expect(
      await within(workspace).findByText("2 backends · 1 session"),
    ).toBeInTheDocument();

    // 操作区顺序：开关（含状态文字）→ 测试连接 → 发现模型 → 更多
    const switchEl = within(workspace).getByRole("switch", {
      name: "Enable Anthropic Official",
    });
    const testBtn = within(workspace).getByRole("button", {
      name: "Test Anthropic Official",
    });
    const discoverBtn = within(workspace).getByRole("button", {
      name: "Discover models",
    });
    const moreBtn = within(workspace).getByRole("button", { name: "More" });
    expect(
      switchEl.compareDocumentPosition(testBtn) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(
      testBtn.compareDocumentPosition(discoverBtn) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(
      discoverBtn.compareDocumentPosition(moreBtn) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    // 启用状态与开关合成一个控件，且可见文案为 Test Connection
    expect(switchEl.closest("label")).toHaveTextContent("Enabled");
    expect(within(workspace).getByText("Test Connection")).toBeInTheDocument();
  });

  it("Given a provider workspace, When the More menu is opened, Then it contains Edit Connection, Copy Provider Key and Delete", async () => {
    installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() => Promise.resolve({ items: [makeModel()] })),
    });
    const user = setupMenuUser();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    await user.click(screen.getByRole("button", { name: "More" }));

    const menu = await screen.findByRole("menu");
    expect(
      within(menu).getByRole("menuitem", { name: "Edit Anthropic connection" }),
    ).toBeInTheDocument();
    expect(
      within(menu).getByRole("menuitem", { name: "Copy Provider Key" }),
    ).toBeInTheDocument();
    expect(
      within(menu).getByRole("menuitem", { name: "Delete Anthropic" }),
    ).toBeInTheDocument();
  });

  it("Given models, When the header checkbox is clicked, Then all currently-listed models are selected and the action bar shows the selected count", async () => {
    installThreeModels();
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    await user.click(
      screen.getByRole("checkbox", { name: "Select all models" }),
    );

    expect(screen.getByText("Selected 3 / 3")).toBeInTheDocument();
    expect(
      screen.getByRole("checkbox", { name: "Select claude-sonnet-4-5" }),
    ).toBeChecked();
    expect(
      screen.getByRole("checkbox", { name: "Select claude-opus-4-1" }),
    ).toBeChecked();
    expect(
      screen.getByRole("checkbox", { name: "Select claude-haiku" }),
    ).toBeChecked();
  });

  it("Given a provider that already has models, When the model toolbar renders, Then Add model manually sits between the filter and the count and still opens the manual add dialog", async () => {
    installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() => Promise.resolve({ items: [makeModel()] })),
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    const workspace = await waitForModelTable(/Anthropic models/);
    const search = within(workspace).getByRole("searchbox", {
      name: "Filter models",
    });
    const addButton = within(workspace).getByRole("button", {
      name: "Add model manually",
    });
    const count = within(workspace).getByText("Showing 1 / 1");

    expect(
      search.compareDocumentPosition(addButton) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(
      addButton.compareDocumentPosition(count) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();

    await user.click(addButton);
    expect(
      await screen.findByRole("dialog", { name: /Add model/ }),
    ).toBeInTheDocument();
  });

  it("Given a disabled model, When the table renders, Then only that row carries a disabled badge", async () => {
    installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [
            makeModel(),
            makeModel({
              id: 12,
              modelKey: "mk-opus",
              modelId: "claude-opus-4-1",
              name: "",
              isDefault: false,
              enabled: false,
            }),
          ],
        }),
      ),
    });
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    expect(
      within(rowForModel("claude-opus-4-1")).getByText("Disabled"),
    ).toBeInTheDocument();
    expect(
      within(rowForModel("claude-sonnet-4-5")).queryByText("Disabled"),
    ).toBeNull();
  });

  it("Given selection is active, When the toolbar is inspected, Then the search/count toolbar is replaced in place by the selection action bar", async () => {
    installThreeModels();
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    expect(
      screen.getByRole("searchbox", { name: "Filter models" }),
    ).toBeInTheDocument();

    await user.click(
      screen.getByRole("checkbox", { name: "Select all models" }),
    );

    // 工具栏被选择态操作条原地替换，搜索框消失
    expect(
      screen.queryByRole("searchbox", { name: "Filter models" }),
    ).toBeNull();
    expect(
      screen.getByRole("button", { name: "Select all" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Clear selection" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Enable selected" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Disable selected" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Delete selected" }),
    ).toBeInTheDocument();
  });

  it("Given models are selected, When another provider is opened, Then the old provider selection is cleared", async () => {
    const first = makeProvider({
      id: 1,
      name: "Anthropic",
      providerKey: "pk-1",
    });
    const second = makeProvider({
      id: 2,
      name: "OpenAI",
      providerKey: "pk-2",
      type: "openai-response",
      defaultModelKey: "mk-gpt",
    });
    installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [first, second] }),
      ),
      ListLLMModels: vi.fn((req: unknown) =>
        Promise.resolve({
          items:
            (req as { id?: number }).id === 1
              ? [makeModel()]
              : [
                  makeModel({
                    id: 21,
                    providerId: 2,
                    providerKey: "pk-2",
                    modelKey: "mk-gpt",
                    modelId: "gpt-5",
                  }),
                ],
        }),
      ),
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    await user.click(
      screen.getByRole("checkbox", { name: "Select claude-sonnet-4-5" }),
    );
    expect(
      screen.getByRole("button", { name: "Clear selection" }),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /OpenAI/ }));

    await waitForModelTable(/OpenAI models/);
    expect(
      screen.getByRole("searchbox", { name: "Filter models" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Clear selection" }),
    ).toBeNull();
  });

  it("Given selected models, When each row is inspected, Then each row annotates whether it can be deleted", async () => {
    installThreeModels({
      "mk-opus": { backends: 1, sessions: 0, routes: 2 },
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    await user.click(
      screen.getByRole("checkbox", { name: "Select all models" }),
    );

    // 被引用模型的标注依赖异步引用计数，等待其加载
    expect(
      await within(rowForModel("claude-opus-4-1")).findByText(
        "Referenced by 3: becomes invalid after deletion",
      ),
    ).toBeInTheDocument();
    expect(
      within(rowForModel("claude-sonnet-4-5")).getByText(
        "Default model: cannot delete",
      ),
    ).toBeInTheDocument();
    expect(
      within(rowForModel("claude-haiku")).getByText("Can delete"),
    ).toBeInTheDocument();
  });

  it("Given selected models including the default one, When batch delete is requested, Then only the default model is protected and referenced models are listed for deletion with their impact", async () => {
    installThreeModels({
      "mk-opus": { backends: 1, sessions: 0, routes: 2 },
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    await user.click(
      screen.getByRole("checkbox", { name: "Select all models" }),
    );
    await within(rowForModel("claude-opus-4-1")).findByText(
      "Referenced by 3: becomes invalid after deletion",
    );

    await user.click(screen.getByRole("button", { name: "Delete selected" }));

    const dialog = await screen.findByRole("dialog", {
      name: /selected models/,
    });
    expect(within(dialog).getByText("Will be deleted")).toBeInTheDocument();
    expect(
      within(dialog).getByText("Protected (unchanged)"),
    ).toBeInTheDocument();
    // 可删除组包含 haiku 与被引用的 opus（后者带上引用影响）；受保护组只剩默认模型
    expect(within(dialog).getByText("claude-haiku")).toBeInTheDocument();
    expect(within(dialog).getByText("claude-opus-4-1")).toBeInTheDocument();
    expect(
      within(dialog).getByText(
        "Referenced by 3: becomes invalid after deletion",
      ),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByText("Default model: cannot delete"),
    ).toBeInTheDocument();
    const confirm = within(dialog).getByRole("button", {
      name: "Delete 2 models",
    });
    expect(confirm).toBeEnabled();
  });

  it("Given a mixed selection, When batch delete is requested, Then the title and description carry the real counts and the non-transactional caveat is always shown", async () => {
    installThreeModels({
      "mk-opus": { backends: 1, sessions: 0, routes: 2 },
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    await user.click(
      screen.getByRole("checkbox", { name: "Select all models" }),
    );
    await within(rowForModel("claude-opus-4-1")).findByText(
      "Referenced by 3: becomes invalid after deletion",
    );
    await user.click(screen.getByRole("button", { name: "Delete selected" }));

    // 标题写清选中数，描述写清被保护数与真正会删掉的数量
    const dialog = await screen.findByRole("dialog", {
      name: /Delete the 3 selected models/,
    });
    expect(
      within(dialog).getByText(
        /1 of them are protected .* only 2 will actually be deleted/i,
      ),
    ).toBeInTheDocument();
    // 非事务性提醒常驻
    expect(
      within(dialog).getByText(/not as a transaction/i),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByRole("button", { name: "Delete 2 models" }),
    ).toBeEnabled();
  });

  it("Given every selected model is deletable, When batch delete is requested, Then the description says so and the caveat is still shown", async () => {
    installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [
            makeModel({
              id: 12,
              modelKey: "mk-opus",
              modelId: "claude-opus-4-1",
              name: "",
              isDefault: false,
            }),
          ],
        }),
      ),
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    await user.click(
      screen.getByRole("checkbox", { name: "Select all models" }),
    );
    await user.click(screen.getByRole("button", { name: "Delete selected" }));

    const dialog = await screen.findByRole("dialog", {
      name: /Delete the 1 selected model/,
    });
    expect(
      within(dialog).getByText(/All 1 selected model will be deleted/i),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByText(/not as a transaction/i),
    ).toBeInTheDocument();
  });

  it("Given only the default model is selected, When batch delete is requested, Then the primary button is disabled and explains there is nothing to delete", async () => {
    installThreeModels({
      "mk-opus": { backends: 1, sessions: 0, routes: 2 },
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    // 默认模型是唯一仍被保护的一档
    await user.click(
      screen.getByRole("checkbox", { name: "Select claude-sonnet-4-5" }),
    );
    await within(rowForModel("claude-sonnet-4-5")).findByText(
      "Default model: cannot delete",
    );

    await user.click(screen.getByRole("button", { name: "Delete selected" }));

    const dialog = await screen.findByRole("dialog", {
      name: /Delete the 1 selected model/,
    });
    expect(within(dialog).getByText("No deletable models")).toBeInTheDocument();
    expect(
      within(dialog).getByRole("button", { name: "Delete 0 models" }),
    ).toBeDisabled();
  });

  it("Given two deletable models are selected, When batch delete is confirmed, Then DeleteLLMModel is called sequentially for each and the flash reports the deleted count", async () => {
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [
            makeModel({
              id: 12,
              modelKey: "mk-opus",
              modelId: "claude-opus-4-1",
              name: "",
              isDefault: false,
            }),
            makeModel({
              id: 13,
              modelKey: "mk-haiku",
              modelId: "claude-haiku",
              name: "",
              isDefault: false,
            }),
          ],
        }),
      ),
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    await user.click(
      screen.getByRole("checkbox", { name: "Select all models" }),
    );
    await user.click(screen.getByRole("button", { name: "Delete selected" }));

    const dialog = await screen.findByRole("dialog", {
      name: /selected models/,
    });
    await user.click(
      within(dialog).getByRole("button", { name: "Delete 2 models" }),
    );

    await waitFor(() => {
      expect(mocks.DeleteLLMModel).toHaveBeenCalledTimes(2);
    });
    expect(mocks.DeleteLLMModel).toHaveBeenNthCalledWith(
      1,
      expect.objectContaining({ id: 12 }),
    );
    expect(mocks.DeleteLLMModel).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({ id: 13 }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Deleted 2 models",
    );
  });

  it("Given a deletable batch where one delete fails, When batch delete runs, Then it stops at the failure and reports deleted and unprocessed counts", async () => {
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [
            makeModel({
              id: 12,
              modelKey: "mk-opus",
              modelId: "claude-opus-4-1",
              name: "",
              isDefault: false,
            }),
            makeModel({
              id: 13,
              modelKey: "mk-haiku",
              modelId: "claude-haiku",
              name: "",
              isDefault: false,
            }),
            makeModel({
              id: 14,
              modelKey: "mk-sonnet-lite",
              modelId: "claude-sonnet-lite",
              name: "",
              isDefault: false,
            }),
          ],
        }),
      ),
      DeleteLLMModel: vi
        .fn()
        .mockResolvedValueOnce({})
        .mockRejectedValueOnce(new Error("database locked"))
        .mockResolvedValue({}),
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    await user.click(
      screen.getByRole("checkbox", { name: "Select all models" }),
    );
    await user.click(screen.getByRole("button", { name: "Delete selected" }));

    const dialog = await screen.findByRole("dialog", {
      name: /selected models/,
    });
    await user.click(
      within(dialog).getByRole("button", { name: "Delete 3 models" }),
    );

    // 第 2 条失败后停止，第 3 条不再调用
    await waitFor(() => {
      expect(mocks.DeleteLLMModel).toHaveBeenCalledTimes(2);
    });
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(/Deleted 1 model/);
    expect(alert).toHaveTextContent(/2 not processed/);
  });

  it("Given selected models including the default, When batch disable runs, Then the default model is skipped with an explanation and the flash reports the disabled count", async () => {
    const mocks = installThreeModels({
      "mk-opus": { backends: 1, sessions: 0, routes: 2 },
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    await user.click(
      screen.getByRole("checkbox", { name: "Select all models" }),
    );
    await user.click(screen.getByRole("button", { name: "Disable selected" }));

    await waitFor(() => {
      expect(mocks.SetLLMModelEnabled).toHaveBeenCalledTimes(2);
    });
    // 默认模型（id 11）不参与停用；opus 与 haiku 被停用
    expect(mocks.SetLLMModelEnabled).toHaveBeenCalledWith(
      expect.objectContaining({ id: 12, enabled: false }),
    );
    expect(mocks.SetLLMModelEnabled).toHaveBeenCalledWith(
      expect.objectContaining({ id: 13, enabled: false }),
    );
    expect(mocks.SetLLMModelEnabled).not.toHaveBeenCalledWith(
      expect.objectContaining({ id: 11 }),
    );
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(/Disabled 2 models/);
    expect(alert).toHaveTextContent(/default model excluded/);
  });

  it("Given selected models with a disabled one, When batch enable runs, Then only disabled models are enabled and the flash reports the count", async () => {
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [
            makeModel({
              id: 12,
              modelKey: "mk-opus",
              modelId: "claude-opus-4-1",
              name: "",
              isDefault: false,
              enabled: false,
            }),
            makeModel({
              id: 13,
              modelKey: "mk-haiku",
              modelId: "claude-haiku",
              name: "",
              isDefault: false,
            }),
          ],
        }),
      ),
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    await user.click(
      screen.getByRole("checkbox", { name: "Select all models" }),
    );
    await user.click(screen.getByRole("button", { name: "Enable selected" }));

    await waitFor(() => {
      expect(mocks.SetLLMModelEnabled).toHaveBeenCalledTimes(1);
    });
    expect(mocks.SetLLMModelEnabled).toHaveBeenCalledWith(
      expect.objectContaining({ id: 12, enabled: true }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Enabled 1 model",
    );
  });

  it("Given a provider workspace, When Copy Provider Key is chosen from the More menu, Then the provider key is written to the clipboard and a success flash is shown", async () => {
    installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() => Promise.resolve({ items: [makeModel()] })),
    });
    const user = setupMenuUser();
    const writeText = mockClipboard();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    await user.click(screen.getByRole("button", { name: "More" }));
    // fireEvent 直接触发 Radix DropdownMenuItem onSelect（不产生 pointer-leave 关闭菜单）
    fireEvent.click(
      await screen.findByRole("menuitem", { name: "Copy Provider Key" }),
    );

    await waitFor(() => {
      expect(writeText).toHaveBeenCalledWith("pk-1");
    });
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Provider Key copied",
    );
  });

  it("Given a multi-vendor preview with existing and unknown-vendor models, When discover is opened, Then models are grouped by vendor, existing items stay disabled, and select-all targets only new models", async () => {
    installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() =>
        Promise.resolve({ items: [makeModel({ modelId: "gpt-5" })] }),
      ),
      PreviewLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [
            {
              id: "gpt-5",
              vendor: "openai",
              contextWindow: 100000,
              maxOutput: 8000,
              modalities: [],
              thinking: false,
              knownInCago: true,
            },
            {
              id: "gpt-5.1",
              vendor: "openai",
              contextWindow: 120000,
              maxOutput: 9000,
              modalities: [],
              thinking: false,
              knownInCago: true,
            },
            {
              id: "deepseek-chat",
              vendor: "deepseek",
              contextWindow: 64000,
              maxOutput: 4096,
              modalities: [],
              thinking: false,
              knownInCago: false,
            },
            {
              id: "custom-xyz",
              vendor: "",
              contextWindow: 0,
              maxOutput: 0,
              modalities: [],
              thinking: false,
              knownInCago: false,
            },
          ],
        }),
      ),
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    await user.click(screen.getByRole("button", { name: "Discover models" }));

    const dialog = await screen.findByRole("dialog", { name: /Discover/ });

    // 远端总数 / 新增数 / 已存在数
    expect(
      await within(dialog).findByText("Remote 4 · new 3 · existing 1"),
    ).toBeInTheDocument();

    // 按模型族分组 + 认不出的兜底分组
    expect(within(dialog).getByText("OpenAI")).toBeInTheDocument();
    expect(within(dialog).getByText("DeepSeek")).toBeInTheDocument();
    expect(within(dialog).getByText("Other")).toBeInTheDocument();

    // 新 / 已存在 · 跳过 区分
    expect(within(dialog).getAllByText("New")).toHaveLength(3);
    expect(
      within(dialog).getByText("Already exists · skip"),
    ).toBeInTheDocument();

    // 已存在项不可勾选
    expect(
      within(dialog).getByRole("checkbox", { name: "gpt-5" }),
    ).toBeDisabled();

    // 全选只作用于新模型：先取消一个新项使全选处于未全选态，再点全选
    await user.click(within(dialog).getByRole("checkbox", { name: "gpt-5.1" }));
    await user.click(
      within(dialog).getByRole("checkbox", {
        name: "Select all new models",
      }),
    );
    expect(
      within(dialog).getByRole("checkbox", { name: "gpt-5.1" }),
    ).toBeChecked();
    expect(
      within(dialog).getByRole("checkbox", { name: "deepseek-chat" }),
    ).toBeChecked();
    expect(
      within(dialog).getByRole("checkbox", { name: "custom-xyz" }),
    ).toBeChecked();
    expect(
      within(dialog).getByRole("checkbox", { name: "gpt-5" }),
    ).not.toBeChecked();

    // 底部导入结论 + 已存在项保持不变
    expect(
      within(dialog).getByText(/Will import 3 new models/),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByText(
        /keep their name, context, default and reference/,
      ),
    ).toBeInTheDocument();
  });

  it("Given preview fails with an HTTP 401, When discover is opened, Then it shows an understandable reason with next steps and collapses the raw response until expanded", async () => {
    installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() => Promise.resolve({ items: [makeModel()] })),
      PreviewLLMModels: vi.fn(() =>
        Promise.reject(
          new Error(
            'http 401: {"type":"error","error":{"request_id":"req_123"}}',
          ),
        ),
      ),
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    await user.click(screen.getByRole("button", { name: "Discover models" }));

    const dialog = await screen.findByRole("dialog", { name: /Discover/ });
    const alert = await within(dialog).findByRole("alert");

    // 可理解的失败原因，原始 JSON 不作为主文案
    expect(alert).toHaveTextContent(/API Key/i);
    expect(alert).not.toHaveTextContent("request_id");

    // 标题与解释分开两段，标题带上状态码
    const failureTitle = within(alert).getByText("Authentication failed (401)");
    const failureDetail = within(alert).getByText(/rejected the request/i);
    expect(failureTitle).not.toBe(failureDetail);

    // 可执行的下一步：前往编辑连接 / 重试
    expect(
      within(alert).getByRole("button", { name: /Edit connection/i }),
    ).toBeInTheDocument();
    expect(
      within(alert).getByRole("button", { name: "Retry" }),
    ).toBeInTheDocument();

    // 原始响应默认折叠，按需展开
    expect(within(alert).queryByText(/request_id/)).not.toBeInTheDocument();
    await user.click(
      within(alert).getByRole("button", { name: /raw response/i }),
    );
    expect(within(alert).getByText(/request_id/)).toBeInTheDocument();

    // 前往编辑连接直达编辑弹窗
    await user.click(
      within(alert).getByRole("button", { name: /Edit connection/i }),
    );
    expect(
      await screen.findByRole("dialog", { name: /Edit connection/ }),
    ).toBeInTheDocument();
  });

  it("Given a catalog-known model id, When the manual add dialog fills it in, Then context and max output are autofilled, stay editable, and an empty display name falls back to the model id", async () => {
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() => Promise.resolve({ items: [] })),
      LookupLLMModel: vi.fn(() =>
        Promise.resolve({
          known: true,
          vendor: "anthropic",
          contextWindow: 200000,
          maxOutput: 64000,
        }),
      ),
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await waitForEmptyModelSection(/Anthropic models/);
    await user.click(
      await screen.findByRole("button", { name: "Add model manually" }),
    );

    const dialog = await screen.findByRole("dialog", {
      name: /Add model/,
    });
    const modelIdInput = within(dialog).getByLabelText("Model ID");
    fireEvent.change(modelIdInput, { target: { value: "claude-sonnet-4-5" } });
    fireEvent.blur(modelIdInput);

    // 内置目录命中自动填入上下文窗口与最大输出
    await waitFor(() => {
      expect(within(dialog).getByLabelText("Context Window")).toHaveValue(
        200000,
      );
    });
    // 自动补数据必须说明来源，且明说仍可改
    expect(
      within(dialog).getByText(/built-in catalog matched/i),
    ).toBeInTheDocument();
    expect(within(dialog).getByLabelText("Max Output Tokens")).toHaveValue(
      64000,
    );

    // 补全后仍可修改
    fireEvent.change(within(dialog).getByLabelText("Context Window"), {
      target: { value: "300000" },
    });
    expect(within(dialog).getByLabelText("Context Window")).toHaveValue(300000);

    // 显示名称可留空
    expect(within(dialog).getByLabelText("Display name")).toHaveValue("");

    await user.click(within(dialog).getByRole("button", { name: "Add" }));

    await waitFor(() => {
      expect(mocks.ImportLLMModels).toHaveBeenCalledWith(
        expect.objectContaining({
          providerId: 1,
          models: expect.arrayContaining([
            expect.objectContaining({
              modelId: "claude-sonnet-4-5",
              name: "",
              contextWindow: 300000,
              maxOutput: 64000,
            }),
          ]),
        }),
      );
    });
  });

  it("Given a blocked model delete, When Disable instead succeeds, Then the panel flashes the disabled model and reloads the model list", async () => {
    let referenced = false;
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [
            makeModel(),
            makeModel({
              id: 12,
              modelKey: "mk-opus",
              modelId: "claude-opus-4-1",
              name: "",
              isDefault: false,
            }),
          ],
        }),
      ),
      LLMModelRefCounts: vi.fn((req: unknown) =>
        Promise.resolve({
          counts:
            referenced && (req as { modelKey?: string }).modelKey === "mk-opus"
              ? { backends: 1, sessions: 2, routes: 0 }
              : { backends: 0, sessions: 0, routes: 0 },
        }),
      ),
    });
    const user = setupMenuUser();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    const listCallsBefore = (mocks.ListLLMModels as ReturnType<typeof vi.fn>)
      .mock.calls.length;

    referenced = true;
    await user.click(
      screen.getByRole("button", { name: "More actions for claude-opus-4-1" }),
    );
    await user.click(
      await screen.findByRole("menuitem", { name: "Delete claude-opus-4-1" }),
    );
    const dialog = await screen.findByRole("dialog", { name: /Delete model/ });
    await user.click(
      await within(dialog).findByRole("button", { name: "Disable instead" }),
    );

    await waitFor(() => {
      expect(mocks.SetLLMModelEnabled).toHaveBeenCalledWith(
        expect.objectContaining({ id: 12, enabled: false }),
      );
    });
    // 后端已改状态，工作区必须跟着刷新并把结果说出来，而不是留在陈旧的启用态
    expect(
      await screen.findByText("Model claude-opus-4-1 disabled"),
    ).toBeInTheDocument();
    await waitFor(() => {
      expect(
        (mocks.ListLLMModels as ReturnType<typeof vi.fn>).mock.calls.length,
      ).toBeGreaterThan(listCallsBefore);
    });
  });

  it("Given a blocked provider delete, When Disable instead succeeds, Then the panel flashes the disabled provider and reloads the provider list", async () => {
    let referenced = false;
    const mocks = installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() => Promise.resolve({ items: [makeModel()] })),
      LLMProviderRefCounts: vi.fn(() =>
        Promise.resolve({
          counts: referenced
            ? { backends: 2, sessions: 1, routes: 0 }
            : { backends: 0, sessions: 0, routes: 0 },
        }),
      ),
    });
    const user = setupMenuUser();
    render(<LlmProvidersPanel />);

    await waitForModelTable(/Anthropic models/);
    const providerCallsBefore = (
      mocks.ListLLMProviders as ReturnType<typeof vi.fn>
    ).mock.calls.length;

    referenced = true;
    await user.click(screen.getByRole("button", { name: "More" }));
    await user.click(
      await screen.findByRole("menuitem", { name: "Delete Anthropic" }),
    );
    const dialog = await screen.findByRole("dialog", {
      name: /Delete provider/,
    });
    await user.click(
      await within(dialog).findByRole("button", { name: "Disable instead" }),
    );

    await waitFor(() => {
      expect(mocks.SetLLMProviderEnabled).toHaveBeenCalledWith(
        expect.objectContaining({ id: 1, enabled: false }),
      );
    });
    expect(
      await screen.findByText("Provider Anthropic disabled"),
    ).toBeInTheDocument();
    await waitFor(() => {
      expect(
        (mocks.ListLLMProviders as ReturnType<typeof vi.fn>).mock.calls.length,
      ).toBeGreaterThan(providerCallsBefore);
    });
  });

  it("Given a long endpoint, When the nav item renders, Then the endpoint owns its own truncating line and the model count is a separate element that cannot be truncated away", async () => {
    const endpoint = "https://example.invalid/v1/openai/compatible/gateway";
    installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({
          items: [
            makeProvider({
              name: "Long Endpoint Provider",
              baseUrl: endpoint,
              modelCount: 7,
            }),
          ],
        }),
      ),
    });
    render(<LlmProvidersPanel />);

    const nav = await screen.findByRole("complementary", {
      name: "Provider list",
    });
    const item = await within(nav).findByRole("button", {
      name: /Long Endpoint Provider/,
    });

    // endpoint 独占一行：截断只吃 endpoint 自己
    const endpointEl = within(item).getByText(endpoint);
    expect(endpointEl.className).toContain("truncate");

    // 计数是同级独立元素，不参与 endpoint 的截断
    const countEl = within(item).getByText("7 models");
    expect(endpointEl.contains(countEl)).toBe(false);
    expect(countEl.className).toContain("shrink-0");
    expect(countEl.className).not.toContain("truncate");
  });

  it("Given no providers, When the empty state renders, Then it carries no vendor-specific API key documentation link", async () => {
    installAppMock();
    render(<LlmProvidersPanel />);

    await screen.findByRole("button", { name: "Add First Provider" });
    // 供应商可能是 OpenAI / 兼容端点，硬编码 Anthropic 文档链接是错的
    expect(screen.queryByText("How to get an API key")).toBeNull();
    expect(
      document.querySelector('a[href^="https://docs.anthropic.com"]'),
    ).toBeNull();
  });

  it("Given the provider list is still loading, When the spinner renders, Then it says providers are loading, not models", async () => {
    installAppMock({
      ListLLMProviders: vi.fn(() => new Promise(() => {})),
    });
    render(<LlmProvidersPanel />);

    expect(await screen.findByText("Loading providers…")).toBeInTheDocument();
    expect(screen.queryByText("Loading models…")).toBeNull();
  });

  it("Given a model row test succeeds, When it completes, Then only that row gains a transient Passed chip", async () => {
    installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() =>
        Promise.resolve({
          items: [
            makeModel(),
            makeModel({
              id: 12,
              modelKey: "mk-opus",
              modelId: "claude-opus-4-1",
              name: "",
              isDefault: false,
            }),
          ],
        }),
      ),
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await user.click(
      await screen.findByRole("button", { name: "Test claude-opus-4-1" }),
    );

    const testedRow = await waitFor(() => {
      const row = rowForModel("claude-opus-4-1");
      expect(within(row).getByText("Passed")).toBeInTheDocument();
      return row;
    });
    expect(within(testedRow).getByText("Passed")).toBeInTheDocument();
    expect(
      within(rowForModel("claude-sonnet-4-5")).queryByText("Passed"),
    ).toBeNull();
  });

  it("Given a model row test fails, When it completes, Then the row shows no Passed chip", async () => {
    installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(() => Promise.resolve({ items: [makeModel()] })),
      TestLLMProvider: vi.fn(() =>
        Promise.resolve({ ok: false, message: "401 unauthorized" }),
      ),
    });
    const user = userEvent.setup();
    render(<LlmProvidersPanel />);

    await user.click(
      await screen.findByRole("button", { name: "Test claude-sonnet-4-5" }),
    );

    await screen.findByText(/401 unauthorized/);
    expect(
      within(rowForModel("claude-sonnet-4-5")).queryByText("Passed"),
    ).toBeNull();
  });

  // 守卫：模型表属于「供应商列表落地」之后的第二段异步。只要屏障停在工作区
  // region 上，后面的同步查询就是在赌调度顺序——CI 上已经随机红过。这条用例
  // 把模型列表故意排到供应商列表之后，任何退回 region 屏障的写法都会稳定失败。

  it("Given the model list resolves after the provider list, When the panel is rendered, Then the shared barrier lands on the model table instead of the loading placeholder", async () => {
    installAppMock({
      ListLLMProviders: vi.fn(() =>
        Promise.resolve({ items: [makeProvider()] }),
      ),
      ListLLMModels: vi.fn(
        () =>
          new Promise((resolve) => {
            setTimeout(() => resolve({ items: [makeModel()] }), 30);
          }),
      ),
    });
    render(<LlmProvidersPanel />);

    await waitForModelTable("Anthropic models");

    expect(
      screen.getByRole("checkbox", { name: "Select all models" }),
    ).toBeInTheDocument();
  });
});
