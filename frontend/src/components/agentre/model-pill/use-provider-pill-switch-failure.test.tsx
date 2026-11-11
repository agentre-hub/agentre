import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ExternalToast } from "sonner";
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

const sonnerMocks = vi.hoisted(() => ({
  toast: Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn() }),
}));
vi.mock("sonner", () => sonnerMocks);

import { ProviderPill, useProviderPill } from ".";
import type { UseProviderPillOptions } from ".";

function Harness(props: UseProviderPillOptions) {
  const pill = useProviderPill(props);
  return <ProviderPill {...pill} />;
}

const PROVIDER = {
  id: 1,
  providerKey: "acme-anthropic",
  name: "Acme Claude",
  type: "anthropic",
  enabled: true,
  defaultModelKey: "mk-sonnet",
};

function model(key: string, modelId: string) {
  return { modelKey: key, modelId, name: modelId, enabled: true };
}

function lastErrorToast(): { title: unknown; options: ExternalToast } {
  const calls = sonnerMocks.toast.error.mock.calls;
  const [title, options] = calls[calls.length - 1] ?? [];
  return { title, options: (options ?? {}) as ExternalToast };
}

beforeEach(() => {
  vi.clearAllMocks();
  appMocks.ListLLMProviders.mockResolvedValue({ items: [PROVIDER] });
  appMocks.ListLLMModels.mockResolvedValue({
    items: [
      model("mk-sonnet", "claude-sonnet-4-5"),
      model("mk-opus", "claude-opus-4-5"),
    ],
  });
  appMocks.RemoteDeviceFingerprint.mockResolvedValue("sha256:local");
  appMocks.RemoteDeviceList.mockResolvedValue([]);
  appMocks.RemoteDeviceListProviders.mockResolvedValue([]);
});

async function switchToOpus() {
  render(
    <Harness
      backendType="claudecode"
      sessionId={42}
      boundProviderKey="acme-anthropic"
      persistedProviderKey="acme-anthropic"
      persistedModelKey="mk-sonnet"
    />,
  );
  const pill = screen.getByTestId("provider-pill");
  await waitFor(() => expect(pill).not.toBeDisabled());
  await waitFor(() => expect(pill).toHaveTextContent("claude-sonnet-4-5"));
  const user = userEvent.setup();
  await user.click(pill);
  const listbox = within(screen.getByTestId("model-target-popover")).getByRole(
    "listbox",
  );
  await user.click(
    within(listbox).getByRole("option", { name: /claude-opus-4-5/ }),
  );
  return user;
}

// A2（spec 2026-11-10「输入框药丸切换失败」）
describe("useProviderPill · switch failure", () => {
  it("Given the switch is rejected, When a model is picked, Then the pill rolls back and a toast says it is still the previous model with the raw text only in a collapsed Details", async () => {
    appMocks.SetChatSessionModelTarget.mockRejectedValue(
      new Error("rpc error: session 42 not attached"),
    );

    await switchToOpus();

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    const { title, options } = lastErrorToast();
    expect(title).toBe("Model didn't switch — still claude-sonnet-4-5");
    expect(screen.getByTestId("provider-pill")).toHaveTextContent(
      "claude-sonnet-4-5",
    );

    render(<>{options.description as React.ReactNode}</>);
    const details = screen
      .getByText(/session 42 not attached/)
      .closest("details");
    expect(details).not.toBeNull();
    expect(details).not.toHaveAttribute("open");
  });

  it("Given a switch failed, When the picker is opened again, Then the provider list is still there with a localized failure line below it and no raw text", async () => {
    appMocks.SetChatSessionModelTarget.mockRejectedValue(
      new Error("rpc error: session 42 not attached"),
    );
    const user = await switchToOpus();
    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());

    await user.click(screen.getByTestId("provider-pill"));
    const popover = screen.getByTestId("model-target-popover");
    expect(within(popover).getByRole("alert")).toHaveTextContent(
      "The last switch didn't go through",
    );
    expect(within(popover).getByRole("listbox")).toBeInTheDocument();
    expect(within(popover).queryByText(/not attached/)).toBeNull();
  });

  it("Given the switch succeeds, When a model is picked, Then no failure toast is shown", async () => {
    appMocks.SetChatSessionModelTarget.mockResolvedValue({});

    await switchToOpus();

    await waitFor(() =>
      expect(appMocks.SetChatSessionModelTarget).toHaveBeenCalled(),
    );
    expect(sonnerMocks.toast.error).not.toHaveBeenCalled();
  });
});

describe("useProviderPill · CLI-native model switch failure", () => {
  async function chooseDeniedCLIModel() {
    appMocks.ListLLMProviders.mockResolvedValue({ items: [] });
    appMocks.ListCLIModels.mockResolvedValue({
      models: [
        { value: "opus", displayName: "Opus", modelId: "claude-opus" },
        { value: "denied", displayName: "Denied model", modelId: "denied" },
      ],
      effectiveValue: "opus",
      effectiveModelId: "claude-opus",
    });
    appMocks.SetChatSessionCLIModel.mockRejectedValue(
      new Error("Model 'denied' not found"),
    );
    render(
      <Harness
        backendType="claudecode"
        backendId={9}
        sessionId={42}
        boundProviderKey=""
        boundModelKey=""
        boundCLIModel=""
        persistedProviderKey=""
        persistedModelKey=""
        persistedCLIModelMode="fixed"
        persistedCLIModel="opus"
      />,
    );
    const pill = screen.getByTestId("provider-pill");
    await waitFor(() => expect(pill).toHaveTextContent("Opus"));
    const user = userEvent.setup();
    await user.click(pill);
    const popover = await screen.findByTestId("model-target-popover");
    await user.click(
      await within(popover).findByRole("option", { name: /Denied model/ }),
    );
    return { pill, popover };
  }

  it("Given the CLI rejects the model, When it is picked, Then the pill keeps the previous model and a toast says so with the raw text only in a collapsed Details", async () => {
    const { pill } = await chooseDeniedCLIModel();

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    const { title, options } = lastErrorToast();
    expect(title).toBe("Model didn't switch — still Opus");
    expect(pill).toHaveTextContent("Opus");
    expect(pill).not.toHaveTextContent("Denied model");

    render(<>{options.description as React.ReactNode}</>);
    const details = screen
      .getAllByText(/'denied' not found/)[0]
      .closest("details");
    expect(details).not.toBeNull();
    expect(details).not.toHaveAttribute("open");
  });

  it("Given the CLI rejects the model, When the popover stays open, Then it shows the localized failure line below the list and never the raw text", async () => {
    const { popover } = await chooseDeniedCLIModel();

    await waitFor(() =>
      expect(within(popover).getByRole("alert")).toHaveTextContent(
        "The last switch didn't go through",
      ),
    );
    expect(within(popover).queryByText(/'denied' not found/)).toBeNull();
    expect(
      within(popover).getByRole("option", { name: /Denied model/ }),
    ).toBeInTheDocument();
  });
});
