import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { OrgDetailAgent } from "../org-detail-agent";
import type { OrgAgent, OrgDepartment } from "../types";
import type { agent_backend_svc } from "../../../../../wailsjs/go/models";

// agent() 是测试夹具：agentBackendId/skills 是历史派生字段（=execTargets[0]，
// 仍被组织架构页当前只读代码路径消费的兼容视图），execTargets 才是真源
// （R15/R15e）。调用方沿用 agentBackendId/skills 覆写时自动派生出对应的单元素
// execTargets——这样绝大多数既有测试（单档场景）不需要改一行；需要多档的测试
// 直接显式传 execTargets 覆盖掉这份自动派生。
const agent = (overrides: Partial<OrgAgent> = {}): OrgAgent => {
  const agentBackendId = overrides.agentBackendId ?? 0;
  const skills = overrides.skills ?? [
    { id: "superpowers@m", enabled: true },
    { id: "frontend-design@m", enabled: true },
  ];
  const base = {
    id: 7,
    name: "Eva",
    description: "工程总监",
    avatarColor: "agent-2",
    avatarDataUrl: "",
    systemBadge: "",
    departmentId: 1,
    departmentName: "工程部",
    parentAgentId: 0,
    parentAgentName: "",
    agentBackendId,
    sortOrder: 1,
    prompt: ["你是 Eva。", "负责工程。"],
    skills,
    execTargets: agentBackendId > 0 ? [{ id: 1, agentBackendId, skills }] : [],
    createtime: 0,
    updatetime: 0,
  };
  return { ...base, ...overrides } as OrgAgent;
};

const dept: OrgDepartment = {
  id: 1,
  name: "工程部",
  description: "",
  icon: "hammer",
  accentColor: "agent-2",
  parentId: 0,
  leadAgentId: 0,
  leadAgentName: "",
  sortOrder: 1,
  directAgentCount: 1,
  subdepartmentCount: 0,
  memberCount: 1,
  createtime: 0,
  updatetime: 0,
} as OrgDepartment;

const backend = (
  overrides: Partial<agent_backend_svc.BackendItem> = {},
): agent_backend_svc.BackendItem =>
  ({
    id: 5,
    type: "claudecode",
    name: "Claude Code",
    llmProviderId: 3,
    llmProviderName: "Anthropic 官方",
    llmProviderType: "anthropic",
    llmProviderModel: "Sonnet 4.6",
    llmProviderActive: true,
    cliPath: "",
    modelRoutes: "{}",
    sandbox: "",
    approval: "",
    envJson: "{}",
    agentCount: 1,
    createtime: 0,
    updatetime: 0,
    ...overrides,
  }) as agent_backend_svc.BackendItem;

function LocationStateProbe() {
  const location = useLocation();
  return (
    <output data-testid="location-state">
      {JSON.stringify(location.state ?? {})}
    </output>
  );
}

function renderPanel(
  overrides: Partial<OrgAgent> = {},
  backends: agent_backend_svc.BackendItem[] = [],
  availableTools: string[] = [],
) {
  const onUpdate = vi.fn().mockResolvedValue(undefined);
  const onDelete = vi.fn().mockResolvedValue(undefined);
  const onUploadAvatar = vi.fn().mockResolvedValue(undefined);
  const onDeleteAvatar = vi.fn().mockResolvedValue(undefined);
  const onClose = vi.fn();
  render(
    <MemoryRouter initialEntries={["/org"]}>
      <LocationStateProbe />
      <OrgDetailAgent
        agent={agent(overrides)}
        departments={[dept]}
        agents={[]}
        backends={backends}
        isLeadOf={null}
        availableTools={availableTools}
        onUpdate={onUpdate}
        onMove={vi.fn().mockResolvedValue(undefined)}
        onDelete={onDelete}
        onUploadAvatar={onUploadAvatar}
        onDeleteAvatar={onDeleteAvatar}
        onClose={onClose}
      />
    </MemoryRouter>,
  );
  return { onUpdate, onDelete, onUploadAvatar, onDeleteAvatar };
}

