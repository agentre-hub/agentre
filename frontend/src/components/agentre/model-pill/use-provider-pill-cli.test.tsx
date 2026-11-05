import {
  act,
  render,
  renderHook,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import "@/i18n";

const appMocks = vi.hoisted(() => ({
  ListLLMProviders: vi.fn(),
  ListLLMModels: vi.fn(),
  ListCLIModels: vi.fn(),
  SetChatSessionCLIModel: vi.fn(),
  SetChatSessionModelTarget: vi.fn(),
  RemoteDeviceFingerprint: vi.fn(),
  RemoteDeviceList: vi.fn(),
  RemoteDeviceListProviders: vi.fn(),
  RemoteDeviceSyncProvider: vi.fn(),
}));

vi.mock("../../../../wailsjs/go/app/App", () => appMocks);

import { ProviderPill, useProviderPill } from "../model-pill";
import type { UseProviderPillOptions } from "../model-pill";

function Harness(props: UseProviderPillOptions) {
  const pill = useProviderPill(props);
  return <ProviderPill {...pill} />;
}

beforeEach(() => {
  vi.clearAllMocks();
  appMocks.ListLLMProviders.mockResolvedValue({ items: [] });
  appMocks.ListLLMModels.mockResolvedValue({ items: [] });
  appMocks.RemoteDeviceFingerprint.mockResolvedValue("sha256:local-desktop");
  appMocks.RemoteDeviceList.mockResolvedValue([]);
  appMocks.RemoteDeviceListProviders.mockResolvedValue([]);
  appMocks.SetChatSessionCLIModel.mockResolvedValue({});
});

const nativeSession = {
  boundProviderKey: "",
  boundModelKey: "",
  backendId: 9,
  boundCLIModel: "",
  sessionId: 42,
  persistedProviderKey: "",
  persistedModelKey: "",
  onSwitched: () => {},
} satisfies Partial<UseProviderPillOptions>;

describe("ProviderPill · CLI 自带模型覆盖三种 CLI 与远端执行", () => {
  it.each([
    ["codex", "Codex", "gpt-6-luna", "GPT-6 Luna"],
    ["piagent", "Pi Agent", "anthropic/claude-sonnet", "Claude Sonnet"],
  ])(
    "Given a local %s session on its CLI login, When the pill opens, Then the CLI's own models are offered and the follow-agent line names that CLI",
    async (backendType, cliName, value, label) => {
      appMocks.ListCLIModels.mockResolvedValue({
        models: [{ value, displayName: label, modelId: value }],
        effectiveValue: value,
        effectiveModelId: value,
      });
      const user = userEvent.setup();
      render(
        <Harness
          {...nativeSession}
          backendType={backendType}
          executionLocation=""
        />,
      );
      await user.click(screen.getByTestId("provider-pill"));
      const popover = await screen.findByTestId("model-target-popover");
      expect(
        await within(popover).findByText(
          `${cliName} built-in · CLI login account`,
        ),
      ).toBeInTheDocument();
      expect(
        within(popover).getByRole("option", { name: new RegExp(`^${label}`) }),
      ).toBeInTheDocument();
      expect(
        within(popover).getByTestId("special-resolution"),
      ).toHaveTextContent(`${cliName} · ${label}`);
      expect(appMocks.ListCLIModels).toHaveBeenCalledWith({
        backendId: 9,
        refresh: false,
      });
    },
  );

  it("Given a remote Claude Code session on the CLI login, When the pill opens, Then the CLI segment comes from that backend's device and the popover says where it runs", async () => {
    appMocks.ListCLIModels.mockResolvedValue({
      models: [
        { value: "sonnet", displayName: "Sonnet", modelId: "claude-sonnet" },
      ],
      effectiveValue: "sonnet",
      effectiveModelId: "claude-sonnet",
    });
    const user = userEvent.setup();
    render(
      <Harness
        {...nativeSession}
        backendType="claudecode"
        executionLocation="sha256:remote-box"
        deviceLabel="build-box"
      />,
    );
    await user.click(screen.getByTestId("provider-pill"));
    const popover = await screen.findByTestId("model-target-popover");
    expect(
      await within(popover).findByText(
        "Claude Code built-in · CLI login account",
      ),
    ).toBeInTheDocument();
    expect(
      within(popover).getByTestId("remote-device-header"),
    ).toHaveTextContent("build-box");
    await user.click(within(popover).getByRole("option", { name: /^Sonnet/ }));
    expect(appMocks.SetChatSessionCLIModel).toHaveBeenCalledWith({
      sessionId: 42,
      mode: "fixed",
      value: "sonnet",
    });
  });

  it("Given a running session whose new choice waits for the next turn, When the pill opens, Then the face shows old → new and the popover tags the model in use", async () => {
    appMocks.ListCLIModels.mockResolvedValue({
      models: [
        { value: "opus", displayName: "Opus", modelId: "claude-opus" },
        { value: "sonnet", displayName: "Sonnet", modelId: "claude-sonnet" },
      ],
      effectiveValue: "opus",
      effectiveModelId: "claude-opus",
    });
    const user = userEvent.setup();
    render(
      <Harness
        {...nativeSession}
        backendType="claudecode"
        executionLocation=""
        persistedCLIModelMode="fixed"
        persistedCLIModel="sonnet"
        actualCLIModel="claude-opus"
        cliModelPending
      />,
    );
    const pill = screen.getByTestId("provider-pill");
    await within(pill).findByText("Sonnet");
    expect(pill).toHaveTextContent("Opus → Sonnet");
    await user.click(pill);
    const popover = await screen.findByTestId("model-target-popover");
    expect(
      within(popover).getByRole("option", { name: /^Opus/ }),
    ).toHaveTextContent("In use this turn");
    expect(
      within(popover).getByText(
        "This turn finishes on Opus; Sonnet takes over from the next turn",
      ),
    ).toBeInTheDocument();
  });
});

describe("useProviderPill · 输入不变时身份稳定", () => {
  const PROVIDERS = [
    {
      id: 1,
      providerKey: "anthropic",
      name: "Anthropic",
      type: "anthropic",
      enabled: true,
    },
  ];
  const MODELS = [
    {
      id: 11,
      modelKey: "sonnet",
      modelId: "claude-sonnet",
      name: "Sonnet",
      enabled: true,
    },
  ];

  it.each([
    [
      "a provider-bound session",
      { boundProviderKey: "anthropic", persistedProviderKey: "anthropic" },
    ],
    [
      "a session on the CLI login with a pending switch",
      { cliModelPending: true },
    ],
  ])(
    "Given %s, When the host re-renders with unchanged inputs, Then setTarget, catalog, pillState and cliModels keep their identity",
    async (_name, overrides) => {
      appMocks.ListLLMProviders.mockResolvedValue({ items: PROVIDERS });
      appMocks.ListLLMModels.mockResolvedValue({ items: MODELS });
      appMocks.ListCLIModels.mockResolvedValue({
        models: [
          { value: "opus", displayName: "Opus", modelId: "claude-opus" },
        ],
        effectiveValue: "opus",
        effectiveModelId: "claude-opus",
      });
      const props: UseProviderPillOptions = {
        ...nativeSession,
        backendType: "claudecode",
        executionLocation: "",
        persistedCLIModelMode: "fixed",
        persistedCLIModel: "opus",
        actualCLIModel: "claude-opus",
        ...overrides,
      };
      const { result, rerender } = renderHook(
        (p: UseProviderPillOptions) => useProviderPill(p),
        { initialProps: props },
      );
      await waitFor(() => {
        expect(result.current.loading).toBe(false);
        expect(result.current.catalogLoading).toBe(false);
        expect(result.current.catalog.length).toBe(1);
      });
      // 目录与 CLI 列表都是异步到的:多让几轮宏任务,等在途请求全部落地。
      for (let i = 0; i < 5; i++) {
        await act(async () => {
          await new Promise((resolve) => setTimeout(resolve, 0));
        });
      }
      const settled = result.current;

      rerender({ ...props });

      expect(result.current.setTarget).toBe(settled.setTarget);
      expect(result.current.catalog).toBe(settled.catalog);
      expect(result.current.pillState).toBe(settled.pillState);
      expect(result.current.cliModels).toBe(settled.cliModels);
    },
  );

  it("Given a settled pill, When the persisted target really changes, Then pillState and setTarget follow it", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({ items: PROVIDERS });
    appMocks.ListLLMModels.mockResolvedValue({ items: MODELS });
    const props: UseProviderPillOptions = {
      ...nativeSession,
      backendType: "claudecode",
      executionLocation: "",
      boundProviderKey: "anthropic",
      persistedProviderKey: "",
      persistedModelKey: "",
    };
    const { result, rerender } = renderHook(
      (p: UseProviderPillOptions) => useProviderPill(p),
      { initialProps: props },
    );
    await waitFor(() => expect(result.current.catalog.length).toBe(1));
    const settled = result.current;

    rerender({
      ...props,
      persistedProviderKey: "anthropic",
      persistedModelKey: "sonnet",
    });

    await waitFor(() => expect(result.current.modelKey).toBe("sonnet"));
    expect(result.current.pillState).not.toBe(settled.pillState);
    expect(result.current.setTarget).not.toBe(settled.setTarget);
    expect(result.current.catalog).toBe(settled.catalog);
  });
});
