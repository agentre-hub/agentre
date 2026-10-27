import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { AgentBackendsPanel } from "./agent-backends";
import type { BackendView, EngineSettingsPorts } from "./ports";

function setup() {
  const backend = {
    id: 7,
    syncId: "cc-7",
    name: "My Claude",
    type: "claudecode",
    llmProviderKey: "",
    llmModelKey: "",
    envJson: "{}",
    deviceId: "",
    defaultModel: "saved-manual-id",
    modelRoutes: {},
    cliByDevice: [],
    agentCount: 0,
  } as unknown as BackendView;
  const updateBackend = vi.fn().mockResolvedValue(backend);
  const ports = {
    listBackends: vi.fn().mockResolvedValue([backend]),
    listProviders: vi.fn().mockResolvedValue([]),
    listModels: vi.fn().mockResolvedValue([]),
    localDeviceFingerprint: vi.fn().mockResolvedValue("sha256:self"),
    listRuntimeDevices: vi.fn().mockResolvedValue([]),
    listAccountDevices: vi.fn().mockResolvedValue([]),
    updateBackend,
    cliPath: { get: vi.fn().mockResolvedValue(""), set: vi.fn() },
    canEditEnvJSON: true,
  } as unknown as EngineSettingsPorts;
  return { backend, ports, updateBackend };
}

describe("backend editor CLI models", () => {
  it("Given a saved Claude backend, When selecting a CLI model and saving, Then the CLI value is written to defaultModel and provider keys stay empty", async () => {
    const { ports, updateBackend } = setup();
    const listCLIModels = vi.fn().mockResolvedValue({
      models: [{ value: "sonnet", label: "CLI Sonnet", modelId: "sonnet-id" }],
      effectiveValue: "sonnet",
      effectiveModelId: "sonnet-id",
    });
    const user = userEvent.setup();
    render(<AgentBackendsPanel ports={ports} listCLIModels={listCLIModels} />);
    await user.click(
      await screen.findByRole("button", { name: "Edit My Claude" }),
    );
    await waitFor(() =>
      expect(listCLIModels).toHaveBeenCalledWith({
        backendId: 7,
        refresh: false,
      }),
    );
    const dialog = screen.getByRole("dialog");
    expect(
      within(dialog).queryByLabelText("Custom Model"),
    ).not.toBeInTheDocument();
    await user.click(
      within(dialog).getByRole("button", { name: "Model binding" }),
    );
    await user.click(screen.getByRole("option", { name: /^CLI Sonnet/ }));
    expect(
      within(dialog).getByRole("button", { name: "Model binding" }),
    ).toHaveTextContent("CLI Sonnet · sonnet-id");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(updateBackend).toHaveBeenCalledWith(
        7,
        expect.objectContaining({
          defaultModel: "sonnet",
          llmProviderKey: "",
          llmModelKey: "",
        }),
      ),
    );
  });
  it("Given CLI discovery fails, When retrying and entering a manual ID, Then discovery refreshes and the manual value can still be saved", async () => {
    const { ports, updateBackend } = setup();
    const listCLIModels = vi
      .fn()
      .mockResolvedValue({ models: [], error: "unsupported initialize" });
    const user = userEvent.setup();
    render(<AgentBackendsPanel ports={ports} listCLIModels={listCLIModels} />);
    await user.click(
      await screen.findByRole("button", { name: "Edit My Claude" }),
    );
    await user.click(screen.getByRole("button", { name: "Model binding" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "unsupported initialize",
    );
    await user.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() =>
      expect(listCLIModels).toHaveBeenLastCalledWith({
        backendId: 7,
        refresh: true,
      }),
    );
    await user.type(
      screen.getByRole("textbox", { name: "Enter another model ID" }),
      "manual-cli-id{Enter}",
    );
    await user.click(
      within(screen.getByRole("dialog")).getByRole("button", { name: "Save" }),
    );
    await waitFor(() =>
      expect(updateBackend).toHaveBeenCalledWith(
        7,
        expect.objectContaining({ defaultModel: "manual-cli-id" }),
      ),
    );
  });
});
