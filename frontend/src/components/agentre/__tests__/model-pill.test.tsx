/**
 * model-pill.test.tsx — 会话 LLM ModelTarget 选择器（规格 2026-08-09 决策 4/5 +
 * 2026-08-10 决策 10 + 2026-08-11「新建与已有会话流程」）。
 *
 * 覆盖：只列兼容供应商（builtin→全部 / claudecode→anthropic / codex→openai-response /
 * piagent→anthropic+openai-chat+openai-response）；未绑 agent（CLI 登录态）也显示；
 * 新建会话瞬态选择 provider-default / fixed-model / 点「跟随 agent 绑定」清回；列表
 * 加载中 → pill 禁用；拉取失败 → 弹层底部错误行；已有会话（sessionId>0）选择立即持久化
 * （SetChatSessionModelTarget，providerKey + modelKey）；切换失败回滚；不可切换时 pill
 * 常显但 disabled + tooltip 说明原因（openclaw / 无兼容供应商），不隐藏（决策 10）。
 */

import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  TooltipProvider,
  TranscriptRenderContext,
  TranscriptRowView,
} from "@agentre-hub/agentre-ui";
import i18n from "@/i18n";

const appMocks = vi.hoisted(() => ({
  ListLLMProviders: vi.fn(),
  ListLLMModels: vi.fn().mockResolvedValue({ items: [] }),
  SetChatSessionModelTarget: vi.fn(),
  RemoteDeviceFingerprint: vi.fn().mockResolvedValue("sha256:local-desktop"),
  RemoteDeviceList: vi.fn().mockResolvedValue([]),
  RemoteDeviceListProviders: vi.fn().mockResolvedValue([]),
  RemoteDeviceSyncProvider: vi.fn().mockResolvedValue(undefined),
}));

vi.mock("../../../../wailsjs/go/app/App", () => appMocks);

import {
  isProviderCompatible,
  isProviderSelectableBackend,
  ProviderPill,
  useProviderPill,
} from "../model-pill";
import { recentStorageKey } from "../model-target-picker/recents";
import type { UseProviderPillOptions } from "../model-pill";
import type { TranscriptRow } from "@agentre-hub/agentre-ui";

beforeEach(() => {
  vi.clearAllMocks();
  appMocks.ListLLMProviders.mockResolvedValue({ items: [] });
  appMocks.ListLLMModels.mockResolvedValue({ items: [] });
  appMocks.RemoteDeviceFingerprint.mockResolvedValue("sha256:local-desktop");
  appMocks.RemoteDeviceList.mockResolvedValue([]);
  appMocks.RemoteDeviceListProviders.mockResolvedValue([]);
  appMocks.RemoteDeviceSyncProvider.mockResolvedValue(undefined);
});

function Harness(props: UseProviderPillOptions) {
  const pill = useProviderPill(props);
  return <ProviderPill {...pill} />;
}

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

function providerDefaultOption(listbox: HTMLElement, index = 0) {
  return within(listbox).getAllByRole("option", {
    name: /Follow this provider's default/i,
  })[index];
}

type SwitchNotice = {
  providerKey: string;
  providerName?: string;
  modelKey?: string;
  modelName?: string;
};

function renderSwitchNotice(block: SwitchNotice) {
  const row = {
    key: "message:9:notice:0",
    messageId: 9,
    message: {
      id: 9,
      role: "assistant",
      blocks: [{ type: "notice", noticeKind: "switch", ...block }],
      createtime: 0,
      durationMs: 0,
      model: "",
      promptTokens: 0,
      completionTokens: 0,
      cachedTokens: 0,
      cacheCreationTokens: 0,
      reasoningTokens: 0,
    },
    item: {
      type: "notice",
      uiStateKey: "message:9:notice:0",
      block: { type: "notice", noticeKind: "switch", ...block },
    },
    isFirstOfMessage: true,
    isLastOfMessage: true,
    autonomous: false,
  } as unknown as TranscriptRow;

  render(
    <TooltipProvider>
      <TranscriptRenderContext.Provider
        value={{ agentName: "Agentre", agentAvatar: <span />, sessionId: 42 }}
      >
        <TranscriptRowView
          row={row}
          liveTail=""
          liveBlocks={undefined}
          liveRetry={null}
          showIndicator={false}
          compacting={false}
          reconnecting={false}
        />
      </TranscriptRenderContext.Provider>
    </TooltipProvider>,
  );
}

const ANTHROPIC_PROVIDER = {
  id: 1,
  providerKey: "acme-anthropic",
  name: "Acme Claude",
  type: "anthropic",
  enabled: true,
  defaultModelKey: "mk-sonnet",
};

const CHAT_PROVIDER = {
  id: 2,
  providerKey: "acme-chat",
  name: "Acme Chat",
  type: "openai-chat",
  enabled: true,
  defaultModelKey: "mk-gpt",
};

const RESPONSE_PROVIDER = {
  id: 3,
  providerKey: "acme-response",
  name: "Acme Resp",
  type: "openai-response",
  enabled: true,
  defaultModelKey: "mk-gpt5-default",
};

const ALL_PROVIDERS = [ANTHROPIC_PROVIDER, CHAT_PROVIDER, RESPONSE_PROVIDER];

function model(key: string, modelId: string, enabled = true) {
  return { modelKey: key, modelId, name: modelId, enabled };
}

// ── 远端 daemon 门控（gap 1：chat Picker 接收远端能力/目录）──────────────────────

function device(overrides: Record<string, unknown> = {}) {
  return {
    id: 7,
    name: "Build box",
    online: true,
    supportsLLMModelTarget: true,
    ...overrides,
  };
}

function remoteModel(key: string, modelId: string, enabled = true) {
  return { key, modelId, name: modelId, enabled };
}

function remoteProvider(overrides: Record<string, unknown> = {}) {
  return {
    key: "acme-anthropic",
    name: "Acme Claude",
    type: "anthropic",
    defaultModelKey: "mk-sonnet",
    models: [remoteModel("mk-sonnet", "claude-sonnet-4-5")],
    ...overrides,
  };
}

