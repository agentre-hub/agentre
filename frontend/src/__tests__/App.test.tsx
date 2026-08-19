import {
  act,
  createEvent,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import App from "../App";

// App 渲染 <TurnCompleteNotifier/>，它在 mount 时通过 wailsjs runtime 订阅
// "notification:click"。这些 App 用例不关心该订阅、也不一定设置 window.runtime，
// 故把 Events* 桩成安全 no-op；Window*/Environment 仍走真实实现委托到 window.runtime。
vi.mock("../../wailsjs/runtime/runtime", async () => {
  const actual = await vi.importActual<
    typeof import("../../wailsjs/runtime/runtime")
  >("../../wailsjs/runtime/runtime");
  return {
    ...actual,
    EventsOn: vi.fn(() => () => {}),
    EventsOff: vi.fn(),
  };
});

// 生成的 llm_provider_svc 命名空间在 vitest 的 SSR 变换下只保留了部分
// DTO 类，而其它 domain 命名空间正常。App 层的 LLM Provider 集成场景会
// 构造这些 DTO（例如连接测试请求），因此这里只替换 llm_provider_svc，
// 保持其余命名空间（chat_svc / agent_svc / ...）不变。
vi.mock("../../wailsjs/go/models", async () => {
  const actual = await vi.importActual<
    typeof import("../../wailsjs/go/models")
  >("../../wailsjs/go/models");
  class ModelClass {
    static createFrom(source: Record<string, unknown> = {}) {
      return new ModelClass(source);
    }
    constructor(init?: Record<string, unknown>) {
      if (init) Object.assign(this, init);
    }
  }
  // 任意 llm_provider_svc 类都落到 ModelClass，避免枚举遗漏新 DTO。
  const svc = new Proxy(
    {},
    {
      get: () => ModelClass,
      has: () => true,
    },
  );
  return { ...actual, llm_provider_svc: svc };
});

const themeStorageKey = "agentre.theme";
const windowSizeStorageKey = "agentre.windowSize";
const lastPathStorageKey = "agentre.lastPath";
const themeLabelByValue: Record<"system" | "light" | "dark", string> = {
  dark: "Dark",
  light: "Light",
  system: "System",
};
let restoreMatchMedia: (() => void) | undefined;

async function selectThemeOption(
  user: ReturnType<typeof userEvent.setup>,
  trigger: HTMLElement,
  value: "system" | "light" | "dark",
) {
  await user.click(trigger);
  const option = await screen.findByRole("option", {
    name: themeLabelByValue[value],
  });
  await user.click(option);
}

type MockWailsRuntimeOptions = {
  fullscreen?: boolean;
  platform?: string;
  size?: { h: number; w: number };
};

function fireSelectAllKey(
  target: Document | HTMLElement,
  modifier: "ctrl" | "meta" = "meta",
) {
  const event = createEvent.keyDown(target, {
    bubbles: true,
    cancelable: true,
    code: "KeyA",
    ctrlKey: modifier === "ctrl",
    key: "a",
    metaKey: modifier === "meta",
  });

  Object.defineProperty(event, "ctrlKey", {
    configurable: true,
    value: modifier === "ctrl",
  });
  Object.defineProperty(event, "metaKey", {
    configurable: true,
    value: modifier === "meta",
  });

  fireEvent(target, event);

  return event;
}

function mockSystemColorScheme(initialDark = false) {
  const originalMatchMedia = window.matchMedia;
  const listeners = new Set<EventListenerOrEventListenerObject>();
  const mediaQueryList = {
    matches: initialDark,
    media: "(prefers-color-scheme: dark)",
    onchange: null,
    addEventListener: vi.fn(
      (_event: string, listener: EventListenerOrEventListenerObject) => {
        listeners.add(listener);
      },
    ),
    removeEventListener: vi.fn(
      (_event: string, listener: EventListenerOrEventListenerObject) => {
        listeners.delete(listener);
      },
    ),
    addListener: vi.fn((listener: EventListenerOrEventListenerObject) => {
      listeners.add(listener);
    }),
    removeListener: vi.fn((listener: EventListenerOrEventListenerObject) => {
      listeners.delete(listener);
    }),
    dispatchEvent: vi.fn(() => true),
  } as unknown as MediaQueryList;

  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: vi.fn(() => mediaQueryList),
  });

  restoreMatchMedia = () => {
    if (originalMatchMedia) {
      Object.defineProperty(window, "matchMedia", {
        configurable: true,
        value: originalMatchMedia,
      });
    } else {
      Reflect.deleteProperty(window, "matchMedia");
    }
  };

  return {
    setDark(dark: boolean) {
      Object.defineProperty(mediaQueryList, "matches", {
        configurable: true,
        value: dark,
      });
      const event = {
        matches: dark,
        media: mediaQueryList.media,
      } as MediaQueryListEvent;

      listeners.forEach((listener) => {
        if (typeof listener === "function") {
          listener(event);
        } else {
          listener.handleEvent(event);
        }
      });
    },
  };
}

