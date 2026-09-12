import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const appMocks = vi.hoisted(() => ({
  ListLLMProviders: vi.fn(),
  ListLLMModels: vi.fn(),
  SetChatSessionModelTarget: vi.fn(),
  RemoteDeviceFingerprint: vi.fn(),
  RemoteDeviceList: vi.fn(),
  RemoteDeviceListProviders: vi.fn(),
  RemoteDeviceSyncProvider: vi.fn(),
}));
vi.mock("../../../../../wailsjs/go/app/App", () => appMocks);

import { BoardModelPill } from "../model-pill";

const anthropic = {
  id: 1,
  providerKey: "prov-anthropic",
  name: "Anthropic",
  type: "anthropic",
  enabled: true,
  defaultModelKey: "m-sonnet",
};

beforeEach(() => {
  vi.clearAllMocks();
  appMocks.ListLLMProviders.mockResolvedValue({ items: [anthropic] });
  appMocks.ListLLMModels.mockResolvedValue({
    items: [
      {
        modelKey: "m-sonnet",
        modelId: "claude-sonnet-5",
        name: "Sonnet",
        enabled: true,
      },
    ],
  });
  appMocks.RemoteDeviceFingerprint.mockResolvedValue("sha256:local");
  appMocks.RemoteDeviceList.mockResolvedValue([]);
  appMocks.RemoteDeviceListProviders.mockResolvedValue([]);
});

describe("BoardModelPill", () => {
  it("Given a fixed model, When the pill renders, Then the resolved model id is on the trigger", async () => {
    render(
      <BoardModelPill
        className="pill"
        backendType="claudecode"
        providerKey="prov-anthropic"
        modelKey="m-sonnet"
        onChange={() => {}}
      />,
    );

    expect(await screen.findByText("claude-sonnet-5")).toBeInTheDocument();
  });

  it("Given a target is picked, When the picker closes, Then the host is handed the whole target", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    const onChange = vi.fn();
    render(
      <BoardModelPill
        className="pill"
        backendType="claudecode"
        providerKey=""
        modelKey=""
        onChange={onChange}
      />,
    );

    await user.click(await screen.findByTestId("board-model-pill"));
    const options = await screen.findAllByRole("option");
    await user.click(options[options.length - 1]);

    await waitFor(() => expect(onChange).toHaveBeenCalled());
    expect(onChange.mock.calls[0][0].providerKey).toBe("prov-anthropic");
  });

  it("Given a provider that the backend cannot use, When the catalog settles, Then the pill falls back to the agent binding", async () => {
    const onChange = vi.fn();
    render(
      <BoardModelPill
        className="pill"
        backendType="codex"
        providerKey="prov-anthropic"
        modelKey="m-sonnet"
        onChange={onChange}
      />,
    );

    await waitFor(() =>
      expect(onChange).toHaveBeenCalledWith({ providerKey: "", modelKey: "" }),
    );
  });

  it("Given a compatible provider, When the catalog settles, Then nothing is reset behind the user's back", async () => {
    const onChange = vi.fn();
    render(
      <BoardModelPill
        className="pill"
        backendType="claudecode"
        providerKey="prov-anthropic"
        modelKey="m-sonnet"
        onChange={onChange}
      />,
    );

    await screen.findByText("claude-sonnet-5");
    expect(onChange).not.toHaveBeenCalled();
  });
});