describe("provider compatibility gate（与后端 ProviderTypeMatch 对齐）", () => {
  it("builtin → 全部供应商兼容", () => {
    expect(isProviderCompatible("builtin", "anthropic")).toBe(true);
    expect(isProviderCompatible("builtin", "openai-chat")).toBe(true);
    expect(isProviderCompatible("builtin", "openai-response")).toBe(true);
  });

  it("claudecode → 仅 anthropic", () => {
    expect(isProviderCompatible("claudecode", "anthropic")).toBe(true);
    expect(isProviderCompatible("claudecode", "openai-chat")).toBe(false);
    expect(isProviderCompatible("claudecode", "openai-response")).toBe(false);
  });

  it("codex → 仅 openai-response", () => {
    expect(isProviderCompatible("codex", "openai-response")).toBe(true);
    expect(isProviderCompatible("codex", "anthropic")).toBe(false);
    expect(isProviderCompatible("codex", "openai-chat")).toBe(false);
  });

  it("piagent → anthropic / openai-chat / openai-response", () => {
    expect(isProviderCompatible("piagent", "anthropic")).toBe(true);
    expect(isProviderCompatible("piagent", "openai-chat")).toBe(true);
    expect(isProviderCompatible("piagent", "openai-response")).toBe(true);
  });

  it("openclaw 不渲染供应商选择器", () => {
    expect(isProviderSelectableBackend("openclaw")).toBe(false);
    expect(isProviderCompatible("openclaw", "anthropic")).toBe(false);
    expect(isProviderSelectableBackend("")).toBe(false);
  });

  it("builtin / claudecode / codex / piagent 均可选供应商", () => {
    expect(isProviderSelectableBackend("builtin")).toBe(true);
    expect(isProviderSelectableBackend("claudecode")).toBe(true);
    expect(isProviderSelectableBackend("codex")).toBe(true);
    expect(isProviderSelectableBackend("piagent")).toBe(true);
  });
});

describe("ProviderPill · 新建会话 ModelTarget 选择器", () => {
  it("只列兼容供应商：claudecode 弹层只显示 anthropic，不列 openai-chat / openai-response", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({ items: ALL_PROVIDERS });
    render(<Harness backendType="claudecode" />);

    const pill = await waitFor(() => screen.getByTestId("provider-pill"));
    await waitFor(() => expect(pill).not.toBeDisabled());

    const user = userEvent.setup();
    await user.click(pill);

    const listbox = screen.getByRole("listbox");
    expect(providerDefaultOption(listbox)).toBeInTheDocument();
    expect(within(listbox).getByText("Acme Claude")).toBeInTheDocument();
    expect(within(listbox).queryByText("Acme Chat")).not.toBeInTheDocument();
    expect(within(listbox).queryByText("Acme Resp")).not.toBeInTheDocument();
  });

  it("builtin 弹层列出全部供应商", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({ items: ALL_PROVIDERS });
    render(<Harness backendType="builtin" />);

    const pill = await waitFor(() => screen.getByTestId("provider-pill"));
    await waitFor(() => expect(pill).not.toBeDisabled());

    const user = userEvent.setup();
    await user.click(pill);

    const listbox = screen.getByRole("listbox");
    expect(
      within(listbox).getAllByRole("option", {
        name: /Follow this provider's default/i,
      }),
    ).toHaveLength(3);
    expect(within(listbox).getByText("Acme Claude")).toBeInTheDocument();
    expect(within(listbox).getByText("Acme Chat")).toBeInTheDocument();
    expect(within(listbox).getByText("Acme Resp")).toBeInTheDocument();
  });

  it("未绑 agent（CLI 登录态）也显示选择器，顶部特殊项为「跟随 agent 绑定」", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({ items: ALL_PROVIDERS });
    render(<Harness backendType="piagent" boundProviderKey="" />);

    const pill = await waitFor(() => screen.getByTestId("provider-pill"));
    await waitFor(() => expect(pill).not.toBeDisabled());

    // 未选时 pill 标签显示顶部特殊项「跟随 agent 绑定」。
    expect(pill).toHaveTextContent("Follow agent binding");

    const user = userEvent.setup();
    await user.click(pill);

    const listbox = screen.getByRole("listbox");
    // 未选时「跟随 agent 绑定」项高亮（aria-selected）。
    const follow = within(listbox).getByRole("option", {
      name: /Follow agent binding/i,
    });
    expect(follow).toHaveAttribute("aria-selected", "true");
    // 兼容供应商仍全部列出。
    expect(
      within(listbox).getAllByRole("option", {
        name: /Follow this provider's default/i,
      }),
    ).toHaveLength(3);
    expect(within(listbox).getByText("Acme Claude")).toBeInTheDocument();
    expect(within(listbox).getByText("Acme Chat")).toBeInTheDocument();
    expect(within(listbox).getByText("Acme Resp")).toBeInTheDocument();
  });

  it("选中 provider-default → pill 显示供应商名 · 当前默认模型；点「跟随 agent 绑定」清回", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({ items: ALL_PROVIDERS });
    appMocks.ListLLMModels.mockResolvedValue({
      items: [
        model("mk-gpt5-default", "gpt-5"),
        model("mk-fixed", "gpt-5-codex"),
      ],
    });
    render(<Harness backendType="codex" />);

    const pill = await waitFor(() => screen.getByTestId("provider-pill"));
    await waitFor(() => expect(pill).not.toBeDisabled());

    const user = userEvent.setup();
    await user.click(pill);
    // provider-default 项（label=Acme Resp + sublabel=gpt-5 + Default 徽标）与 fixed
    // 项（gpt-5-codex）都含 "Acme Resp"：用 sublabel 区分，选 provider-default。
    const providerDefault = within(screen.getByRole("listbox"))
      .getAllByRole("option")
      .find(
        (o) =>
          o.textContent?.includes("gpt-5") &&
          !o.textContent?.includes("gpt-5-codex"),
      );
    expect(providerDefault).toBeDefined();
    await user.click(providerDefault as HTMLElement);

    // provider-default 摘要写的是「实际会跑哪个模型」：供应商身份交给品牌标识，
    // 脸上只留解析出的模型 ID（mockup ?view=chat 四态样品）。
    expect(screen.getByTestId("provider-pill")).toHaveTextContent("gpt-5");

    // 点击「跟随 agent 绑定」清回（无瞬态选择）。
    await user.click(screen.getByTestId("provider-pill"));
    await user.click(
      within(screen.getByRole("listbox")).getByRole("option", {
        name: /Follow agent binding/i,
      }),
    );
    expect(screen.getByTestId("provider-pill")).toHaveTextContent(
      "Follow agent binding",
    );
  });

  it("选中 fixed-model → pill 显示解析出的模型 ID", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({ items: ALL_PROVIDERS });
    appMocks.ListLLMModels.mockResolvedValue({
      items: [
        model("mk-gpt5-default", "gpt-5"),
        model("mk-fixed", "gpt-5-codex"),
      ],
    });
    render(<Harness backendType="codex" />);

    const pill = await waitFor(() => screen.getByTestId("provider-pill"));
    await waitFor(() => expect(pill).not.toBeDisabled());

    const user = userEvent.setup();
    await user.click(pill);
    await user.click(
      within(screen.getByRole("listbox")).getByRole("option", {
        name: /gpt-5-codex/,
      }),
    );

    expect(screen.getByTestId("provider-pill")).toHaveTextContent(
      "gpt-5-codex",
    );
  });

  it("列表加载中 → pill 禁用；加载完成 → 可用", async () => {
    const slow = deferred<{ items: unknown[] }>();
    appMocks.ListLLMProviders.mockReturnValue(slow.promise);
    render(<Harness backendType="claudecode" />);

    expect(screen.getByTestId("provider-pill")).toBeDisabled();

    act(() => {
      slow.resolve({ items: [ANTHROPIC_PROVIDER] });
    });
    await waitFor(() =>
      expect(screen.getByTestId("provider-pill")).not.toBeDisabled(),
    );
  });

  it("拉取失败 → 弹层底部错误行", async () => {
    appMocks.ListLLMProviders.mockRejectedValue(new Error("boom"));
    render(<Harness backendType="claudecode" />);

    const pill = await waitFor(() => screen.getByTestId("provider-pill"));
    await waitFor(() => expect(pill).not.toBeDisabled());

    const user = userEvent.setup();
    await user.click(pill);

    expect(
      screen.getByText("Failed to load providers. Please try again."),
    ).toBeInTheDocument();
  });

  it("openclaw backend 不拉取供应商；pill 常显但 disabled + tooltip 说明原因（决策 10：禁用而非隐藏）", () => {
    render(<Harness backendType="openclaw" />);

    const pill = screen.getByTestId("provider-pill");
    expect(pill).toBeDisabled();
    expect(pill).toHaveAttribute(
      "title",
      "This backend does not use agentre providers",
    );
    expect(appMocks.ListLLMProviders).not.toHaveBeenCalled();
  });

  it("无兼容供应商（列表拉取成功但为空）→ pill disabled + tooltip 说明原因", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({ items: [CHAT_PROVIDER] });
    render(<Harness backendType="claudecode" />);

    const pill = await waitFor(() => screen.getByTestId("provider-pill"));
    await waitFor(() => expect(pill).toBeDisabled());
    expect(pill).toHaveAttribute(
      "title",
      "No available provider matches this backend type",
    );
  });

  it("弹层底部常显「自下一轮生效」说明（不随轮状态变化）", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({ items: ALL_PROVIDERS });
    render(<Harness backendType="builtin" />);

    const pill = await waitFor(() => screen.getByTestId("provider-pill"));
    await waitFor(() => expect(pill).not.toBeDisabled());

    const user = userEvent.setup();
    await user.click(pill);

    expect(
      screen.getByText(
        "Switching takes effect from the next turn; the turn in progress is unaffected",
      ),
    ).toBeInTheDocument();
  });

  it("chat 弹层不再总述 ↻ 图例（会不会变已下放到每行副行），只留底部「自下一轮生效」（何时生效）", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({ items: ALL_PROVIDERS });
    render(<Harness backendType="builtin" />);

    const pill = await waitFor(() => screen.getByTestId("provider-pill"));
    await waitFor(() => expect(pill).not.toBeDisabled());

    const user = userEvent.setup();
    await user.click(pill);

    // 「跟随默认会不会变」这件事已经收进每一行 provider-default 的副行（「当前 <模型>」）
    // + ↻ 标记，弹层顶部不再总述一遍图例。
    expect(screen.queryByTestId("dynamic-legend")).not.toBeInTheDocument();
    // 「何时生效」是另一件事，没有别处替它说 —— 底部这条必须留着，且留在列表之后。
    const switchNote = screen.getByText(
      "Switching takes effect from the next turn; the turn in progress is unaffected",
    );
    const list = screen.getByRole("listbox");
    expect(
      list.compareDocumentPosition(switchNote) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("Given a new session follows an agent provider without a model key, when the catalog resolves a default, then the pill promises only that provider", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({
      items: [ANTHROPIC_PROVIDER],
    });
    appMocks.ListLLMModels.mockResolvedValue({
      items: [model("mk-sonnet", "claude-sonnet-4-5")],
    });

    render(
      <Harness backendType="claudecode" boundProviderKey="acme-anthropic" />,
    );

    const pill = await screen.findByTestId("provider-pill");
    await waitFor(() => expect(pill).not.toBeDisabled());

    expect(pill).toHaveTextContent("Follow agent binding");
    expect(pill).toHaveTextContent("Acme Claude");
    expect(pill).not.toHaveTextContent("claude-sonnet-4-5");
    const followAgentIcon = screen.getByTestId("follow-agent-icon");
    expect(followAgentIcon).toBeInTheDocument();
    // Mockup uses a plain person silhouette (no brand/VCS glyph) for the
    // follow-agent state — must not render lucide's GitBranch icon.
    expect(followAgentIcon).toHaveClass("lucide-user-round");
    expect(followAgentIcon).not.toHaveClass("lucide-git-branch");
  });
});