function mockDesktopViewport() {
  const originalMatchMedia = window.matchMedia;
  const listenersByQuery = new Map<
    string,
    Set<EventListenerOrEventListenerObject>
  >();

  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: vi.fn((query: string) => {
      const listeners = new Set<EventListenerOrEventListenerObject>();
      listenersByQuery.set(query, listeners);

      return {
        matches: query.includes("min-width: 1024px"),
        media: query,
        onchange: null,
        addEventListener: vi.fn(
          (_event: string, listener: EventListenerOrEventListenerObject) => {
            listeners.add(listener);
          },
        ),
        removeEventListener: vi.fn(
          (_event: string, listener: EventListenerOrEventListenerObject) => {
            listeners.delete(listener);
          },
        ),
        addListener: vi.fn((listener: EventListenerOrEventListenerObject) => {
          listeners.add(listener);
        }),
        removeListener: vi.fn(
          (listener: EventListenerOrEventListenerObject) => {
            listeners.delete(listener);
          },
        ),
        dispatchEvent: vi.fn(() => true),
      } as unknown as MediaQueryList;
    }),
  });

  restoreMatchMedia = () => {
    listenersByQuery.clear();

    if (originalMatchMedia) {
      Object.defineProperty(window, "matchMedia", {
        configurable: true,
        value: originalMatchMedia,
      });
    } else {
      Reflect.deleteProperty(window, "matchMedia");
    }
  };
}

function mockLlmProviders() {
  const existing =
    (window as unknown as { go?: { app?: { App?: Record<string, unknown> } } })
      .go?.app?.App ?? {};
  Object.defineProperty(window, "go", {
    configurable: true,
    value: {
      app: {
        App: {
          ...existing,
          ListLLMProviders: vi.fn(() =>
            Promise.resolve({
              items: [
                {
                  baseUrl: "",
                  createtime: 0,
                  defaultModelKey: "mk-sonnet",
                  enabled: true,
                  hasApiKey: true,
                  id: 1,
                  maskedApiKey: "sk-ant-•••••••••••••• xJ12",
                  modelCount: 1,
                  name: "Production",
                  providerKey: "pk-production",
                  type: "anthropic",
                  updatetime: 0,
                },
                {
                  baseUrl: "http://localhost:11434/v1",
                  createtime: 0,
                  defaultModelKey: "",
                  enabled: false,
                  hasApiKey: false,
                  id: 2,
                  maskedApiKey: "",
                  modelCount: 0,
                  name: "Ollama 本机",
                  providerKey: "pk-ollama",
                  type: "openai-chat",
                  updatetime: 0,
                },
              ],
            }),
          ),
          ListLLMModels: vi.fn(() =>
            Promise.resolve({
              items: [
                {
                  contextWindow: 200000,
                  createtime: 0,
                  enabled: true,
                  id: 11,
                  isDefault: true,
                  maxOutput: 64000,
                  modelId: "claude-sonnet-4-6",
                  modelKey: "mk-sonnet",
                  name: "Sonnet",
                  providerId: 1,
                  providerKey: "pk-production",
                  updatetime: 0,
                },
              ],
            }),
          ),
          CreateLLMProvider: vi.fn(() =>
            Promise.resolve({
              item: {
                baseUrl: "https://api.example.com",
                createtime: 0,
                defaultModelKey: "mk-sonnet",
                enabled: false,
                hasApiKey: true,
                id: 99,
                maskedApiKey: "sk-ant-•••••••••••••• xJ12",
                name: "Draft Anthropic",
                providerKey: "pk-99",
                type: "anthropic",
                updatetime: 0,
              },
            }),
          ),
          TestLLMProvider: vi.fn(() =>
            Promise.resolve({
              message: "模型调用成功",
              modelCount: 0,
              ok: true,
            }),
          ),
        },
      },
    },
  });
}

function mockOrgData() {
  const existing =
    (window as unknown as { go?: { app?: { App?: Record<string, unknown> } } })
      .go?.app?.App ?? {};
  Object.defineProperty(window, "go", {
    configurable: true,
    value: {
      app: {
        App: {
          ...existing,
          ListAgentBackends: vi.fn(() => Promise.resolve({ items: [] })),
          LoadOrg: vi.fn(() =>
            Promise.resolve({
              departments: [
                {
                  id: 1,
                  name: "工程部",
                  description: "",
                  icon: "hammer",
                  accentColor: "agent-2",
                  parentId: 0,
                  leadAgentId: 0,
                  leadAgentName: "",
                  sortOrder: 1,
                  directAgentCount: 1,
                  subdepartmentCount: 0,
                  memberCount: 1,
                  createtime: 0,
                  updatetime: 0,
                },
              ],
              agents: [
                {
                  id: 1,
                  name: "CEO 助手",
                  description: "默认入口",
                  avatarColor: "agent-1",
                  avatarDataUrl: "",
                  systemBadge: "DEFAULT",
                  departmentId: 0,
                  departmentName: "",
                  agentBackendId: 0,
                  sortOrder: 0,
                  prompt: [],
                  skills: [],
                  createtime: 0,
                  updatetime: 0,
                },
                {
                  id: 2,
                  name: "Eva",
                  description: "工程总监",
                  avatarColor: "agent-2",
                  avatarDataUrl: "",
                  systemBadge: "",
                  departmentId: 1,
                  departmentName: "工程部",
                  agentBackendId: 0,
                  sortOrder: 1,
                  prompt: [],
                  skills: [],
                  createtime: 0,
                  updatetime: 0,
                },
              ],
            }),
          ),
        },
      },
    },
  });
}

function mockAgentBackends() {
  const existing =
    (window as unknown as { go?: { app?: { App?: Record<string, unknown> } } })
      .go?.app?.App ?? {};
  Object.defineProperty(window, "go", {
    configurable: true,
    value: {
      app: {
        App: {
          ...existing,
          ListAgentBackends: vi.fn(() =>
            Promise.resolve({
              items: [
                {
                  id: 1,
                  type: "builtin",
                  name: "默认助手",
                  llmProviderId: 1,
                  llmProviderName: "Anthropic",
                  llmProviderType: "anthropic",
                  llmProviderModel: "sonnet-4-6",
                  llmProviderActive: true,
                  cliPath: "",
                  createtime: 0,
                  updatetime: 0,
                },
                {
                  id: 2,
                  type: "builtin",
                  name: "AWS Bedrock",
                  llmProviderId: 2,
                  llmProviderName: "AWS Bedrock",
                  llmProviderType: "anthropic",
                  llmProviderModel: "sonnet-4-6",
                  llmProviderActive: true,
                  cliPath: "",
                  createtime: 0,
                  updatetime: 0,
                },
              ],
            }),
          ),
        },
      },
    },
  });
}

