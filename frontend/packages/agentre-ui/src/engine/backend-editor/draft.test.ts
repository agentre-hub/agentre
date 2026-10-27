import { describe, expect, it, vi } from "vitest";

import type { Backend, Translate } from "../agent-backends-shared";

import {
  buildBackendDraft,
  changedBackendFields,
  saveBackendDraft,
  type BackendDraft,
  type BackendDraftFields,
} from "./draft";

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
      baseline: draft(),
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
      baseline: draft({ type: "openclaw" }),
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

describe("changedBackendFields", () => {
  it("Given an edit baseline, When the CLI path and a Claude tier route differ, Then both are named, the route under config", () => {
    const baseline = draft({ cliPath: "/usr/bin/claude" });
    const next = draft({
      cliPath: "/opt/claude",
      modelRoutes: {
        OPUS: { providerKey: "anthropic", modelKey: "opus" },
      } as BackendDraft["modelRoutes"],
    });

    expect(changedBackendFields(baseline, next)).toEqual([
      "cliPath",
      "config.modelRoutes",
    ]);
  });

  it("Given no baseline (create), When the env table is empty, Then envJson is not reported as populated", () => {
    expect(
      changedBackendFields(null, draft({ envJson: "{}", name: "" })),
    ).toEqual(["type", "deviceId"]);
  });
});

function fields(
  overrides: Partial<BackendDraftFields> = {},
): BackendDraftFields {
  return {
    type: "codex",
    name: "codex",
    deviceId: "",
    llmProviderKey: "",
    llmModelKey: "",
    cliPath: "",
    routes: {
      OPUS: { providerKey: "", modelKey: "" },
      SONNET: { providerKey: "", modelKey: "" },
      HAIKU: { providerKey: "", modelKey: "" },
    },
    sandbox: "",
    approval: "",
    envEntries: [],
    reasoningEffort: "",
    defaultPermissionMode: "",
    defaultModel: "",
    openClawGatewayURL: "",
    openClawAgentID: "",
    openClawDefaultModel: "",
    hermesUrl: "",
    hermesAuthProvider: "",
    hermesUserId: "",
    ...overrides,
  };
}

describe("buildBackendDraft default model", () => {
  // spec 2026-10-26 决策 7：CLI 登录态下三种 CLI 都能存一个默认模型，codex 用自己的
  // 模型 id、pi 用 "provider/id"；绑定供应商期间该值保留但被忽略。
  it("Given a codex backend, When a CLI model is chosen, Then defaultModel is kept (trimmed) instead of cleared", () => {
    expect(
      buildBackendDraft(fields({ type: "codex", defaultModel: " gpt-6-luna " }))
        .defaultModel,
    ).toBe("gpt-6-luna");
  });

  it("Given a pi-agent backend, When a CLI model is chosen, Then defaultModel is kept as provider/id", () => {
    expect(
      buildBackendDraft(
        fields({ type: "piagent", defaultModel: " zhipu/glm-5.3 " }),
      ).defaultModel,
    ).toBe("zhipu/glm-5.3");
  });

  it("Given a Claude Code backend, Then defaultModel is still kept", () => {
    expect(
      buildBackendDraft(
        fields({ type: "claudecode", defaultModel: " sonnet " }),
      ).defaultModel,
    ).toBe("sonnet");
  });

  it("Given a backend that has no CLI model binding, Then defaultModel is cleared", () => {
    expect(
      buildBackendDraft(fields({ type: "builtin", defaultModel: "x" }))
        .defaultModel,
    ).toBe("");
    expect(
      buildBackendDraft(fields({ type: "openclaw", defaultModel: "x" }))
        .defaultModel,
    ).toBe("");
  });
});
