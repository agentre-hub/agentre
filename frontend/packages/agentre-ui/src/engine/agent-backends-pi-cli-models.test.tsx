import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { AgentBackendsPanel } from "./agent-backends";
import type { BackendView, EngineSettingsPorts } from "./ports";

// spec 2026-10-26 决策 7：CLI 自带模型段对走 CLI 登录态的后端按 backendType 通用渲染。
// pi 的模型值形状是 "provider/id"，列表按 pi 自己声明的供应商分小节。
function setup(defaultModel = "") {
  const backend = {
    id: 9,
    syncId: "piagent-9",
    name: "My piagent",
    type: "piagent",
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

const piCatalog = {
  models: [
    {
      value: "anthropic/claude-sonnet-5-5",
      label: "Claude Sonnet 5.5",
      modelId: "claude-sonnet-5-5",
      group: "anthropic",
    },
    {
      value: "zhipu/glm-5.3",
      label: "GLM 5.3",
      modelId: "glm-5.3",
      group: "zhipu",
    },
  ],
  effectiveValue: "anthropic/claude-sonnet-5-5",
  effectiveModelId: "claude-sonnet-5-5",
};

describe("piagent backend CLI models", () => {
  it("Given a saved Pi backend, When opening its binding picker, Then it asks for the Pi CLI catalog and groups the models by pi provider", async () => {
    const { ports } = setup();
    const listCLIModels = vi.fn().mockResolvedValue(piCatalog);
    const user = userEvent.setup();
    render(<AgentBackendsPanel ports={ports} listCLIModels={listCLIModels} />);
    await user.click(
      await screen.findByRole("button", { name: "Edit My piagent" }),
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
        "Pi Agent built-in · CLI login account",
      ),
    ).toBeInTheDocument();
    // 按 pi 自己的供应商分小节：两个 group 各一组，组名就是 pi 的 provider。
    const groups = within(section as HTMLElement)
      .getAllByRole("group")
      .filter((g) => g.getAttribute("aria-label"));
    expect(groups.map((g) => g.getAttribute("aria-label"))).toEqual([
      "anthropic",
      "zhipu",
    ]);
    // 「跟随 CLI 默认 → Claude Sonnet 5.5」与固定行都写这颗模型。
    expect(
      within(section as HTMLElement).getAllByRole("option", {
        name: /Claude Sonnet 5.5/,
      }),
    ).toHaveLength(2);
  });

  it("Given a saved Pi backend with a fixed CLI model, When saving, Then provider/id is written to defaultModel", async () => {
    const { ports, updateBackend } = setup("zhipu/glm-5.3");
    const user = userEvent.setup();
    render(
      <AgentBackendsPanel
        ports={ports}
        listCLIModels={vi.fn().mockResolvedValue(piCatalog)}
      />,
    );
    await user.click(
      await screen.findByRole("button", { name: "Edit My piagent" }),
    );
    await user.click(bindingTrigger());
    expect(bindingTrigger()).toHaveTextContent("Pi Agent login account");
    expect(bindingTrigger()).toHaveTextContent("Fixed");
    const rows = screen.getAllByRole("option", { name: /GLM 5.3/ });
    await user.click(rows[rows.length - 1]);
    await user.click(
      within(screen.getByRole("dialog")).getByRole("button", { name: "Save" }),
    );
    await waitFor(() =>
      expect(updateBackend).toHaveBeenCalledWith(
        9,
        expect.objectContaining({ defaultModel: "zhipu/glm-5.3" }),
      ),
    );
  });

  it("Given a saved Pi backend bound to a provider, Then the stored defaultModel is not shown as the binding", async () => {
    const { ports } = setup("zhipu/glm-5.3");
    (
      ports.listBackends as unknown as {
        mockResolvedValue: (v: unknown) => void;
      }
    ).mockResolvedValue([
      {
        id: 9,
        syncId: "piagent-9",
        name: "My piagent",
        type: "piagent",
        llmProviderKey: "key-1",
        llmModelKey: "mk-1",
        envJson: "{}",
        deviceId: "",
        defaultModel: "zhipu/glm-5.3",
        modelRoutes: {},
        cliByDevice: [],
        agentCount: 0,
      },
    ]);
    (
      ports.listProviders as unknown as {
        mockResolvedValue: (v: unknown) => void;
      }
    ).mockResolvedValue([
      {
        id: 1,
        type: "anthropic",
        name: "Anthropic",
        providerKey: "key-1",
        baseUrl: "",
        maskedApiKey: "sk-•••",
        hasApiKey: true,
        model: "claude-sonnet-4-6",
        maxOutput: 0,
        contextWindow: 0,
        createtime: 0,
        updatetime: 0,
      },
    ]);
    const user = userEvent.setup();
    render(
      <AgentBackendsPanel
        ports={ports}
        listCLIModels={vi.fn().mockResolvedValue(piCatalog)}
      />,
    );
    await user.click(
      await screen.findByRole("button", { name: "Edit My piagent" }),
    );
    await user.click(bindingTrigger());
    // 绑了供应商时该值被忽略：主行回到供应商绑定，不再声称「Pi 登录账号 / 固定」。
    expect(bindingTrigger()).not.toHaveTextContent("Pi Agent login account");
    expect(bindingTrigger()).toHaveTextContent("Anthropic");
  });
});
