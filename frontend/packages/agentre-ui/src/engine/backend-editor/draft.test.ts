import { describe, expect, it, vi } from "vitest";

import type { Backend, Translate } from "../agent-backends-shared";

import { saveBackendDraft, type BackendDraft } from "./draft";

function draft(overrides: Partial<BackendDraft> = {}): BackendDraft {
  return {
    type: "claudecode",
    name: "docker · Claude Code",
    deviceId: "agentred-b",
    llmProviderKey: "",
    llmModelKey: "",
    cliPath: "",
    modelRoutes: {} as BackendDraft["modelRoutes"],
    sandbox: "",
    approval: "",
    envJson: "",
    reasoningEffort: "" as BackendDraft["reasoningEffort"],
    defaultPermissionMode: "",
    defaultModel: "",
    openClawGatewayUrl: "",
    openClawAgentId: "",
    openClawDefaultModel: "",
    openClawSessionMode: "",
    hermesUrl: "",
    hermesAuthProvider: "",
    hermesUserId: "",
    ...overrides,
  };
}

function bridge() {
  return {
    CreateAgentBackend: vi.fn(),
    CreateOpenClawAgentBackend: vi.fn(),
    UpdateAgentBackend: vi.fn(),
    UpdateOpenClawAgentBackend: vi.fn(),
  };
}

const t = ((key: string) => key) as unknown as Translate;

describe("saveBackendDraft", () => {
  // 端口契约里 BackendInput.type 是必填：宿主可以据此判断能不能落这一类后端。
  it("编辑保存时把后端类型一并交给 UpdateAgentBackend", async () => {
    const editing = { id: 7, type: "claudecode" } as Backend;
    const ports = bridge();

    await saveBackendDraft({
      draft: draft(),
      state: { kind: "edit", backend: editing },
      editing,
      openClawToken: "",
      clearOpenClawToken: false,
      bridge: ports,
      onSaved: vi.fn(),
      t,
    });

    expect(ports.UpdateAgentBackend).toHaveBeenCalledWith(
      expect.objectContaining({ id: 7, type: "claudecode" }),
    );
  });

  it("OpenClaw 的编辑保存同样带上类型", async () => {
    const editing = { id: 8, type: "openclaw" } as Backend;
    const ports = bridge();

    await saveBackendDraft({
      draft: draft({ type: "openclaw" }),
      state: { kind: "edit", backend: editing },
      editing,
      openClawToken: "token",
      clearOpenClawToken: false,
      bridge: ports,
      onSaved: vi.fn(),
      t,
    });

    expect(ports.UpdateOpenClawAgentBackend).toHaveBeenCalledWith(
      expect.objectContaining({ id: 8, type: "openclaw" }),
      "token",
      false,
    );
  });
});