describe("ProviderPill · Composer 四态（mockup ?view=chat：单行，脸上写实际会跑哪个模型）", () => {
  it("Given an existing session follows an agent fixed binding, when the bound model resolves, then the pill shows the follow marker and resolved model on one line", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({
      items: [ANTHROPIC_PROVIDER],
    });
    appMocks.ListLLMModels.mockResolvedValue({
      items: [model("mk-sonnet", "claude-sonnet-4-5")],
    });

    render(
      <Harness
        backendType="claudecode"
        sessionId={42}
        boundProviderKey="acme-anthropic"
        boundModelKey="mk-sonnet"
      />,
    );

    const pill = await screen.findByTestId("provider-pill");
    await waitFor(() => expect(pill).toHaveTextContent("claude-sonnet-4-5"));
    // 单行：模式前缀 + 解析出的模型 ID 挤在同一行，不再有第二行副标题。
    expect(pill).toHaveTextContent("Follow agent binding · claude-sonnet-4-5");
    expect(
      within(pill).queryByTestId("model-target-trigger-sub"),
    ).not.toBeInTheDocument();
    // 26px 单行高度必须真的赢过 Picker 触发按钮的默认 h-9（cn 走 tailwind-merge）。
    expect(pill).toHaveClass("h-[26px]");
    expect(pill).not.toHaveClass("h-9");
    expect(pill).not.toHaveClass("min-h-9");
    // 未选态同样是中性 .pill：不许出现常亮的高亮边框/文字。
    expect(pill).toHaveClass(
      "cursor-pointer",
      "border-border",
      "bg-card",
      "text-foreground",
    );
    expect(pill).not.toHaveClass("bg-muted");
    // 可点控件要有 hover 反馈，但只许动背景 —— 边框变色是用户明确否掉的那种反馈。
    expect(pill).toHaveClass("hover:bg-accent");
    expect(pill.className).not.toMatch(/hover:border-/);
    // focus-visible ring 是键盘用户的唯一定位手段，不能被 hover 改动顺手抹掉。
    expect(pill).toHaveClass("focus-visible:ring-2");
    expect(screen.getByTestId("follow-agent-icon")).toBeInTheDocument();
    // 跟随 Agent 态由人形图标表达，不挂供应商品牌标识（mockup 四态样品第一格）。
    expect(within(pill).queryByRole("img")).not.toBeInTheDocument();
  });

  it("Given an existing session follows an agent provider default, when the default resolves, then the pill shows the resolved model with a dynamic marker", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({
      items: [ANTHROPIC_PROVIDER],
    });
    appMocks.ListLLMModels.mockResolvedValue({
      items: [model("mk-sonnet", "claude-sonnet-4-5")],
    });

    render(
      <Harness
        backendType="claudecode"
        sessionId={42}
        boundProviderKey="acme-anthropic"
        boundModelKey=""
      />,
    );

    const pill = await screen.findByTestId("provider-pill");
    await waitFor(() => expect(pill).toHaveTextContent("claude-sonnet-4-5"));
    expect(pill).toHaveTextContent("Follow agent binding");
    expect(
      screen.getByTestId("provider-pill-dynamic-icon"),
    ).toBeInTheDocument();
  });

  it("Given provider-default is selected, when the default resolves, then the pill shows the model logo and dynamic marker", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({ items: [RESPONSE_PROVIDER] });
    appMocks.ListLLMModels.mockResolvedValue({
      items: [
        model("mk-gpt5-default", "gpt-5"),
        model("mk-fixed", "gpt-5-codex"),
      ],
    });

    render(
      <Harness
        backendType="codex"
        sessionId={42}
        persistedProviderKey="acme-response"
      />,
    );

    const pill = await screen.findByTestId("provider-pill");
    await waitFor(() => expect(pill).toHaveTextContent("gpt-5"));
    // 模式改由 ↻ 表达，模式名不再占 pill 的脸；供应商身份交给品牌标识。
    expect(pill).not.toHaveTextContent("Follow provider default");
    expect(pill).not.toHaveTextContent("Acme Resp");
    expect(
      screen.getByTestId("provider-pill-dynamic-icon"),
    ).toBeInTheDocument();
    expect(
      within(pill).getByRole("img", { name: "OpenAI" }),
    ).toBeInTheDocument();
    expect(
      within(pill).queryByTestId("model-target-trigger-sub"),
    ).not.toBeInTheDocument();
    // 选中态不再靠常亮描边宣示自己：一律中性 .pill 配色，模式交给图标与 ↻ 表达。
    expect(pill).toHaveClass("border-border", "bg-card", "text-foreground");
    expect(pill).not.toHaveClass("border-primary-text/60");
    expect(pill).not.toHaveClass("text-primary-text");
    expect(pill).not.toHaveClass("bg-primary-soft");
    // mockup 的 .pill 是 cursor:pointer；tailwind v4 把 button 默认光标改成了 default。
    expect(pill).toHaveClass("cursor-pointer");
  });

  it("Given a fixed model is selected, when it resolves, then the pill marks it fixed without a dynamic icon", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({ items: [RESPONSE_PROVIDER] });
    appMocks.ListLLMModels.mockResolvedValue({
      items: [
        model("mk-gpt5-default", "gpt-5"),
        model("mk-fixed", "gpt-5-codex"),
      ],
    });

    render(
      <Harness
        backendType="codex"
        sessionId={42}
        persistedProviderKey="acme-response"
        persistedModelKey="mk-fixed"
      />,
    );

    const pill = await screen.findByTestId("provider-pill");
    await waitFor(() => expect(pill).toHaveTextContent("gpt-5-codex"));
    // 固定态 = 品牌标识 + 等宽模型 ID，没有 ↻ 也没有模式名。
    expect(pill).not.toHaveTextContent("Fixed model");
    expect(within(pill).getByRole("img")).toBeInTheDocument();
    expect(
      screen.queryByTestId("provider-pill-dynamic-icon"),
    ).not.toBeInTheDocument();
    expect(
      within(pill).queryByTestId("model-target-trigger-sub"),
    ).not.toBeInTheDocument();
  });

  it("Given the selected model is missing, when the catalog finishes loading, then the pill keeps the target and labels it no longer valid", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({
      items: [ANTHROPIC_PROVIDER],
    });
    appMocks.ListLLMModels.mockResolvedValue({ items: [] });

    render(
      <Harness
        backendType="claudecode"
        sessionId={42}
        persistedProviderKey="acme-anthropic"
        persistedModelKey="mk-deleted"
      />,
    );

    const pill = await screen.findByTestId("provider-pill");
    await waitFor(() => expect(pill).toHaveTextContent("mk-deleted"));
    // 失效态单行：等宽目标 · 已失效，整颗 pill 走 mockup 的 .pill.warn 配色。
    expect(pill).toHaveTextContent("mk-deleted · No longer valid");
    // 失效是唯一保留配色信号的一态（mockup .pill.warn）。
    expect(pill).toHaveClass(
      "cursor-pointer",
      "border-status-waiting",
      "bg-status-waiting-bg",
      "text-status-waiting",
    );
    expect(
      within(pill).queryByTestId("model-target-trigger-sub"),
    ).not.toBeInTheDocument();
  });
});

