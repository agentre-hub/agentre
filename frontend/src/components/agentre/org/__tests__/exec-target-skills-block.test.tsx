import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ExecTargetSkillsBlock } from "../exec-target-skills-block";
import type { agent_backend_svc } from "../../../../../wailsjs/go/models";

function backend(
  overrides: Partial<agent_backend_svc.BackendItem> = {},
): agent_backend_svc.BackendItem {
  return {
    id: 51,
    type: "claudecode",
    name: "Claude Code",
    llmProviderKey: "",
    llmProviderName: "",
    llmProviderType: "",
    llmProviderModel: "",
    llmProviderActive: false,
    cliPath: "",
    modelRoutes: "{}",
    sandbox: "",
    approval: "",
    envJson: "{}",
    reasoningEffort: "",
    defaultPermissionMode: "",
    defaultModel: "",
    openClawGatewayUrl: "",
    openClawAgentId: "",
    openClawDefaultModel: "",
    openClawSessionMode: "",
    hasToken: false,
    deviceId: "",
    deviceName: "",
    online: false,
    agentCount: 0,
    createtime: 0,
    updatetime: 0,
    ...overrides,
  } as agent_backend_svc.BackendItem;
}

function withCaps(caps: string[], packs: Array<Record<string, unknown>> = []) {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  (window as any).go = {
    app: {
      App: {
        GetBackendCapabilities: vi.fn().mockResolvedValue({
          capabilities: caps,
          permissionModeMeta: {
            allowedModes: [],
            defaultMode: "",
            switchableDuringTurn: false,
            order: [],
          },
        }),
        ListAgentSkillPacks: vi.fn().mockResolvedValue({ packs }),
      },
    },
  };
}

afterEach(() => {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  delete (window as any).go;
});

describe("ExecTargetSkillsBlock", () => {
  it("renders a sequence badge + machine/backend label + its own inherit/on/off count", async () => {
    withCaps(
      ["skills"],
      [
        {
          id: "superpowers@m",
          name: "superpowers",
          description: "d",
          skills: ["a"],
          source: "installed",
          recommended: false,
          installed: true,
          enabled: true,
          globallyEnabled: true,
        },
      ],
    );
    render(
      <ExecTargetSkillsBlock
        agentId={7}
        index={1}
        total={3}
        backend={backend({
          id: 52,
          name: "构建机 Claude",
          deviceId: "3",
          deviceName: "构建机",
          online: true,
        })}
        skills={[]}
        onSkillsChange={vi.fn()}
        copySources={[]}
      />,
    );
    expect(await screen.findByText("2")).toBeInTheDocument();
    expect(screen.getByText(/构建机/)).toBeInTheDocument();
    expect(await screen.findByText("superpowers")).toBeInTheDocument();
  });

  // 「这一档的 backend 不支持技能」现在由**行**说：行不给展开入口，也就没有这一块
  // 可挂（判据搬到 exec-target-list.test.tsx 的
  // 「Given a backend without skills support…」那条）。

  it("offline target: disables Manage skills but keeps granted chips removable and hides the inherited count", async () => {
    withCaps(["skills"]);
    const onSkillsChange = vi.fn();
    render(
      <ExecTargetSkillsBlock
        agentId={7}
        index={0}
        total={2}
        backend={backend({
          id: 51,
          deviceId: "3",
          deviceName: "构建机",
          online: false,
        })}
        skills={[{ id: "opsctl@x", enabled: true }]}
        onSkillsChange={onSkillsChange}
        copySources={[]}
      />,
    );
    expect(await screen.findByText("opsctl")).toBeInTheDocument();
    const manage = screen.getByRole("button", { name: "Manage skills" });
    expect(manage).toBeDisabled();
    expect(screen.queryByText(/inherited/i)).toBeNull();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Remove opsctl" }));
    expect(onSkillsChange).toHaveBeenCalledWith([]);
  });

  it("copy from: only same-backend-type sources are pickable", async () => {
    withCaps(["skills"], []);
    const onSkillsChange = vi.fn();
    const user = userEvent.setup();
    render(
      <ExecTargetSkillsBlock
        agentId={7}
        index={1}
        total={3}
        backend={backend({ id: 52, name: "构建机 Claude" })}
        skills={[]}
        onSkillsChange={onSkillsChange}
        copySources={[
          {
            index: 0,
            label: "1 · 本机 · Claude Code",
            skills: [{ id: "superpowers@x", enabled: true }],
            sameType: true,
          },
          {
            index: 2,
            label: "3 · 老服务器 · Codex",
            skills: [{ id: "codex-only@x", enabled: true }],
            sameType: false,
          },
        ]}
      />,
    );
    await user.click(await screen.findByRole("button", { name: "Copy from" }));
    const enabledOption = await screen.findByRole("button", {
      name: /1 · 本机 · Claude Code/,
    });
    expect(enabledOption).not.toBeDisabled();
    const disabledOption = screen.getByRole("button", {
      name: /3 · 老服务器 · Codex/,
    });
    expect(disabledOption).toBeDisabled();
    await user.click(enabledOption);
    expect(onSkillsChange).toHaveBeenCalledWith([
      { id: "superpowers@x", enabled: true },
    ]);
  });

  it("single-target degenerate case is out of scope here: total=1 still renders (parent decides whether to mount it)", async () => {
    withCaps(["skills"], []);
    render(
      <ExecTargetSkillsBlock
        agentId={7}
        index={0}
        total={1}
        backend={backend({ id: 51 })}
        skills={[]}
        onSkillsChange={vi.fn()}
        copySources={[]}
      />,
    );
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Manage skills" }),
      ).toBeInTheDocument(),
    );
  });
});
