// frontend/src/components/agentre/remote-devices/device-providers-sync.test.tsx
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";

// Hoist mocks so the module factory can reference them.
const appMocks = vi.hoisted(() => ({
  RemoteDeviceListProviders: vi.fn(),
  ListAgentBackends: vi.fn(),
  ListLLMProviders: vi.fn(),
}));

vi.mock("../../../../wailsjs/go/app/App", () => appMocks);

import { DeviceProvidersSync } from "./device-providers-sync";

const DEVICE_ID = 42;

beforeEach(() => {
  vi.clearAllMocks();
  // Stub clipboard globally before each test.
  vi.stubGlobal("navigator", {
    ...navigator,
    clipboard: { writeText: vi.fn().mockResolvedValue(undefined) },
  });
});

describe("DeviceProvidersSync", () => {
  it("shows synced row for key-a and missing row for key-b with fix command", async () => {
    appMocks.RemoteDeviceListProviders.mockResolvedValue([
      { key: "k-a", name: "Anthropic Prod", type: "anthropic" },
    ]);
    appMocks.ListAgentBackends.mockResolvedValue({
      items: [
        {
          deviceID: String(DEVICE_ID),
          llmProviderKey: "k-a",
          name: "backend-a",
          type: "claudecode",
        },
        {
          deviceID: String(DEVICE_ID),
          llmProviderKey: "k-b",
          name: "backend-b",
          type: "builtin",
        },
        // different device — should be excluded
        {
          deviceID: "999",
          llmProviderKey: "k-c",
          name: "backend-c",
          type: "builtin",
        },
      ],
    });
    appMocks.ListLLMProviders.mockResolvedValue({
      items: [
        {
          providerKey: "k-a",
          name: "Anthropic Prod",
          type: "anthropic",
          baseUrl: "https://api.anthropic.com",
        },
        {
          providerKey: "k-b",
          name: "OpenAI Staging",
          type: "openai-chat",
          baseUrl: "https://api.openai.com/v1",
          model: "gpt-4o",
        },
      ],
    });

    render(<DeviceProvidersSync deviceId={DEVICE_ID} />);

    // Panel must appear
    await waitFor(() =>
      expect(screen.getByTestId("device-providers-sync")).toBeInTheDocument(),
    );

    // k-a should be synced (no missing badge, no fix cmd)
    expect(screen.queryByTestId("missing-badge-k-a")).not.toBeInTheDocument();
    expect(screen.queryByTestId("fix-cmd-k-a")).not.toBeInTheDocument();

    // k-b should be missing with fix command
    expect(screen.getByTestId("missing-badge-k-b")).toBeInTheDocument();
    const fixCmdEl = screen.getByTestId("fix-cmd-k-b");
    expect(fixCmdEl).toBeInTheDocument();
    const fixCmdText = fixCmdEl.textContent ?? "";
    expect(fixCmdText).toContain("agentred llm add --key=k-b");
    expect(fixCmdText).toContain('--name="OpenAI Staging"');
    expect(fixCmdText).toContain("--type=openai-chat");
    expect(fixCmdText).toContain("--api-key=<API_KEY>");
    expect(fixCmdText).toContain("--base-url=https://api.openai.com/v1");
    expect(fixCmdText).toContain("--model=gpt-4o");

    // k-c (different device) should not appear
    expect(screen.queryByTestId("missing-badge-k-c")).not.toBeInTheDocument();

    // Copy button for k-b — use fireEvent.click to avoid pointer-events issues
    const copyBtn = screen.getByLabelText(/Copy fix command k-b/);
    fireEvent.click(copyBtn);
    await waitFor(() =>
      expect(navigator.clipboard.writeText).toHaveBeenCalledWith(fixCmdText),
    );
  });

  it("shows empty message when no local backends on this device", async () => {
    appMocks.RemoteDeviceListProviders.mockResolvedValue([]);
    appMocks.ListAgentBackends.mockResolvedValue({ items: [] });
    appMocks.ListLLMProviders.mockResolvedValue({ items: [] });

    render(<DeviceProvidersSync deviceId={DEVICE_ID} />);

    await waitFor(() =>
      expect(
        screen.getByText(
          /No local Agent Backend providers are linked to this device/,
        ),
      ).toBeInTheDocument(),
    );
  });

  it("Given the load fails with machine text, Then it states the fact and keeps the raw text in the folded detail only", async () => {
    appMocks.RemoteDeviceListProviders.mockRejectedValue(
      new Error("network timeout"),
    );
    appMocks.ListAgentBackends.mockResolvedValue({ items: [] });
    appMocks.ListLLMProviders.mockResolvedValue({ items: [] });

    const { container } = render(<DeviceProvidersSync deviceId={DEVICE_ID} />);

    expect(
      await screen.findByText(
        "Could not load the provider sync status for this device",
      ),
    ).toBeInTheDocument();
    const detail = container.querySelector("details");
    expect(detail).not.toBeNull();
    expect(detail).not.toHaveAttribute("open");
    expect(detail?.querySelector("pre")).toHaveTextContent("network timeout");
    // 原话只在折叠详情里，不在正文。
    const outsideDetail = Array.from(container.querySelectorAll("p, span"))
      .filter((el) => !el.closest("details"))
      .map((el) => el.textContent ?? "");
    expect(outsideDetail.some((s) => s.includes("network timeout"))).toBe(
      false,
    );
  });

  it("Given the load failed, When the user clicks Retry, Then it loads again and shows the sync panel", async () => {
    appMocks.RemoteDeviceListProviders.mockRejectedValueOnce(
      new Error("network timeout"),
    ).mockResolvedValue([]);
    appMocks.ListAgentBackends.mockResolvedValue({
      items: [{ deviceID: String(DEVICE_ID), llmProviderKey: "k-a" }],
    });
    appMocks.ListLLMProviders.mockResolvedValue({
      items: [
        { providerKey: "k-a", name: "Anthropic Prod", type: "anthropic" },
      ],
    });

    render(<DeviceProvidersSync deviceId={DEVICE_ID} />);

    fireEvent.click(await screen.findByRole("button", { name: "Retry" }));

    await waitFor(() =>
      expect(screen.getByTestId("device-providers-sync")).toBeInTheDocument(),
    );
    expect(appMocks.RemoteDeviceListProviders).toHaveBeenCalledTimes(2);
    expect(
      screen.queryByText(
        "Could not load the provider sync status for this device",
      ),
    ).toBeNull();
  });
});