describe("ProviderPill · 顶部特殊项副行（mockup：→ + 品牌标识 + 供应商 · 模型）", () => {
  it("Given the agent binding resolves to a provider and model, When the picker opens, Then the follow-agent option's resolution line carries the arrow, the provider brand mark and a monospaced model id", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({
      items: [ANTHROPIC_PROVIDER],
    });
    appMocks.ListLLMModels.mockResolvedValue({
      items: [model("mk-sonnet", "claude-sonnet-4-5")],
    });

    render(
      <Harness
        backendType="claudecode"
        sessionId={42}
        boundProviderKey="acme-anthropic"
        boundModelKey="mk-sonnet"
      />,
    );

    const pill = await screen.findByTestId("provider-pill");
    await waitFor(() => expect(pill).toHaveTextContent("claude-sonnet-4-5"));

    const user = userEvent.setup();
    await user.click(pill);

    const follow = within(screen.getByRole("listbox")).getByRole("option", {
      name: /Follow agent binding/i,
    });
    const resolution = within(follow).getByTestId("special-resolution");
    // 解析结果不是一串纯文字：箭头点出「解析到」，品牌标识让绑定的供应商一眼可认。
    expect(resolution).toHaveTextContent("→");
    expect(
      within(resolution).getByRole("img", { name: "Anthropic" }),
    ).toBeInTheDocument();
    expect(resolution).toHaveTextContent("Acme Claude");
    // 模型 ID 是标识符，按 mockup 单独走等宽，不跟着供应商名一起排。
    expect(within(resolution).getByText("claude-sonnet-4-5")).toHaveClass(
      "font-mono",
    );
  });

  it("Given the bound provider cannot be resolved in the catalog, When the picker opens, Then the resolution line stays plain text instead of rendering half a brand mark", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({
      items: [ANTHROPIC_PROVIDER],
    });
    appMocks.ListLLMModels.mockResolvedValue({
      items: [model("mk-sonnet", "claude-sonnet-4-5")],
    });

    render(
      <Harness
        backendType="claudecode"
        sessionId={42}
        boundProviderKey="ghost-provider"
        boundModelKey=""
      />,
    );

    const pill = await screen.findByTestId("provider-pill");
    await waitFor(() => expect(pill).not.toBeDisabled());

    const user = userEvent.setup();
    await user.click(pill);

    const follow = within(screen.getByRole("listbox")).getByRole("option", {
      name: /Follow agent binding/i,
    });
    expect(follow).toHaveTextContent("ghost-provider");
    expect(follow).not.toHaveTextContent("→");
    expect(within(follow).queryByRole("img")).not.toBeInTheDocument();
    expect(
      within(follow).queryByTestId("special-resolution"),
    ).not.toBeInTheDocument();
  });
});