function withCaps(
  caps: string[],
  packs: Array<Record<string, unknown>> = [],
  // execTargetOrder 是后端解析后的本端派发顺序（R14），执行目标列表的内容就是它；
  // 不给的话列表停在骨架态（顺序数据尚未到达），只断言其它区块的用例不受影响。
  execTargetOrder: Array<{ agentBackendId: number; available: boolean }> = [],
) {
  // 面板渲染即调 GetBackendCapabilities（经 useBackendCapabilities）。
  // 该 binding 走真实 wailsjs App.js（读 window.go.app.App.*），故测试
  // 把返回挂到 window.go 上覆盖默认空能力（与 chat-panel 等既有模式一致）。
  // 技能区在 caps 含 "skills" 时挂载即拉目录，故 packs 可注入 globallyEnabled 等。
  window.go = {
    app: {
      App: {
        GetBackendCapabilities: vi.fn().mockResolvedValue({
          capabilities: caps,
          permissionModeMeta: null,
        }),
        ListAgentSkillPacks: vi.fn().mockResolvedValue({ packs }),
        ListAgentExecTargetAvailability: vi
          .fn()
          .mockResolvedValue(
            execTargetOrder.map((it) => ({ reason: "", hint: "", ...it })),
          ),
      },
    },
  };
}

beforeEach(() => {
  // 默认空能力 + 空技能目录，保证未显式 withCaps 的面板测试也能渲染
  // （否则真实 App.js 在 window.go 缺失时抛 Cannot read 'app'）。
  withCaps([]);
});

afterEach(() => {
  delete window.go;
});