function mockHooks() {
  const existing =
    (window as unknown as { go?: { app?: { App?: Record<string, unknown> } } })
      .go?.app?.App ?? {};
  const hookA = {
    id: 2,
    name: "Jira urgent",
    interpreter: "bash",
    command: "echo '{\"events\":[]}'",
    scheduleExpr: "*/5 * * * *",
    timezone: "Asia/Shanghai",
    env: [{ key: "JIRA_TOKEN", value: "********", secret: true }],
    enabled: true,
    nextRunAt: 0,
    lastRunAt: 1778934000,
    lastStatus: "ok",
    lastError: "",
    lastDurationMs: 412,
    totalCount: 37,
    createtime: 0,
    updatetime: 0,
  };
  const hookB = {
    ...hookA,
    id: 3,
    name: "RSS advisories",
    interpreter: "node",
    enabled: false,
    lastStatus: "",
  };
  const event = {
    id: 100,
    hookId: 2,
    title: "payment callback timeout",
    dedupeKey: "OPS-4821",
    payloadJson: '{"severity":"high"}',
    receivedAt: 1778934120,
    createtime: 0,
  };
  const app = {
    ...existing,
    LoadHooks: vi.fn(() =>
      Promise.resolve({ hooks: [hookA, hookB], events: [event] }),
    ),
    CreateHook: vi.fn((req) => Promise.resolve({ ...hookA, id: 9, ...req })),
    UpdateHook: vi.fn((req) => Promise.resolve({ ...hookA, ...req })),
    DeleteHook: vi.fn(() => Promise.resolve()),
    ToggleHook: vi.fn((id, enabled) =>
      Promise.resolve({ ...hookA, id, enabled }),
    ),
    RunHook: vi.fn(() =>
      Promise.resolve({
        exitCode: 0,
        durationMs: 412,
        timedOut: false,
        stdout: "{}",
        stderr: "",
        parseError: "",
        events: [event],
        newCount: 1,
        dupCount: 1,
        persisted: false,
      }),
    ),
    ProbeInterpreters: vi.fn(() =>
      Promise.resolve([
        { key: "bash", path: "/bin/bash", installed: true },
        { key: "node", path: "/usr/bin/node", installed: true },
        { key: "python", path: "/usr/bin/python3", installed: true },
        { key: "pwsh", path: "", installed: false },
      ]),
    ),
  };
  Object.defineProperty(window, "go", {
    configurable: true,
    value: { app: { App: app } },
  });
  return app;
}

// runtimeEventStubs supplies no-op EventsOn/EventsOff so always-mounted
// subscribers (e.g. QuitConfirmDialog's "app:quit-blocked" listener) don't blow
// up on window.runtime.EventsOnMultiple when <App/> renders without a full runtime.
function runtimeEventStubs() {
  return {
    EventsOn: vi.fn(() => vi.fn()),
    EventsOnMultiple: vi.fn(() => vi.fn()),
    EventsOff: vi.fn(),
    EventsEmit: vi.fn(),
  };
}

function mockWailsRuntime({
  fullscreen = false,
  platform = "darwin",
  size = { h: 768, w: 1024 },
}: MockWailsRuntimeOptions = {}) {
  const runtime = {
    ...runtimeEventStubs(),
    Environment: vi.fn(() =>
      Promise.resolve({
        arch: "arm64",
        buildType: "dev",
        platform,
      }),
    ),
    WindowGetSize: vi.fn(() => Promise.resolve(size)),
    WindowCenter: vi.fn(),
    WindowIsFullscreen: vi.fn(() => Promise.resolve(fullscreen)),
    WindowSetSize: vi.fn(),
    WindowShow: vi.fn(),
  };

  Object.defineProperty(window, "runtime", {
    configurable: true,
    value: runtime,
  });

  return runtime;
}

beforeEach(async () => {
  const info = vi.mocked((await import("../../wailsjs/go/app/App")).Info);
  info.mockReset();
  info.mockResolvedValue({
    name: "agentre",
    version: "dev",
    commit: "dev",
    env: "test",
    runtimeMode: "interactive",
  });
  // Baseline full runtime so <App/> startup (Environment/Window*) and the
  // always-mounted QuitConfirmDialog's "app:quit-blocked" subscription both
  // work; individual tests still override via mockWailsRuntime as needed.
  mockWailsRuntime();
});

afterEach(() => {
  restoreMatchMedia?.();
  restoreMatchMedia = undefined;
  Reflect.deleteProperty(window, "go");
  Reflect.deleteProperty(window, "runtime");
  vi.useRealTimers();
});