describe("ProviderPill · 跟随 Agent 且后端走 CLI 自身登录态（spec L118：副行要写出它解析成什么）", () => {
  async function openFollowOption() {
    const pill = await screen.findByTestId("provider-pill");
    await waitFor(() => expect(pill).not.toBeDisabled());
    await userEvent.setup().click(pill);
    return {
      pill,
      follow: within(screen.getByRole("listbox")).getByRole("option", {
        name: /Follow agent binding/i,
      }),
    };
  }

  it("Given the agent backend is bound to a provider, When the pill and picker render, Then neither claims the CLI login state", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({
      items: [ANTHROPIC_PROVIDER],
    });
    appMocks.ListLLMModels.mockResolvedValue({
      items: [model("mk-sonnet", "claude-sonnet-4-5")],
    });
    render(
      <Harness
        backendType="claudecode"
        sessionId={42}
        boundProviderKey="acme-anthropic"
        boundModelKey="mk-sonnet"
      />,
    );

    const { pill, follow } = await openFollowOption();
    expect(pill).toHaveTextContent("Follow agent binding · claude-sonnet-4-5");
    expect(pill).not.toHaveTextContent("CLI login state");
    expect(follow).not.toHaveTextContent(
      "Determined by the CLI's own login account",
    );
    expect(
      within(within(follow).getByTestId("special-resolution")).getByRole("img"),
    ).toBeInTheDocument();
  });

  it("Given the agent backend is known to have no provider bound, When the pill and picker render, Then both say the CLI's own login account decides", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({
      items: [ANTHROPIC_PROVIDER],
    });
    appMocks.ListLLMModels.mockResolvedValue({
      items: [model("mk-sonnet", "claude-sonnet-4-5")],
    });
    // 空串 = 会话详情已加载且该 Agent 后端确实没绑供应商（≠ 还没加载出来）。
    render(
      <Harness
        backendType="claudecode"
        sessionId={42}
        boundProviderKey=""
        boundModelKey=""
      />,
    );

    const { pill, follow } = await openFollowOption();
    expect(pill).toHaveTextContent("Follow agent binding · CLI login state");
    const resolution = within(follow).getByTestId("special-resolution");
    expect(resolution).toHaveTextContent("→");
    expect(resolution).toHaveTextContent(
      "Determined by the CLI's own login account",
    );
    // 没有供应商可认领，就别画半个空标识。
    expect(within(resolution).queryByRole("img")).not.toBeInTheDocument();
  });

  it("Given the agent binding is not loaded yet, When the pill and picker render, Then neither flashes the CLI login state", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({
      items: [ANTHROPIC_PROVIDER],
    });
    appMocks.ListLLMModels.mockResolvedValue({
      items: [model("mk-sonnet", "claude-sonnet-4-5")],
    });
    // boundProviderKey 未传 = undefined = 会话详情/新建目标还没到，「不知道」不等于「没绑」。
    render(<Harness backendType="claudecode" sessionId={42} />);

    const { pill, follow } = await openFollowOption();
    expect(pill).toHaveTextContent("Follow agent binding");
    expect(pill).not.toHaveTextContent("CLI login state");
    expect(follow).not.toHaveTextContent(
      "Determined by the CLI's own login account",
    );
    expect(
      within(follow).queryByTestId("special-resolution"),
    ).not.toBeInTheDocument();
  });
});

