import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { AgentBackendsPanel } from "./agent-backends";
import type { BackendView, EngineSettingsPorts } from "./ports";

// spec 2026-10-26 决策 7：CLI 自带模型段对走 CLI 登录态的后端按 backendType 通用渲染，
// Codex 与 Claude Code 一样能从后端设置里挑 CLI 自己的模型。
function setup(type: "codex" | "claudecode", defaultModel: string) {
  const backend = {
    id: 9,
    syncId: `${type}-9`,
    name: `My ${type}`,
    type,
    llmProviderKey: "",
    llmModelKey: "",
    envJson: "{}",
    deviceId: "",
    defaultModel,
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

function bindingTrigger() {
  return screen
    .getAllByRole("button")
    .find((b) => (b.getAttribute("aria-label") ?? "") === "Model binding")!;
}

describe("codex backend CLI models", () => {
  it("Given a saved Codex backend, When opening its binding picker, Then it asks for the Codex CLI catalog and offers the Codex section", async () => {
    const { ports } = setup("codex", "");
    const listCLIModels = vi.fn().mockResolvedValue({
      models: [
        { value: "gpt-6-luna", label: "GPT-6 Luna", modelId: "gpt-6-luna" },
      ],
      effectiveValue: "gpt-6-luna",
      effectiveModelId: "gpt-6-luna",
    });
    const user = userEvent.setup();
    render(<AgentBackendsPanel ports={ports} listCLIModels={listCLIModels} />);
    await user.click(
      await screen.findByRole("button", { name: "Edit My codex" }),
    );
    await waitFor(() =>
      expect(listCLIModels).toHaveBeenCalledWith({
        backendId: 9,
        refresh: false,
      }),
    );
    await user.click(bindingTrigger());
    const section = document.querySelector("[data-cli-model-section]");
    expect(section).not.toBeNull();
    expect(
      within(section as HTMLElement).getByText(
        "Codex built-in · CLI login account",
      ),
    ).toBeInTheDocument();
    // 首行「跟随 CLI 默认 → GPT-6 Luna」与固定模型行都写这颗模型，固定行是后出现的那个。
    expect(
      within(section as HTMLElement).getAllByRole("option", {
        name: /GPT-6 Luna/,
      }),
    ).toHaveLength(2);
  });

  it("Given a saved Codex backend with a fixed CLI model, When saving, Then the Codex model id is written to defaultModel", async () => {
    const { ports, updateBackend } = setup("codex", "gpt-5.5");
    const listCLIModels = vi.fn().mockResolvedValue({
      models: [
        { value: "gpt-6-luna", label: "GPT-6 Luna", modelId: "gpt-6-luna" },
      ],
      effectiveValue: "gpt-6-luna",
      effectiveModelId: "gpt-6-luna",
    });
    const user = userEvent.setup();
    render(<AgentBackendsPanel ports={ports} listCLIModels={listCLIModels} />);
    await user.click(
      await screen.findByRole("button", { name: "Edit My codex" }),
    );
    await user.click(bindingTrigger());
    expect(bindingTrigger()).toHaveTextContent("Codex login account");
    expect(bindingTrigger()).toHaveTextContent("Fixed");
    const modelRows = screen.getAllByRole("option", { name: /GPT-6 Luna/ });
    await user.click(modelRows[modelRows.length - 1]);
    await user.click(
      within(screen.getByRole("dialog")).getByRole("button", { name: "Save" }),
    );
    await waitFor(() =>
      expect(updateBackend).toHaveBeenCalledWith(
        9,
        expect.objectContaining({ defaultModel: "gpt-6-luna" }),
      ),
    );
  });

  it("Given a saved Claude Code backend, Then its catalog gate and section are unchanged", async () => {
    const { ports } = setup("claudecode", "");
    const listCLIModels = vi.fn().mockResolvedValue({
      models: [{ value: "sonnet", label: "CLI Sonnet", modelId: "sonnet-id" }],
      effectiveValue: "sonnet",
      effectiveModelId: "sonnet-id",
    });
    const user = userEvent.setup();
    render(<AgentBackendsPanel ports={ports} listCLIModels={listCLIModels} />);
    await user.click(
      await screen.findByRole("button", { name: "Edit My claudecode" }),
    );
    await waitFor(() => expect(listCLIModels).toHaveBeenCalled());
    expect(bindingTrigger()).toHaveTextContent("Claude Code login account");
  });
});
