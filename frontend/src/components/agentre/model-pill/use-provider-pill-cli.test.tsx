import { render, screen, within } from "@testing-library/react";
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