describe("ProviderPill · 已有会话（决策 1/9/10：选择立即持久化 + 切换 notice）", () => {
  it("已有会话水合当前选择：providerKey + modelKey 显示会话已持久化的目标", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({ items: ALL_PROVIDERS });
    appMocks.ListLLMModels.mockResolvedValue({
      items: [model("mk-sonnet", "claude-sonnet-4-5")],
    });
    render(
      <Harness
        backendType="claudecode"
        sessionId={42}
        persistedProviderKey="acme-anthropic"
        persistedModelKey="mk-sonnet"
      />,
    );

    // 目录异步加载（providers 空时 catalogLoading 已为 false，不能只等 not.toBeDisabled），
    // 必须等模型目录解析完成、pill 写出解析到的模型 ID 才断言。
    await waitFor(() =>
      expect(screen.getByTestId("provider-pill")).toHaveTextContent(
        "claude-sonnet-4-5",
      ),
    );
    // 单行形态下供应商身份由品牌标识承担，人读名不再挤进 pill 的脸。
    expect(screen.getByTestId("provider-pill")).not.toHaveTextContent(
      "Acme Claude",
    );
  });

  it("选中 provider-default 调用 SetChatSessionModelTarget 并按响应更新显示，随后触发 onSwitched", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({ items: ALL_PROVIDERS });
    appMocks.SetChatSessionModelTarget.mockResolvedValue({
      providerKey: "acme-anthropic",
      modelKey: "",
      agentProviderKey: "",
      agentModelKey: "",
    });
    const onSwitched = vi.fn();
    render(
      <Harness
        backendType="claudecode"
        sessionId={42}
        persistedProviderKey=""
        onSwitched={onSwitched}
      />,
    );

    const pill = await waitFor(() => screen.getByTestId("provider-pill"));
    await waitFor(() => expect(pill).not.toBeDisabled());

    const user = userEvent.setup();
    await user.click(pill);
    await user.click(providerDefaultOption(screen.getByRole("listbox")));

    expect(appMocks.SetChatSessionModelTarget).toHaveBeenCalledWith({
      sessionId: 42,
      providerKey: "acme-anthropic",
      modelKey: "",
    });
    await waitFor(() =>
      expect(screen.getByTestId("provider-pill")).toHaveTextContent(
        "Acme Claude",
      ),
    );
    await waitFor(() => expect(onSwitched).toHaveBeenCalledTimes(1));
  });

  it("清回「跟随 agent 绑定」调用 SetChatSessionModelTarget 传双空串，pill 落回绑定供应商名", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({ items: ALL_PROVIDERS });
    appMocks.SetChatSessionModelTarget.mockResolvedValue({
      providerKey: "",
      modelKey: "",
      agentProviderKey: "acme-anthropic",
      agentModelKey: "",
    });
    render(
      <Harness
        backendType="claudecode"
        sessionId={42}
        boundProviderKey="acme-anthropic"
        persistedProviderKey="acme-response"
      />,
    );

    const pill = await waitFor(() => screen.getByTestId("provider-pill"));
    await waitFor(() => expect(pill).not.toBeDisabled());

    const user = userEvent.setup();
    await user.click(pill);
    await user.click(
      within(screen.getByRole("listbox")).getByRole("option", {
        name: /Follow agent binding/i,
      }),
    );

    expect(appMocks.SetChatSessionModelTarget).toHaveBeenCalledWith({
      sessionId: 42,
      providerKey: "",
      modelKey: "",
    });
    await waitFor(() =>
      expect(screen.getByTestId("provider-pill")).toHaveTextContent(
        "Acme Claude",
      ),
    );
  });

  it("切换失败：回滚显示并在弹层底部报错", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({ items: ALL_PROVIDERS });
    appMocks.SetChatSessionModelTarget.mockRejectedValue(new Error("nope"));
    render(
      <Harness
        backendType="claudecode"
        sessionId={42}
        persistedProviderKey=""
      />,
    );

    const pill = await waitFor(() => screen.getByTestId("provider-pill"));
    await waitFor(() => expect(pill).not.toBeDisabled());

    const user = userEvent.setup();
    await user.click(pill);
    await user.click(providerDefaultOption(screen.getByRole("listbox")));

    await waitFor(() =>
      expect(screen.getByTestId("provider-pill")).toHaveTextContent(
        "Follow agent binding",
      ),
    );
    await user.click(screen.getByTestId("provider-pill"));
    expect(screen.getByText("nope")).toBeInTheDocument();
  });

  it("切换成功后按执行位置指纹记录最近使用（本机/远端隔离，决策 19）", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({ items: ALL_PROVIDERS });
    appMocks.ListLLMModels.mockResolvedValue({
      items: [
        model("mk-sonnet", "claude-sonnet-4-5"),
        model("mk-opus", "claude-opus-4-5"),
      ],
    });
    appMocks.SetChatSessionModelTarget.mockResolvedValue({
      providerKey: "acme-anthropic",
      modelKey: "mk-opus",
      agentProviderKey: "",
      agentModelKey: "",
    });
    // 远端 daemon 目录含该 provider + 两个模型（启用）→ 固定模型可选。
    appMocks.RemoteDeviceList.mockResolvedValue([device({ id: 7 })]);
    appMocks.RemoteDeviceListProviders.mockResolvedValue([
      remoteProvider({
        models: [
          remoteModel("mk-sonnet", "claude-sonnet-4-5"),
          remoteModel("mk-opus", "claude-opus-4-5"),
        ],
      }),
    ]);
    const onSwitched = vi.fn();
    render(
      <Harness
        backendType="claudecode"
        sessionId={42}
        executionLocation="7"
        persistedProviderKey=""
        onSwitched={onSwitched}
      />,
    );

    const pill = await waitFor(() => screen.getByTestId("provider-pill"));
    await waitFor(() => expect(pill).not.toBeDisabled());

    const user = userEvent.setup();
    await user.click(pill);
    await user.click(
      within(screen.getByRole("listbox")).getByRole("option", {
        name: /claude-opus-4-5/,
      }),
    );

    await waitFor(() => expect(onSwitched).toHaveBeenCalledTimes(1));

    // 最近使用只在 target 成功持久化后记录，并按执行设备指纹隔离：远端会话的选择
    // 落在 daemon:7 指纹下，绝不落进本机 local 指纹（spec「UI, accessibility and
    // recent targets」决策 19）。
    const daemonRecent = JSON.parse(
      window.localStorage.getItem(recentStorageKey("chat", "7")) ?? "[]",
    );
    expect(daemonRecent).toEqual([
      { providerKey: "acme-anthropic", modelKey: "mk-opus" },
    ]);
    const localRecent = JSON.parse(
      window.localStorage.getItem(recentStorageKey("chat", "")) ?? "[]",
    );
    expect(localRecent).toEqual([]);
  });
});

describe("ProviderPill · switch transcript notice", () => {
  it.each([
    [
      "fixed",
      {
        providerKey: "acme-anthropic",
        providerName: "Acme Claude",
        modelKey: "mk-sonnet",
        modelName: "claude-sonnet-4-5",
      },
      "Fixed model",
    ],
    [
      "provider-default",
      { providerKey: "acme-anthropic", providerName: "Acme Claude" },
      "Follow provider default",
    ],
    ["agent", { providerKey: "" }, "Follow agent binding"],
  ])(
    "Given a successful %s switch, when its notice renders, then it uses the same mode wording as the pill",
    (_mode, block, wording) => {
      renderSwitchNotice(block);

      const notice = screen.getByTestId("transcript-notice");
      expect(notice).toHaveTextContent(wording);
      expect(i18n.t("providerPill.mode.fixed")).toBe("Fixed model");
      expect(i18n.t("providerPill.mode.followDefault")).toBe(
        "Follow provider default",
      );
      expect(i18n.t("modelTargetPicker.special.chat")).toBe(
        "Follow agent binding",
      );
    },
  );
});

