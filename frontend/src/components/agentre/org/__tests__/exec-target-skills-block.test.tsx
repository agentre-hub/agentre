import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));
vi.mock("sonner", () => sonnerMocks);

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
  sonnerMocks.toast.error.mockReset();
});

function withFailingCatalog(listPacks: ReturnType<typeof vi.fn>) {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  (window as any).go = {
    app: {
      App: {
        GetBackendCapabilities: vi.fn().mockResolvedValue({
          capabilities: ["skills"],
          permissionModeMeta: {
            allowedModes: [],
            defaultMode: "",
            switchableDuringTurn: false,
            order: [],
          },
        }),
        ListAgentSkillPacks: listPacks,
      },
    },
  };
}

const CATALOG_FAILED = "Could not list the skill packs for this target";

describe("ExecTargetSkillsBlock catalog load failure", () => {
  it("Given the catalog load fails with machine text, Then the block states the fact, keeps the raw text folded, and Retry loads it again", async () => {
    const raw = "rpc error: daemon closed the stream";
    const listPacks = vi
      .fn()
      .mockRejectedValueOnce(new Error(raw))
      .mockResolvedValue({
        packs: [
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
      });
    withFailingCatalog(listPacks);
    const user = userEvent.setup();
    const { container } = render(
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

    expect(await screen.findByText(CATALOG_FAILED)).toBeInTheDocument();
    const detail = container.querySelector("details");
    expect(detail).not.toHaveAttribute("open");
    expect(detail?.querySelector("pre")).toHaveTextContent(raw);

    await user.click(screen.getByRole("button", { name: "Retry" }));

    expect(await screen.findByText("superpowers")).toBeInTheDocument();
    expect(screen.queryByText(CATALOG_FAILED)).toBeNull();
    expect(listPacks).toHaveBeenCalledTimes(2);
  });

  it("Given the picker is open, When its rescan fails, Then a toast states the fact with the raw text folded and a retry", async () => {
    const raw = "scan plugins: context deadline exceeded";
    const listPacks = vi
      .fn()
      .mockResolvedValueOnce({ packs: [] })
      .mockRejectedValue(new Error(raw));
    withFailingCatalog(listPacks);
    const user = userEvent.setup();
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
    await waitFor(() => expect(listPacks).toHaveBeenCalledTimes(1));

    await user.click(
      await screen.findByRole("button", { name: "Manage skills" }),
    );
    await user.click(await screen.findByRole("button", { name: /rescan/i }));

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    const [title, options] = sonnerMocks.toast.error.mock.calls[0];
    expect(title).toBe(CATALOG_FAILED);
    const { container, unmount } = render(<>{options.description}</>);
    expect(container.querySelector("details pre")).toHaveTextContent(raw);
    unmount();
    expect(options.action.label).toBe("Retry");
    options.action.onClick();
    await waitFor(() => expect(listPacks).toHaveBeenCalledTimes(3));
    expect(listPacks).toHaveBeenLastCalledWith(7, 51, true);
  });
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