describe("OrgDetailAgent", () => {
  it("does not render role or initials inputs", () => {
    renderPanel();
    expect(screen.queryByLabelText("agent-role")).toBeNull();
    expect(screen.queryByLabelText("agent-initials")).toBeNull();
  });

  it("renders only the four design-aligned basic fields", () => {
    renderPanel();
    expect(screen.getByLabelText("Name")).toBeInTheDocument();
    expect(screen.getByLabelText("Description")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Upload Image" }),
    ).toBeInTheDocument();
    expect(screen.getByText("PNG / JPG / WEBP · Max 2 MB")).toBeInTheDocument();
    expect(
      screen.getByRole("radiogroup", { name: "Avatar Color" }),
    ).toBeInTheDocument();
    expect(
      screen.getAllByRole("radio", { name: /Avatar color agent-/ }),
    ).toHaveLength(16);
    expect(
      screen.getByRole("radio", { name: "Avatar color agent-16" }),
    ).toBeInTheDocument();
    // 头像编辑入口仍用于选择图标 / 字母备用头像。
    expect(screen.getAllByLabelText("Change avatar").length).toBeGreaterThan(0);
  });

  it("keeps image upload out of the avatar popover", async () => {
    const user = userEvent.setup();
    renderPanel();
    await user.click(screen.getAllByLabelText("Change avatar")[0]);
    expect(
      await screen.findByRole("tab", { name: /Icon/ }),
    ).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: /Letter/ })).toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: /Image/ })).toBeNull();
  });

  it("renders the execution targets section as a single-target row (R15/R20)", async () => {
    withCaps([], [], [{ agentBackendId: 5, available: true }]);
    renderPanel({ agentBackendId: 5 }, [backend()]);
    expect(
      screen.getByRole("heading", { name: "Execution Targets" }),
    ).toBeInTheDocument();
    // 单档：没有序号、机器标为"本机"，第二行是"类型 · 名称"。
    expect(await screen.findByText("Local machine")).toBeInTheDocument();
    expect(screen.getByText(/Claude Code · Claude Code/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Replace/ })).toBeInTheDocument();
  });

  it("falls back to the Agent backend summary when the backend list is empty", async () => {
    withCaps([], [], [{ agentBackendId: 5, available: true }]);
    renderPanel({
      agentBackendId: 5,
      backend: {
        id: 5,
        type: "claudecode",
        name: "Claude Code",
        llmProviderName: "Anthropic 官方",
        llmProviderModel: "Sonnet 4.6",
        llmProviderActive: true,
      },
    });
    expect(await screen.findByText("Local machine")).toBeInTheDocument();
    expect(screen.getByText(/Claude Code · Claude Code/)).toBeInTheDocument();
  });

  // 决策 11：不写「向用户解释我们设计」的说明文字，systemPromptHint 点名要删。
  // 「风格 / 语气 / 边界都写进 system prompt —— 不另设字段」讲的是我们没做什么，
  // 用户此刻做决定用不上它；约束该由控件表达。保留的是当下要用的信息（离线说明、
  // 不可用原因、部门删除两策略），它们各有自己的守卫。
  it("Given the behavior column, When it renders, Then the system prompt carries no design-explaining hint", () => {
    renderPanel();
    expect(screen.getByLabelText("System Prompt")).toBeInTheDocument();
    expect(screen.queryByText(/No separate fields are used/)).toBeNull();
    expect(
      screen.queryByText(/Put style, tone, and boundaries/),
    ).toBeNull();
  });

  it("counts non-whitespace chars in system prompt", async () => {
    const user = userEvent.setup();
    renderPanel({ prompt: [] });
    const ta = screen.getByLabelText("System Prompt");
    await user.type(ta, "hello 世界");
    expect(screen.getByText(/7 chars/)).toBeInTheDocument();
  });

  it("uploads avatar from the inline detail row control", async () => {
    const { onUploadAvatar } = renderPanel();
    expect(
      screen.getByRole("button", { name: "Upload Image" }),
    ).toBeInTheDocument();
    const file = new File([new Uint8Array([1, 2, 3])], "a.png", {
      type: "image/png",
    });
    const input = document.querySelector(
      'input[type="file"]',
    ) as HTMLInputElement | null;
    if (!input) throw new Error("file input not found");
    fireEvent.change(input, { target: { files: [file] } });
    await new Promise((r) => setTimeout(r, 50));
    expect(onUploadAvatar).toHaveBeenCalledWith({
      id: 7,
      dataUrl: expect.stringMatching(/^data:image\/png;base64,/),
    });
  });

  it("deletes an uploaded avatar from the inline detail row control", async () => {
    const user = userEvent.setup();
    const { onDeleteAvatar } = renderPanel({
      avatarDataUrl: "data:image/png;base64,aGVsbG8=",
    });
    await user.click(
      screen.getByRole("button", { name: "Delete uploaded avatar" }),
    );
    expect(onDeleteAvatar).toHaveBeenCalledWith({ id: 7 });
  });

  it("rejects inline avatar files over 2MB before upload", async () => {
    const { onUploadAvatar } = renderPanel();
    const file = new File([new Uint8Array(2 * 1024 * 1024 + 1)], "large.png", {
      type: "image/png",
    });
    const input = document.querySelector(
      'input[type="file"]',
    ) as HTMLInputElement | null;
    if (!input) throw new Error("file input not found");
    fireEvent.change(input, { target: { files: [file] } });
    expect(
      await screen.findByText("Image is too large. Upload a file under 2 MB."),
    ).toBeInTheDocument();
    expect(onUploadAvatar).not.toHaveBeenCalled();
  });

  // 技能不再是独立的一节：它折在执行目标那一行里（单档默认展开），所以断言从
  // 「有没有技能区标题」改成「这一行里能不能管技能」——判据没变，位置变了。
  it("folds the skills of a claudecode target into its own execution-target row (CapSkills)", async () => {
    withCaps(["skills", "mcp_tools"]);
    renderPanel({ agentBackendId: 5 }, [
      backend({ id: 5, type: "claudecode" }),
    ]);
    const row = await screen.findByTestId("exec-target-row-0");
    expect(
      await within(row).findByRole("button", { name: /Skills/ }),
    ).toHaveAttribute("aria-expanded", "true");
    expect(
      within(row).getByRole("button", { name: "Manage skills" }),
    ).toBeInTheDocument();
    expect(screen.queryByText("Skills · Skill Packs")).toBeNull();
  });

  it("shows the gating box for a non-claudecode backend", async () => {
    withCaps(["mcp_tools"]); // codex: no skills cap
    renderPanel({ agentBackendId: 6 }, [backend({ id: 6, type: "codex" })]);
    expect(
      await screen.findByText("This backend doesn't support skills"),
    ).toBeInTheDocument();
  });

  it("granted skill chips render derived names from pack ids", async () => {
    withCaps(["skills", "mcp_tools"]);
    renderPanel({ agentBackendId: 5 }, [
      backend({ id: 5, type: "claudecode" }),
    ]);
    expect(await screen.findByText("superpowers")).toBeInTheDocument();
    expect(screen.getByText("frontend-design")).toBeInTheDocument();
  });

  it("renders the Tools section as one list with no second entry point (no switch, no Add Tool dialog)", async () => {
    withCaps(["skills", "mcp_tools"]);
    renderPanel(
      { tools: [], agentBackendId: 5 },
      [backend({ id: 5, type: "claudecode" })],
      ["org"],
    );
    expect(await screen.findByText("Tools · TOOLS")).toBeInTheDocument();
    expect(
      await screen.findByRole("list", { name: "Tools" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add Tool" })).toBeNull();
    expect(screen.queryByRole("switch", { name: /Org Structure/i })).toBeNull();
  });

  it("grants the org tool from its row in the tool list and saves it", async () => {
    withCaps(["skills", "mcp_tools"]);
    const user = userEvent.setup();
    const { onUpdate } = renderPanel(
      { tools: [], agentBackendId: 5 },
      [backend({ id: 5, type: "claudecode" })],
      ["org"],
    );
    await user.click(
      await screen.findByRole("button", { name: "Grant Org Structure" }),
    );
    await waitFor(() => expect(onUpdate).toHaveBeenCalled());
    expect(onUpdate).toHaveBeenCalledWith(
      expect.objectContaining({
        tools: expect.arrayContaining([
          expect.objectContaining({ key: "org", enabled: true }),
        ]),
      }),
    );
  });

  it("renders the Org Structure chip when the org tool is enabled", async () => {
    withCaps(["skills", "mcp_tools"]);
    renderPanel(
      { tools: [{ key: "org", enabled: true }], agentBackendId: 5 },
      [backend({ id: 5, type: "claudecode" })],
      ["org"],
    );
    await screen.findByText("Tools · TOOLS");
    expect(screen.getByText("Org Structure")).toBeInTheDocument();
  });

  it("renders a globally-on pack as a locked (non-removable) inherited chip", async () => {
    withCaps(
      ["skills"],
      [
        {
          id: "global-on@m",
          name: "global-on",
          description: "globally enabled pack",
          skills: ["a", "b"],
          source: "m",
          recommended: false,
          installed: true,
          enabled: true,
          globallyEnabled: true,
        },
      ],
    );
    // agent 本地无 override：芯片纯由全局已启用 overlay 派生 → 锁定不可移除。
    renderPanel({ agentBackendId: 5, skills: [] }, [
      backend({ id: 5, type: "claudecode" }),
    ]);
    const chip = await screen.findByText("global-on");
    expect(chip).toBeInTheDocument();
    // 继承芯片无移除按钮（locked）。
    expect(
      screen.queryByRole("button", { name: "Remove global-on" }),
    ).toBeNull();
  });

  it("renders the reports-to badge avatar at a legible size (size-5, not the cramped size-3.5)", () => {
    const target = agent({ id: 99, name: "Boss", avatarColor: "agent-3" });
    render(
      <MemoryRouter initialEntries={["/org"]}>
        <OrgDetailAgent
          agent={agent({ parentAgentId: 99 })}
          departments={[dept]}
          agents={[target]}
          backends={[]}
          isLeadOf={null}
          availableTools={[]}
          onUpdate={vi.fn().mockResolvedValue(undefined)}
          onMove={vi.fn().mockResolvedValue(undefined)}
          onDelete={vi.fn().mockResolvedValue(undefined)}
          onUploadAvatar={vi.fn().mockResolvedValue(undefined)}
          onDeleteAvatar={vi.fn().mockResolvedValue(undefined)}
          onClose={vi.fn()}
        />
      </MemoryRouter>,
    );
    // 汇报给徽章里的目标头像（role=img, aria-label=目标名）必须 ≥ 行高 (16px)，
    // 否则字母/图标被挤成 8px 糊点。size-3.5(14px) 是历史遗留的过小值。
    const avatar = screen.getByRole("img", { name: "Boss" });
    expect(avatar.className).toContain("size-5");
    expect(avatar.className).not.toContain("size-3.5");
  });

  it("forces a globally-off installed pack to off and saves enabled:false", async () => {
    withCaps(
      ["skills"],
      [
        {
          id: "global-off@m",
          name: "global-off",
          description: "installed, globally disabled",
          skills: ["c"],
          source: "m",
          recommended: false,
          installed: true,
          enabled: false,
          globallyEnabled: false,
        },
      ],
    );
    const user = userEvent.setup();
    const { onUpdate } = renderPanel({ agentBackendId: 5, skills: [] }, [
      backend({ id: 5, type: "claudecode" }),
    ]);
    await user.click(
      await screen.findByRole("button", { name: "Manage skills" }),
    );
    // 三态行的「Off」分段：把该 installed pack 强制关。
    await user.click(await screen.findByRole("button", { name: "Off" }));
    await user.click(screen.getByText("Done"));
    await waitFor(() => expect(onUpdate).toHaveBeenCalled());
    expect(onUpdate).toHaveBeenCalledWith(
      expect.objectContaining({
        execTargets: expect.arrayContaining([
          expect.objectContaining({
            agentBackendId: 5,
            skills: expect.arrayContaining([
              expect.objectContaining({ id: "global-off@m", enabled: false }),
            ]),
          }),
        ]),
      }),
    );
  });

  describe("OrgDetailAgent setup guidance (task 8)", () => {
    it("shows a no-backend status alert when no backend is bound", () => {
      renderPanel({ agentBackendId: 0 }, []);
      expect(screen.getByText("This agent can't chat yet")).toBeInTheDocument();
      expect(
        screen.getByText(
          /Bind an agent backend to start chatting; the built-in backend also needs an LLM provider\./,
        ),
      ).toBeInTheDocument();
      // 执行目标列表保持在原位（R15：重做成有序列表，节保留在原位置)。
      expect(
        screen.getByRole("heading", { name: "Execution Targets" }),
      ).toBeInTheDocument();
    });

    it("hides the no-backend alert once a backend is selected", () => {
      renderPanel({ agentBackendId: 5 }, [backend()]);
      expect(screen.queryByText("This agent can't chat yet")).toBeNull();
    });

    it("shows a provider-gap alert for a builtin backend with no usable provider", () => {
      renderPanel({ agentBackendId: 8 }, [
        backend({
          id: 8,
          type: "builtin",
          llmProviderName: "",
          llmProviderActive: false,
        }),
      ]);
      expect(
        screen.getByText("An LLM provider is still needed"),
      ).toBeInTheDocument();
      expect(
        screen.getByText(
          /The built-in SDK backend runs tasks through an LLM provider, but no active provider is linked yet\./,
        ),
      ).toBeInTheDocument();
      expect(
        screen.getByRole("button", { name: "Configure LLM providers" }),
      ).toBeInTheDocument();
      expect(
        screen.getByRole("button", { name: "Go to Agent backend settings" }),
      ).toBeInTheDocument();
    });

    it("hides the provider-gap alert once a builtin backend has an active provider linked", () => {
      renderPanel({ agentBackendId: 8 }, [
        backend({
          id: 8,
          type: "builtin",
          llmProviderName: "Anthropic",
          llmProviderActive: true,
        }),
      ]);
      expect(screen.queryByText("An LLM provider is still needed")).toBeNull();
      expect(screen.queryByText("This agent can't chat yet")).toBeNull();
    });

    it("keeps the no-backend alert when the selected backend is a CLI type (no provider required)", () => {
      renderPanel({ agentBackendId: 5 }, [
        backend({ id: 5, type: "claudecode" }),
      ]);
      expect(screen.queryByText("This agent can't chat yet")).toBeNull();
      expect(screen.queryByText("An LLM provider is still needed")).toBeNull();
    });

    it("navigates to settings llm-providers from the configure-provider button", async () => {
      const user = userEvent.setup();
      renderPanel({ agentBackendId: 8 }, [
        backend({
          id: 8,
          type: "builtin",
          llmProviderName: "",
          llmProviderActive: false,
        }),
      ]);
      await user.click(
        screen.getByRole("button", { name: "Configure LLM providers" }),
      );
      expect(screen.getByTestId("location-state")).toHaveTextContent(
        "llm-providers",
      );
    });

    it("navigates to settings agent-backend from the Go to Agent backend settings link", async () => {
      const user = userEvent.setup();
      renderPanel({ agentBackendId: 8 }, [
        backend({
          id: 8,
          type: "builtin",
          llmProviderName: "",
          llmProviderActive: false,
        }),
      ]);
      await user.click(
        screen.getByRole("button", { name: "Go to Agent backend settings" }),
      );
      expect(screen.getByTestId("location-state")).toHaveTextContent(
        "agent-backend",
      );
    });
  });

  describe("OrgDetailAgent auto-save", () => {
    it("removes the cancel and save footer buttons", () => {
      renderPanel();
      expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
      expect(screen.queryByRole("button", { name: "Cancel" })).toBeNull();
    });

    it("saves the name on blur", async () => {
      // R15: 保存要求至少一个执行目标，这里显式配一个，测的是「改名会保存」这件事本身。
      const { onUpdate } = renderPanel({ agentBackendId: 5 }, [backend()]);
      const input = screen.getByLabelText("Name");
      fireEvent.change(input, { target: { value: "Boris 二号" } });
      fireEvent.blur(input);
      await waitFor(() => expect(onUpdate).toHaveBeenCalled());
      expect(onUpdate).toHaveBeenCalledWith(
        expect.objectContaining({ name: "Boris 二号" }),
      );
    });
  });
});