describe("ProviderPill · 远端执行（gap 1：chat Picker 接收 daemon 能力/目录门控）", () => {
  it("远端 daemon 支持 llm-model-target-v1 且目录含 Provider → 该 provider 可选；desktop 独有 Provider 禁用 + 需同步提示", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({
      items: [ANTHROPIC_PROVIDER, CHAT_PROVIDER],
    });
    appMocks.ListLLMModels.mockResolvedValue({
      items: [
        model("mk-sonnet", "claude-sonnet-4-5"),
        model("mk-opus", "claude-opus-4-5"),
      ],
    });
    appMocks.RemoteDeviceList.mockResolvedValue([device({ id: 7 })]);
    appMocks.RemoteDeviceListProviders.mockResolvedValue([remoteProvider()]);
    render(<Harness backendType="builtin" executionLocation="7" />);

    const pill = await waitFor(() => screen.getByTestId("provider-pill"));
    await waitFor(() => expect(pill).not.toBeDisabled());

    const user = userEvent.setup();
    await user.click(pill);

    const listbox = screen.getByRole("listbox");
    // daemon 目录里有 acme-anthropic → provider-default 可选（首项）。
    expect(providerDefaultOption(listbox)).not.toBeDisabled();
    // 不在 daemon 模型目录里的 fixed-model（claude-opus-4-5）→ 需同步禁用。
    const opusOptions = within(listbox).getAllByRole("option", {
      name: /claude-opus-4-5/,
    });
    expect(opusOptions.length).toBeGreaterThan(0);
    for (const opus of opusOptions) {
      expect(opus).toHaveAttribute("aria-disabled", "true");
      expect(opus).toHaveAttribute(
        "title",
        "Local only — sync to this device first",
      );
    }
    // desktop 独有的 Provider（Acme Chat）→ provider-default 需同步禁用。
    const chatDefault = providerDefaultOption(listbox, 1);
    expect(chatDefault).toHaveAttribute("aria-disabled", "true");
    expect(chatDefault).toHaveAttribute(
      "title",
      "Local only — sync to this device first",
    );
    // 灰掉的原因每一行自己已经写了（上面的 title + 行内提示），底部不再总述一遍；
    // 留在底部的只有「同步会把 API Key 复制过去」这条后果承诺。
    expect(
      within(listbox).getAllByText("Local only — sync to this device first")
        .length,
    ).toBeGreaterThan(0);
    expect(
      screen.getByRole("button", { name: "Sync Acme Chat to this device" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        "Syncing copies the API key to that device and needs your explicit confirmation",
      ),
    ).toBeInTheDocument();
  });

  it("远端缺少供应商时提供显式同步入口，确认后复制凭证并刷新目录", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({ items: [CHAT_PROVIDER] });
    appMocks.RemoteDeviceList.mockResolvedValue([device({ id: 7 })]);
    appMocks.RemoteDeviceListProviders.mockResolvedValueOnce(
      [],
    ).mockResolvedValueOnce([
      remoteProvider({
        key: "acme-chat",
        name: "Acme Chat",
        type: "openai-chat",
        models: [],
      }),
    ]);
    render(<Harness backendType="builtin" executionLocation="7" />);

    const pill = await waitFor(() => screen.getByTestId("provider-pill"));
    await waitFor(() => expect(pill).not.toBeDisabled());

    const user = userEvent.setup();
    await user.click(pill);
    const listbox = screen.getByRole("listbox");
    const chatDefault = providerDefaultOption(listbox);
    expect(chatDefault).toBeDisabled();

    await user.click(
      screen.getByRole("button", { name: "Sync Acme Chat to this device" }),
    );
    const dialog = await screen.findByRole("dialog", {
      name: "Sync provider to device",
    });
    expect(dialog).toHaveTextContent(/API key/i);
    await user.click(
      within(dialog).getByRole("button", { name: "Confirm sync" }),
    );

    await waitFor(() =>
      expect(appMocks.RemoteDeviceSyncProvider).toHaveBeenCalledWith(
        7,
        "acme-chat",
      ),
    );
    await waitFor(() =>
      expect(appMocks.RemoteDeviceListProviders).toHaveBeenCalledTimes(2),
    );
  });

  it("Given a remote target with no paired row on this machine, When the picker opens, Then no sync entry is offered for it", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({ items: [CHAT_PROVIDER] });
    appMocks.RemoteDeviceList.mockResolvedValue([]);
    render(
      <Harness backendType="builtin" executionLocation="sha256:peer-desktop" />,
    );

    const pill = await waitFor(() => screen.getByTestId("provider-pill"));
    await waitFor(() => expect(pill).not.toBeDisabled());

    const user = userEvent.setup();
    await user.click(pill);
    expect(providerDefaultOption(screen.getByRole("listbox"))).toBeDisabled();
    expect(
      screen.queryByRole("button", { name: "Sync Acme Chat to this device" }),
    ).not.toBeInTheDocument();
  });

  it("Given a canonical remote fingerprint, When the Composer picker loads target capabilities, Then it resolves the local paired row and keeps remote fixed-model gating", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({
      items: [ANTHROPIC_PROVIDER],
    });
    appMocks.ListLLMModels.mockResolvedValue({
      items: [
        model("mk-sonnet", "claude-sonnet-4-5"),
        model("mk-opus", "claude-opus-4-5"),
      ],
    });
    appMocks.RemoteDeviceList.mockResolvedValue([
      device({
        id: 7,
        daemonFingerprint: "sha256:remote-daemon",
        supportsLLMModelTarget: false,
      }),
    ]);
    appMocks.RemoteDeviceListProviders.mockResolvedValue([
      remoteProvider({
        models: [
          remoteModel("mk-sonnet", "claude-sonnet-4-5"),
          remoteModel("mk-opus", "claude-opus-4-5"),
        ],
      }),
    ]);
    render(
      <Harness
        backendType="builtin"
        executionLocation="sha256:remote-daemon"
      />,
    );

    const pill = await waitFor(() => screen.getByTestId("provider-pill"));
    await waitFor(() => expect(pill).not.toBeDisabled());
    const user = userEvent.setup();
    await user.click(pill);

    await waitFor(() =>
      expect(appMocks.RemoteDeviceListProviders).toHaveBeenCalledWith(7),
    );
    const opus = within(screen.getByRole("listbox")).getByRole("option", {
      name: /claude-opus-4-5/,
    });
    expect(opus).toHaveAttribute("aria-disabled", "true");
  });

  it("Given execution stays on this machine and the device-identity RPC fails, When the picker opens, Then local fixed models are not gated as remote", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({
      items: [ANTHROPIC_PROVIDER],
    });
    appMocks.ListLLMModels.mockResolvedValue({
      items: [
        model("mk-sonnet", "claude-sonnet-4-5"),
        model("mk-opus", "claude-opus-4-5"),
      ],
    });
    // R13 canonicalization stores this installation's own fingerprint on local
    // backends, so a purely local session carries a sha256: executionLocation.
    appMocks.RemoteDeviceFingerprint.mockRejectedValue(
      new Error("remote device service unavailable"),
    );
    appMocks.RemoteDeviceList.mockResolvedValue([]);
    render(
      <Harness
        backendType="builtin"
        executionLocation="sha256:local-desktop"
      />,
    );

    const pill = await waitFor(() => screen.getByTestId("provider-pill"));
    await waitFor(() => expect(pill).not.toBeDisabled());
    const user = userEvent.setup();
    await user.click(pill);

    const listbox = screen.getByRole("listbox");
    expect(providerDefaultOption(listbox)).not.toBeDisabled();
    const opus = within(listbox).getByRole("option", {
      name: /claude-opus-4-5/,
    });
    expect(opus).not.toHaveAttribute("aria-disabled", "true");
    expect(appMocks.RemoteDeviceListProviders).not.toHaveBeenCalled();
  });

  it("Given the execution target changes while its catalog request is in flight, When the previous device answers last, Then it cannot replace the current catalog", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({
      items: [ANTHROPIC_PROVIDER, CHAT_PROVIDER],
    });
    appMocks.RemoteDeviceList.mockResolvedValue([
      device({ id: 7, name: "old-daemon", daemonFingerprint: "sha256:old" }),
      device({
        id: 8,
        name: "current-daemon",
        daemonFingerprint: "sha256:current",
      }),
    ]);
    const stale = deferred<unknown[]>();
    appMocks.RemoteDeviceListProviders.mockReset();
    appMocks.RemoteDeviceListProviders.mockImplementation((id: number) =>
      id === 7 ? stale.promise : Promise.resolve([remoteProvider()]),
    );

    const { rerender } = render(
      <Harness
        backendType="builtin"
        executionLocation="sha256:old"
        sessionId={42}
        persistedProviderKey="acme-chat"
      />,
    );
    await waitFor(() =>
      expect(appMocks.RemoteDeviceListProviders).toHaveBeenCalledWith(7),
    );
    rerender(
      <Harness
        backendType="builtin"
        executionLocation="sha256:current"
        sessionId={42}
        persistedProviderKey="acme-chat"
      />,
    );
    await waitFor(() =>
      expect(appMocks.RemoteDeviceListProviders).toHaveBeenCalledWith(8),
    );

    // The stale device answers last, claiming it does have the selected provider.
    await act(async () => {
      stale.resolve([remoteProvider({ key: "acme-chat", name: "Acme Chat" })]);
      await stale.promise;
    });

    const pill = await waitFor(() => screen.getByTestId("provider-pill"));
    await waitFor(() => expect(pill).not.toBeDisabled());
    const user = userEvent.setup();
    await user.click(pill);

    // The current device (8) does not publish acme-chat, so the missing-provider
    // notice must survive the late answer from device 7.
    expect(
      screen.getByText(
        "Target device is missing this provider. Sync it first.",
      ),
    ).toBeInTheDocument();
  });

  it("远端 daemon 无 llm-model-target-v1 能力位（旧/离线）→ 全部 fixed-model 禁用，provider-default 仍可选", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({
      items: [ANTHROPIC_PROVIDER],
    });
    appMocks.ListLLMModels.mockResolvedValue({
      items: [
        model("mk-sonnet", "claude-sonnet-4-5"),
        model("mk-opus", "claude-opus-4-5"),
      ],
    });
    appMocks.RemoteDeviceList.mockResolvedValue([
      device({ id: 7, supportsLLMModelTarget: false }),
    ]);
    appMocks.RemoteDeviceListProviders.mockReset();
    appMocks.RemoteDeviceListProviders.mockResolvedValue([
      remoteProvider({
        models: [
          remoteModel("mk-sonnet", "claude-sonnet-4-5"),
          remoteModel("mk-opus", "claude-opus-4-5"),
        ],
      }),
    ]);
    render(<Harness backendType="builtin" executionLocation="7" />);

    const pill = await waitFor(() => screen.getByTestId("provider-pill"));
    await waitFor(() => expect(pill).not.toBeDisabled());

    const user = userEvent.setup();
    await user.click(pill);

    const listbox = screen.getByRole("listbox");
    // provider-default 仍可选（目录里存在且能力位只影响 fixed-model）。
    expect(providerDefaultOption(listbox)).not.toBeDisabled();
    // fixed-model 一律禁用：daemon 不支持，绝不静默降级。
    const opus = within(listbox).getByRole("option", {
      name: /claude-opus-4-5/,
    });
    expect(opus).toHaveAttribute("aria-disabled", "true");
    expect(opus).toHaveAttribute(
      "title",
      "This device does not support fixed models — upgrade agentred",
    );
  });

  it("远端执行且当前选中 Provider 在 daemon 缺失 → 弹层底部 remoteMissing 提示", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({ items: [CHAT_PROVIDER] });
    appMocks.RemoteDeviceList.mockResolvedValue([device({ id: 7 })]);
    appMocks.RemoteDeviceListProviders.mockResolvedValue([]);
    render(
      <Harness
        backendType="builtin"
        executionLocation="7"
        sessionId={42}
        persistedProviderKey="acme-chat"
      />,
    );

    const pill = await waitFor(() => screen.getByTestId("provider-pill"));
    await waitFor(() => expect(pill).not.toBeDisabled());

    const user = userEvent.setup();
    await user.click(pill);

    expect(
      screen.getByText(
        "Target device is missing this provider. Sync it first.",
      ),
    ).toBeInTheDocument();
  });

  it("本机（无 executionLocation）不拉远端目录、不启用远端门控，fixed-model 照常可选", async () => {
    appMocks.ListLLMProviders.mockResolvedValue({
      items: [ANTHROPIC_PROVIDER],
    });
    appMocks.ListLLMModels.mockResolvedValue({
      items: [
        model("mk-sonnet", "claude-sonnet-4-5"),
        model("mk-opus", "claude-opus-4-5"),
      ],
    });
    render(<Harness backendType="builtin" />);

    const pill = await waitFor(() => screen.getByTestId("provider-pill"));
    await waitFor(() => expect(pill).not.toBeDisabled());

    expect(appMocks.RemoteDeviceList).not.toHaveBeenCalled();
    expect(appMocks.RemoteDeviceListProviders).not.toHaveBeenCalled();

    const user = userEvent.setup();
    await user.click(pill);
    // 本机不设能力位限制：fixed-model 可选。
    const opus = within(screen.getByRole("listbox")).getByRole("option", {
      name: /claude-opus-4-5/,
    });
    expect(opus).not.toBeDisabled();
  });
});