describe("App", () => {
  it("boots into the chat page and surfaces settings from the rail", async () => {
    const user = userEvent.setup();

    render(<App />);

    expect(screen.getByText("Agentre")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Chat" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    expect(
      screen.getByRole("button", { name: "Settings" }),
    ).not.toHaveAttribute("aria-current");

    await user.click(screen.getByRole("button", { name: "Settings" }));

    expect(screen.getByRole("button", { name: "Settings" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    expect(
      screen.getByRole("heading", { name: "Appearance" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Appearance" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    expect(
      screen.getByRole("complementary", { name: "Settings" }),
    ).not.toHaveClass("hidden");
    expect(
      screen.getByRole("combobox", { name: "Theme Mode" }),
    ).toBeInTheDocument();
  });

  it("restores the last opened page from localStorage on startup", async () => {
    localStorage.setItem(lastPathStorageKey, "/issues");

    render(<App />);

    await waitFor(() => {
      expect(screen.getByRole("button", { name: "Issues" })).toHaveAttribute(
        "aria-current",
        "page",
      );
    });
    expect(screen.getByRole("button", { name: "Chat" })).not.toHaveAttribute(
      "aria-current",
    );
  });

  it("Given a stored /projects path, When the app starts, Then it lands on the merged index instead of a dead route", () => {
    // 「项目」不再是一个导航项 —— 它退化成会话索引的一个分组维度（规格决策 1）。
    // 老用户 localStorage 里存着 /projects，重定向必须把他们带到 /chat。
    localStorage.setItem(lastPathStorageKey, "/projects");

    render(<App />);

    expect(screen.getByRole("button", { name: "Chat" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    expect(
      screen.queryByRole("button", { name: "Projects" }),
    ).not.toBeInTheDocument();
  });

  it("falls back to the chat page when the stored last path is unknown", async () => {
    localStorage.setItem(lastPathStorageKey, "/does-not-exist");

    render(<App />);

    await waitFor(() => {
      expect(screen.getByRole("button", { name: "Chat" })).toHaveAttribute(
        "aria-current",
        "page",
      );
    });
  });

  it("persists the current page to localStorage when navigating", async () => {
    const user = userEvent.setup();

    render(<App />);

    await user.click(screen.getByRole("button", { name: "Settings" }));

    await waitFor(() => {
      expect(localStorage.getItem(lastPathStorageKey)).toBe("/settings");
    });

    await user.click(screen.getByRole("button", { name: "Chat" }));

    await waitFor(() => {
      expect(localStorage.getItem(lastPathStorageKey)).toBe("/chat");
    });
  });

  it("keeps the theme toggle directly above settings in the left rail", () => {
    render(<App />);

    const navRail = screen.getByRole("complementary", {
      name: "Primary navigation",
    });
    const settingsButton = within(navRail).getByRole("button", {
      name: "Settings",
    });
    const themeToggle = within(navRail).getByRole("button", {
      name: /Toggle theme/,
    });

    expect(navRail).toHaveClass("w-14", "px-2");
    expect(settingsButton).toHaveClass("size-10");
    expect(themeToggle).toHaveClass("size-10");
    expect(themeToggle).toHaveClass("mt-auto");
    expect(settingsButton).not.toHaveClass("mt-auto");
    expect(Array.from(navRail.children).at(-2)).toBe(themeToggle);
    expect(Array.from(navRail.children).at(-1)).toBe(settingsButton);
  });

  it("places organization after issues in the left rail workflow group", () => {
    render(<App />);

    const navRail = screen.getByRole("complementary", {
      name: "Primary navigation",
    });
    const labels = within(navRail)
      .getAllByRole("button")
      .map((button) => button.getAttribute("aria-label"));

    expect(labels.slice(0, 4)).toEqual([
      "Chat",
      "Issues",
      "Organization",
      "Hooks",
    ]);
  });

  it("restores the saved Wails window size before showing the hidden startup window", async () => {
    const runtime = mockWailsRuntime();

    localStorage.setItem(
      windowSizeStorageKey,
      JSON.stringify({ height: 720, width: 1120 }),
    );

    render(<App />);

    await waitFor(() => {
      expect(runtime.WindowSetSize).toHaveBeenCalledWith(1120, 720);
      expect(runtime.WindowCenter).toHaveBeenCalled();
      expect(runtime.WindowShow).toHaveBeenCalled();
    });

    expect(runtime.WindowSetSize.mock.invocationCallOrder[0]).toBeLessThan(
      runtime.WindowCenter.mock.invocationCallOrder[0],
    );
    expect(runtime.WindowCenter.mock.invocationCallOrder[0]).toBeLessThan(
      runtime.WindowShow.mock.invocationCallOrder[0],
    );
  });

  it.each(["headless", "unknown"])(
    "disables every native window API when runtime mode is %s",
    async (runtimeMode) => {
      const runtime = mockWailsRuntime();
      const info = vi.mocked((await import("../../wailsjs/go/app/App")).Info);
      info.mockResolvedValueOnce({
        name: "agentre",
        version: "dev",
        commit: "dev",
        env: "test",
        runtimeMode,
      });

      localStorage.setItem(
        windowSizeStorageKey,
        JSON.stringify({ height: 720, width: 1120 }),
      );
      render(<App />);
      fireEvent(window, new Event("resize"));
      fireEvent(window, new Event("beforeunload"));
      fireEvent(window, new Event("pagehide"));

      await waitFor(() => {
        expect(info).toHaveBeenCalled();
      });
      expect(runtime.WindowSetSize).not.toHaveBeenCalled();
      expect(runtime.WindowCenter).not.toHaveBeenCalled();
      expect(runtime.WindowShow).not.toHaveBeenCalled();
      expect(runtime.WindowIsFullscreen).not.toHaveBeenCalled();
      expect(runtime.WindowGetSize).not.toHaveBeenCalled();
    },
  );

  it("stores the normal Wails window size after resize", async () => {
    const runtime = mockWailsRuntime({ size: { h: 760, w: 1180 } });

    render(<App />);

    await waitFor(() => {
      expect(runtime.WindowShow).toHaveBeenCalled();
    });
    fireEvent(window, new Event("resize"));

    await waitFor(() => {
      expect(runtime.WindowGetSize).toHaveBeenCalled();
      expect(localStorage.getItem(windowSizeStorageKey)).toBe(
        JSON.stringify({ height: 760, width: 1180 }),
      );
    });
  });

  it("stores the current Wails window size after maximized resize", async () => {
    const runtime = mockWailsRuntime({
      size: { h: 900, w: 1600 },
    });

    localStorage.setItem(
      windowSizeStorageKey,
      JSON.stringify({ height: 760, width: 1180 }),
    );

    render(<App />);

    await waitFor(() => {
      expect(runtime.WindowShow).toHaveBeenCalled();
    });
    fireEvent(window, new Event("resize"));

    await waitFor(() => {
      expect(runtime.WindowGetSize).toHaveBeenCalled();
      expect(localStorage.getItem(windowSizeStorageKey)).toBe(
        JSON.stringify({ height: 900, width: 1600 }),
      );
    });
  });

  it("does not overwrite the saved window size while fullscreen", async () => {
    const runtime = mockWailsRuntime({
      fullscreen: true,
      size: { h: 900, w: 1600 },
    });

    localStorage.setItem(
      windowSizeStorageKey,
      JSON.stringify({ height: 760, width: 1180 }),
    );

    render(<App />);

    await waitFor(() => {
      expect(runtime.WindowShow).toHaveBeenCalled();
    });
    fireEvent(window, new Event("resize"));

    await waitFor(() => {
      expect(runtime.WindowIsFullscreen).toHaveBeenCalled();
      expect(runtime.WindowGetSize).not.toHaveBeenCalled();
      expect(localStorage.getItem(windowSizeStorageKey)).toBe(
        JSON.stringify({ height: 760, width: 1180 }),
      );
    });
  });

  it("uses Command for global select-all on darwin while preserving editable fields", async () => {
    const user = userEvent.setup();
    const runtime = mockWailsRuntime({ platform: "darwin" });

    render(<App />);

    await waitFor(() => {
      expect(runtime.Environment).toHaveBeenCalled();
      expect(screen.getByText("⌘P")).toBeInTheDocument();
    });

    const appChrome = screen.getByText("Agentre").closest("header");

    if (!(appChrome instanceof HTMLElement)) {
      throw new Error("Expected Agentre to render inside the app chrome");
    }

    const ctrlEvent = fireSelectAllKey(appChrome, "ctrl");
    const metaEvent = fireSelectAllKey(appChrome, "meta");

    expect(ctrlEvent.defaultPrevented).toBe(false);
    expect(metaEvent.defaultPrevented).toBe(true);

    await user.click(screen.getByRole("button", { name: "Chat" }));

    const textareaEvent = fireSelectAllKey(
      screen.getByPlaceholderText("Search sessions, projects, agents"),
      "meta",
    );

    expect(textareaEvent.defaultPrevented).toBe(false);
  });

  it("uses Ctrl for global select-all on windows", async () => {
    const runtime = mockWailsRuntime({ platform: "windows" });

    render(<App />);

    await waitFor(() => {
      expect(runtime.Environment).toHaveBeenCalled();
    });

    const appChrome = screen.getByText("Agentre").closest("header");

    if (!(appChrome instanceof HTMLElement)) {
      throw new Error("Expected Agentre to render inside the app chrome");
    }

    const metaEvent = fireSelectAllKey(appChrome, "meta");
    const ctrlEvent = fireSelectAllKey(appChrome, "ctrl");

    expect(metaEvent.defaultPrevented).toBe(false);
    expect(ctrlEvent.defaultPrevented).toBe(true);
  });

  it("switches between implemented pages from the left rail", async () => {
    const user = userEvent.setup();

    render(<App />);

    await user.click(screen.getByRole("button", { name: "Chat" }));

    expect(screen.getByRole("button", { name: "Chat" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    expect(
      screen.getByRole("button", { name: "Settings" }),
    ).not.toHaveAttribute("aria-current");
    expect(
      screen.getByRole("complementary", { name: "Session index" }),
    ).toHaveStyle({ width: "320px" });
    expect(
      screen.getByPlaceholderText("Search sessions, projects, agents"),
    ).toBeInTheDocument();
    // 空聊天态: 测试环境没有可对话 Agent (ListChatAgents 未 mock, agents=[]),
    // 因此显示 spec §7 组 1B 的两步配置引导空态而非旧占位。
    expect(
      screen.getByText("Before you start, complete two setup steps"),
    ).toBeInTheDocument();
    // TabStrip + ChatPanelHost right pane is visible on /chat
    expect(
      document.querySelector('[data-page-has-chat="true"]'),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { name: "Agent Backends" }),
    ).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Settings" }));

    expect(screen.getByRole("button", { name: "Settings" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    expect(screen.getByRole("button", { name: "Chat" })).not.toHaveAttribute(
      "aria-current",
    );
    expect(
      screen.getByRole("heading", { name: "Appearance" }),
    ).toBeInTheDocument();
  });

  it("opens the implemented Issues workspace from the left rail", async () => {
    const user = userEvent.setup();

    render(<App />);

    await user.click(screen.getByRole("button", { name: "Issues" }));

    const main = screen.getByRole("main");

    expect(screen.getByRole("button", { name: "Issues" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    // Real data layer: the default IssueList mock returns no issues, so the
    // workspace renders its empty state rather than the old static placeholder.
    expect(
      await within(main).findByRole("heading", { name: "No issues yet" }),
    ).toBeInTheDocument();
    expect(within(main).getByText("0 open · 0 closed")).toBeInTheDocument();
    expect(
      within(main).getAllByRole("button", { name: "New issue" }).length,
    ).toBeGreaterThan(0);
    expect(
      within(main).queryByText("Under construction"),
    ).not.toBeInTheDocument();
  });

  it("opens the script-driven Hooks workspace from the left rail", async () => {
    const user = userEvent.setup();
    mockHooks();

    render(<App />);

    await user.click(screen.getByRole("button", { name: "Hooks" }));

    expect(screen.getByRole("button", { name: "Hooks" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    expect((await screen.findAllByText("Jira urgent")).length).toBeGreaterThan(
      0,
    );
    expect(screen.getByText("RSS advisories")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Script" })).toBeInTheDocument();
    expect(screen.queryByText("Under construction")).not.toBeInTheDocument();
  });

  it("loads and lists departments + agents on the organization page", async () => {
    const user = userEvent.setup();
    mockOrgData();
    render(<App />);

    await user.click(screen.getByRole("button", { name: "Organization" }));

    // wait for LoadOrg promise to resolve
    await waitFor(() => {
      expect(
        screen.queryByText("Loading org chart..."),
      ).not.toBeInTheDocument();
    });

    // department row from the mock
    expect(screen.getByText("工程部")).toBeInTheDocument();
    // CEO + Eva rows
    expect(screen.getByText("CEO 助手")).toBeInTheDocument();
    expect(screen.getByText("Eva")).toBeInTheDocument();
  });

  it("uses the shared dialog shell for organization create dialogs", async () => {
    const user = userEvent.setup();
    mockOrgData();
    render(<App />);

    await user.click(screen.getByRole("button", { name: "Organization" }));
    await waitFor(() => {
      expect(
        screen.queryByText("Loading org chart..."),
      ).not.toBeInTheDocument();
    });

    await user.click(screen.getByRole("button", { name: "New Department" }));

    let dialog = await screen.findByRole("dialog");
    let body = within(dialog)
      .getByLabelText("Name")
      .closest("[data-slot='dialog-body']");
    let footer = dialog.querySelector("[data-slot='dialog-footer']");

    expect(body).toHaveClass("px-5", "py-4");
    expect(footer).toHaveClass("border-t", "border-border");

    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });

    await user.click(screen.getByRole("button", { name: "New Agent" }));

    dialog = await screen.findByRole("dialog");
    body = within(dialog)
      .getByLabelText("Name")
      .closest("[data-slot='dialog-body']");
    footer = dialog.querySelector("[data-slot='dialog-footer']");

    expect(body).toHaveClass("px-5", "py-4");
    expect(footer).toHaveClass("border-t", "border-border");
  });

  it("opens detail panel when selecting an agent", async () => {
    const user = userEvent.setup();
    mockOrgData();
    render(<App />);

    await user.click(screen.getByRole("button", { name: "Organization" }));
    await waitFor(() => {
      expect(
        screen.queryByText("Loading org chart..."),
      ).not.toBeInTheDocument();
    });

    // initial state: empty detail panel
    expect(
      screen.getByText("Select a department or agent to view details"),
    ).toBeInTheDocument();

    // click Eva row
    const evaRow = screen.getByText("Eva").closest("button");
    if (!evaRow) throw new Error("Eva row not found");
    await user.click(evaRow);

    // agent detail rendered — description field carries the editable label
    const descInput = await screen.findByDisplayValue("工程总监");
    expect(descInput).toBeInTheDocument();
  });

  // 画布与列表两个视图收敛成一套索引后，`agentre.orgView.mode` 作废：存量里的旧值
  // 只应被忽略 —— 既不报错、不改变渲染，也不顺手清掉其他状态。
  it("ignores the retired organization view-mode value and keeps the same fixed detail panel", async () => {
    const user = userEvent.setup();
    localStorage.setItem("agentre.orgView.mode", "list");
    localStorage.setItem("agentre.orgTree.collapse", '{"1":true}');
    mockOrgData();
    const { container } = render(<App />);

    await user.click(screen.getByRole("button", { name: "Organization" }));
    await waitFor(() => {
      expect(
        screen.queryByText("Loading org chart..."),
      ).not.toBeInTheDocument();
    });

    const detailPanel = container.querySelector(
      '[data-slot="org-detail-panel"]',
    );
    expect(detailPanel).toBeInTheDocument();
    expect(detailPanel).toHaveClass("w-[380px]", "shrink-0", "border-l");
    expect(
      container.querySelector('[data-slot="org-detail-drawer"]'),
    ).toBeNull();
    expect(
      within(detailPanel as HTMLElement).getByText(
        "Select a department or agent to view details",
      ),
    ).toBeInTheDocument();

    // 旧值不影响渲染：只有一套索引，没有视图切换开关
    expect(screen.queryByRole("button", { name: "List view" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Tree view" })).toBeNull();
    expect(container.querySelector('[data-slot="org-index"]')).not.toBeNull();
    // 也没有被顺手抹掉，更没有连累同期存下来的选中态
    expect(localStorage.getItem("agentre.orgView.mode")).toBe("list");
    expect(localStorage.getItem("agentre.orgTree.collapse")).toBe('{"1":true}');

    const evaRow = screen.getByText("Eva").closest("button");
    if (!evaRow) throw new Error("Eva row not found");
    await user.click(evaRow);

    expect(await screen.findByDisplayValue("工程总监")).toBeInTheDocument();
    expect(
      container.querySelector('[data-slot="org-detail-drawer"]'),
    ).toBeNull();
  });

  it("renders only backend management on the Agent backend page", async () => {
    const user = userEvent.setup();

    mockAgentBackends();
    mockLlmProviders();
    render(<App />);

    await user.click(screen.getByRole("button", { name: "Settings" }));
    await user.click(screen.getByRole("button", { name: "Agent Backends" }));

    const backendList = await screen.findByRole("list", {
      name: "Agent backend list",
    });

    // 新版列表不再横向滚动：无 table-container 包裹，自身也不 overflow-x-auto。
    expect(backendList.closest("[data-slot='table-container']")).toBeNull();
    expect(backendList).not.toHaveClass("overflow-x-auto");
    await waitFor(() => {
      expect(within(backendList).getByText("默认助手")).toBeInTheDocument();
      // 后端名与绑定摘要里的供应商名都可能等于 "AWS Bedrock"，用 getAllByText 断言存在。
      expect(
        within(backendList).getAllByText("AWS Bedrock").length,
      ).toBeGreaterThanOrEqual(1);
      // 绑定摘要把供应商名与模型 ID 拆成独立 span，分别断言；"sonnet-4-6" 两个后端都有。
      expect(within(backendList).getByText("Anthropic")).toBeInTheDocument();
      expect(
        within(backendList).getAllByText("sonnet-4-6").length,
      ).toBeGreaterThanOrEqual(1);
    });

    expect(
      screen.queryByRole("table", { name: "LLM provider list" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "New Provider" }),
    ).not.toBeInTheDocument();
  });

  it("marks copyable app content as selectable text", async () => {
    const user = userEvent.setup();

    render(<App />);

    mockAgentBackends();
    mockLlmProviders();

    await user.click(screen.getByRole("button", { name: "Settings" }));
    await user.click(screen.getByRole("button", { name: "Agent Backends" }));

    const backendList = await screen.findByRole("list", {
      name: "Agent backend list",
    });
    await waitFor(() => {
      expect(within(backendList).getByText("默认助手")).toBeInTheDocument();
    });

    expect(
      within(backendList)
        .getByText("默认助手")
        .closest("[data-selectable-text='true']"),
    ).toBeInTheDocument();
  });

  it("shows provider management after selecting LLM providers", async () => {
    const user = userEvent.setup();

    mockLlmProviders();
    render(<App />);

    await user.click(screen.getByRole("button", { name: "Settings" }));
    await user.click(screen.getByRole("button", { name: "LLM Providers" }));

    // 供应商导航按类型分组，展示端点 + 启用状态
    const nav = await screen.findByRole("complementary", {
      name: "Provider list",
    });
    const production = await within(nav).findByRole("button", {
      name: /Production/,
    });
    expect(
      within(production).getByText(/https:\/\/api\.anthropic\.com/),
    ).toBeInTheDocument();
    expect(within(production).getByText(/1 models/)).toBeInTheDocument();
    expect(within(production).queryByText("Enabled")).not.toBeInTheDocument();
    const ollama = within(nav).getByRole("button", { name: /Ollama 本机/ });
    expect(
      within(ollama).getByText(/http:\/\/localhost:11434\/v1/),
    ).toBeInTheDocument();
    // 停用的供应商只出 Disabled 徽标，不再同时挂模型计数。
    expect(within(ollama).queryByText(/0 models/)).not.toBeInTheDocument();
    expect(within(ollama).getByText("Disabled")).toBeInTheDocument();

    expect(
      screen.getByRole("button", { name: "LLM Providers" }),
    ).toHaveAttribute("aria-current", "page");
    expect(
      screen.getByRole("button", { name: "Agent Backends" }),
    ).not.toHaveAttribute("aria-current");
    expect(
      screen.queryByRole("list", { name: "Agent backend list" }),
    ).not.toBeInTheDocument();

    // 选中供应商的工作区展示连接配置 + 默认模型行
    const workspace = await screen.findByRole("region", {
      name: /Production models/,
    });
    expect(
      within(workspace).getByText("https://api.anthropic.com"),
    ).toBeInTheDocument();
    expect(
      within(workspace).getByText("sk-ant-•••••••••••••• xJ12"),
    ).toBeInTheDocument();
    expect(
      within(workspace).getAllByText("claude-sonnet-4-6").length,
    ).toBeGreaterThanOrEqual(1);
    // 模型行主行显示 display name，modelKey 已移入编辑弹窗不再出现在行内
    expect(within(workspace).getByText("Sonnet")).toBeInTheDocument();
    expect(within(workspace).queryByText("mk-sonnet")).not.toBeInTheDocument();

    // mockup 注解①：唯一的「新增供应商」入口落在 H1 页头行内，不再单独占一层 strip
    const pageHeader = screen
      .getByRole("heading", { level: 1, name: "LLM Providers" })
      .closest('[data-slot="settings-page-header"]');
    expect(pageHeader).not.toBeNull();
    expect(
      within(pageHeader as HTMLElement).getByRole("button", {
        name: "New Provider",
      }),
    ).toBeInTheDocument();
    expect(
      screen.getAllByRole("button", { name: "New Provider" }),
    ).toHaveLength(1);
  });

  it("tests an LLM provider by calling the configured model", async () => {
    const user = userEvent.setup();

    mockLlmProviders();
    render(<App />);

    await user.click(screen.getByRole("button", { name: "Settings" }));
    await user.click(screen.getByRole("button", { name: "LLM Providers" }));

    const workspace = await screen.findByRole("region", {
      name: /Production models/,
    });

    await user.click(
      within(workspace).getByRole("button", { name: "Test Production" }),
    );

    const appBridge = (
      window as unknown as {
        go?: { app?: { App?: Record<string, ReturnType<typeof vi.fn>> } };
      }
    ).go?.app?.App;

    // 空 modelKey → 测试 Provider 当前默认模型
    expect(appBridge?.TestLLMProvider).toHaveBeenCalledWith(
      expect.objectContaining({ id: 1, modelKey: "" }),
    );
    expect(
      await screen.findByText(/"Production" call succeeded \(\d+ms\)/),
    ).toBeInTheDocument();
  });

  it("opens under construction pages from unimplemented settings items", async () => {
    const user = userEvent.setup();
    const unimplementedSettingsItems = ["MCP Servers", "Skills / Tools"];

    mockDesktopViewport();
    render(<App />);

    await user.click(screen.getByRole("button", { name: "Settings" }));

    const settingsNav = screen.getByRole("complementary", {
      name: "Settings",
    });

    for (const label of unimplementedSettingsItems) {
      await user.click(
        within(settingsNav).getByRole("button", { name: label }),
      );

      const main = screen.getByRole("main");

      expect(
        within(settingsNav).getByRole("button", { name: label }),
      ).toHaveAttribute("aria-current", "page");
      expect(
        within(main).getByRole("heading", { name: label }),
      ).toBeInTheDocument();
      expect(within(main).getByText("Under construction")).toBeInTheDocument();
      expect(
        within(main).queryByRole("combobox", { name: "Theme Mode" }),
      ).not.toBeInTheDocument();
    }
  });

  it("opens data-backup panel from settings nav", async () => {
    const user = userEvent.setup();

    mockDesktopViewport();
    render(<App />);

    await user.click(screen.getByRole("button", { name: "Settings" }));

    const settingsNav = screen.getByRole("complementary", {
      name: "Settings",
    });

    await user.click(
      within(settingsNav).getByRole("button", { name: "Data & Backup" }),
    );

    const main = screen.getByRole("main");

    expect(
      within(settingsNav).getByRole("button", { name: "Data & Backup" }),
    ).toHaveAttribute("aria-current", "page");
    expect(
      within(main).getByRole("heading", { name: "Data & Backup" }),
    ).toBeInTheDocument();
    expect(
      within(main).queryByText("Under construction"),
    ).not.toBeInTheDocument();
  });

  it("restores the saved dark theme before user interaction", async () => {
    const user = userEvent.setup();
    localStorage.setItem(themeStorageKey, "dark");

    render(<App />);

    expect(document.documentElement).toHaveClass("dark");

    await user.click(screen.getByRole("button", { name: "Settings" }));

    const settingsMain = screen.getByRole("main");

    expect(
      within(settingsMain).getByRole("combobox", { name: "Theme Mode" }),
    ).toHaveTextContent("Dark");
  });

  it("selects manual light and dark themes from settings appearance", async () => {
    const user = userEvent.setup();

    localStorage.setItem(themeStorageKey, "light");

    render(<App />);

    await user.click(screen.getByRole("button", { name: "Settings" }));

    const settingsMain = screen.getByRole("main");
    const navRail = screen.getByRole("complementary", {
      name: "Primary navigation",
    });
    const topBar = screen.getByRole("banner");
    const themeSelect = within(settingsMain).getByRole("combobox", {
      name: "Theme Mode",
    });

    expect(document.documentElement).not.toHaveClass("dark");
    expect(
      within(topBar).queryByRole("combobox", { name: "Theme Mode" }),
    ).not.toBeInTheDocument();
    expect(
      within(topBar).queryByRole("button", { name: /Toggle theme/ }),
    ).not.toBeInTheDocument();
    expect(
      within(navRail).getByRole("button", { name: /Toggle theme/ }),
    ).toBeInTheDocument();
    expect(themeSelect).toHaveTextContent("Light");

    await selectThemeOption(user, themeSelect, "dark");

    expect(document.documentElement).toHaveClass("dark");
    expect(localStorage.getItem(themeStorageKey)).toBe("dark");
    expect(themeSelect).toHaveTextContent("Dark");

    await selectThemeOption(user, themeSelect, "light");

    expect(document.documentElement).not.toHaveClass("dark");
    expect(localStorage.getItem(themeStorageKey)).toBe("light");
    expect(themeSelect).toHaveTextContent("Light");
  });

  it("follows the saved system theme and reacts to system color-scheme changes", async () => {
    const user = userEvent.setup();
    const systemColorScheme = mockSystemColorScheme(false);
    localStorage.setItem(themeStorageKey, "system");

    render(<App />);

    await user.click(screen.getByRole("button", { name: "Settings" }));

    const settingsMain = screen.getByRole("main");
    const themeSelect = within(settingsMain).getByRole("combobox", {
      name: "Theme Mode",
    });

    expect(document.documentElement).not.toHaveClass("dark");
    expect(themeSelect).toHaveTextContent("System");

    act(() => {
      systemColorScheme.setDark(true);
    });

    expect(document.documentElement).toHaveClass("dark");
    expect(localStorage.getItem(themeStorageKey)).toBe("system");
    expect(themeSelect).toHaveTextContent("System");
  });

  it("can switch between following the system and manual preferences", async () => {
    const user = userEvent.setup();
    mockSystemColorScheme(true);
    localStorage.setItem(themeStorageKey, "light");

    render(<App />);

    await user.click(screen.getByRole("button", { name: "Settings" }));

    const settingsMain = screen.getByRole("main");
    const themeSelect = within(settingsMain).getByRole("combobox", {
      name: "Theme Mode",
    });

    expect(document.documentElement).not.toHaveClass("dark");
    expect(themeSelect).toHaveTextContent("Light");

    await selectThemeOption(user, themeSelect, "system");

    expect(document.documentElement).toHaveClass("dark");
    expect(localStorage.getItem(themeStorageKey)).toBe("system");
    expect(themeSelect).toHaveTextContent("System");

    await selectThemeOption(user, themeSelect, "light");

    expect(document.documentElement).not.toHaveClass("dark");
    expect(localStorage.getItem(themeStorageKey)).toBe("light");
    expect(themeSelect).toHaveTextContent("Light");
  });
});
