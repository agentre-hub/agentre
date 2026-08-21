import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import enCommon from "@/i18n/locales/en";
import zhCommon from "@/i18n/locales/zh-CN";

import {
  ModelTargetPicker,
  providerCompatibleForBackend,
  readRecentTargets,
  recordRecentTarget,
  removeRecentTarget,
  recentStorageKey,
} from "@agentre-ai/agentre-ui";

function catalog() {
  return [
    {
      providerKey: "k-anthropic",
      id: 1,
      name: "Anthropic",
      type: "anthropic",
      enabled: true,
      defaultModel: {
        modelKey: "mk-1",
        modelId: "claude-sonnet-4-6",
        name: "Sonnet",
        enabled: true,
        contextWindow: 200000,
        maxOutput: 32000,
      },
      models: [
        {
          modelKey: "mk-1",
          modelId: "claude-sonnet-4-6",
          name: "Sonnet",
          enabled: true,
          contextWindow: 200000,
          maxOutput: 32000,
        },
        {
          modelKey: "mk-opus",
          modelId: "claude-opus-4-8",
          name: "Opus",
          enabled: true,
          contextWindow: 400000,
          maxOutput: 64000,
        },
        {
          modelKey: "mk-off",
          modelId: "claude-old",
          name: "",
          enabled: false,
          contextWindow: 0,
          maxOutput: 0,
        },
      ],
    },
    {
      providerKey: "k-openai",
      id: 2,
      name: "OpenAI",
      type: "openai-response",
      enabled: true,
      defaultModel: {
        modelKey: "mk-2",
        modelId: "gpt-5.4",
        name: "GPT",
        enabled: true,
        contextWindow: 128000,
        maxOutput: 16000,
      },
      models: [
        {
          modelKey: "mk-2",
          modelId: "gpt-5.4",
          name: "GPT",
          enabled: true,
          contextWindow: 128000,
          maxOutput: 16000,
        },
      ],
    },
  ];
}

function catalogWithDisabledProvider() {
  return [
    {
      providerKey: "k-anthropic",
      id: 1,
      name: "Anthropic",
      type: "anthropic",
      enabled: false,
      defaultModel: {
        modelKey: "mk-1",
        modelId: "claude-sonnet-4-6",
        enabled: true,
      },
      models: [
        { modelKey: "mk-1", modelId: "claude-sonnet-4-6", enabled: true },
        { modelKey: "mk-off", modelId: "claude-old", enabled: false },
      ],
    },
  ];
}

afterEach(() => {
  vi.clearAllMocks();
  window.localStorage.clear();
});

describe("ModelTargetPicker recents", () => {
  it("按场景 + 执行位置指纹隔离存储，且只存 providerKey/modelKey", () => {
    recordRecentTarget("backend", "", { providerKey: "k-1", modelKey: "mk-1" });
    recordRecentTarget("backend", "", { providerKey: "k-2", modelKey: "mk-2" });
    // 远端设备隔离：local 里记过的 key 不污染 daemon 作用域。
    expect(readRecentTargets("backend", "")).toHaveLength(2);
    expect(readRecentTargets("backend", "7")).toHaveLength(0);
    recordRecentTarget("backend", "7", { providerKey: "k-3", modelKey: "" });
    expect(readRecentTargets("backend", "7")).toHaveLength(1);
    const raw = window.localStorage.getItem(recentStorageKey("backend", ""));
    expect(raw).not.toContain("name");
    expect(raw).not.toContain("modelId");
    expect(raw).not.toContain("apiKey");
  });

  it("native/inherit（双空 key）永远不进最近；最新在前；最多 5 项去重", () => {
    recordRecentTarget("backend", "", { providerKey: "", modelKey: "" });
    expect(readRecentTargets("backend", "")).toHaveLength(0);

    for (let i = 1; i <= 6; i++) {
      recordRecentTarget("backend", "", {
        providerKey: `k-${i}`,
        modelKey: `mk-${i}`,
      });
    }
    const all = readRecentTargets("backend", "");
    expect(all).toHaveLength(5);
    expect(all[0].providerKey).toBe("k-6");
    expect(all).not.toContainEqual({ providerKey: "k-1", modelKey: "mk-1" });

    recordRecentTarget("backend", "", { providerKey: "k-3", modelKey: "mk-3" });
    const deduped = readRecentTargets("backend", "");
    expect(deduped[0]).toEqual({ providerKey: "k-3", modelKey: "mk-3" });
    expect(deduped.filter((x) => x.providerKey === "k-3")).toHaveLength(1);
  });

  it("removeRecentTarget 只移除指定目标，其余保留", () => {
    recordRecentTarget("backend", "", { providerKey: "k-1", modelKey: "mk-1" });
    recordRecentTarget("backend", "", { providerKey: "k-2", modelKey: "mk-2" });
    removeRecentTarget("backend", "", { providerKey: "k-1", modelKey: "mk-1" });
    expect(readRecentTargets("backend", "")).toEqual([
      { providerKey: "k-2", modelKey: "mk-2" },
    ]);
  });
});

describe("ModelTargetPicker providerCompatibleForBackend", () => {
  it("claudecode 只收 anthropic；codex 只收 openai-response；piagent 三类全收", () => {
    expect(providerCompatibleForBackend("claudecode", "anthropic")).toBe(true);
    expect(providerCompatibleForBackend("claudecode", "openai-response")).toBe(
      false,
    );
    expect(providerCompatibleForBackend("codex", "openai-response")).toBe(true);
    expect(providerCompatibleForBackend("codex", "anthropic")).toBe(false);
    expect(providerCompatibleForBackend("piagent", "anthropic")).toBe(true);
    expect(providerCompatibleForBackend("piagent", "openai-chat")).toBe(true);
    expect(providerCompatibleForBackend("piagent", "openai-response")).toBe(
      true,
    );
    expect(providerCompatibleForBackend("builtin", "anthropic")).toBe(true);
  });
});

describe("ModelTargetPicker", () => {
  it("backend 场景顶部特殊项是 CLI 自身登录态，无 prop 回落「由 CLI 登录态决定」副行，选中发射双空 key", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={onChange}
        catalog={catalog()}
      />,
    );

    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const special = await screen.findByRole("option", {
      name: /CLI login state/,
    });
    expect(special).toHaveTextContent(
      /Determined by the CLI's own login account/,
    );
    await user.click(special);
    expect(onChange).toHaveBeenCalledWith({ providerKey: "", modelKey: "" });
  });

  it("chat / route 特殊项用 prop 副行显示解析结果", async () => {
    const user = userEvent.setup();
    const { unmount } = render(
      <ModelTargetPicker
        scenario="chat"
        specialSublabel="Anthropic · claude-sonnet-4-6"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    expect(
      await screen.findByRole("option", { name: /Follow agent binding/ }),
    ).toHaveTextContent("Anthropic · claude-sonnet-4-6");
    unmount();

    render(
      <ModelTargetPicker
        scenario="route"
        specialSublabel="Anthropic · claude-opus-4-8"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    expect(
      await screen.findByRole("option", { name: /Inherit main binding/ }),
    ).toHaveTextContent("Anthropic · claude-opus-4-8");
  });

  it("provider 组内 provider-default 首项（强调底色 + 动态图标 + 当前解析模型），再 fixed-model；不兼容的 provider 被过滤", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={onChange}
        catalog={catalog()}
      />,
    );

    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const list = await screen.findByRole("listbox", { name: "LLM Provider" });
    // claudecode 只收 anthropic：OpenAI 组不可见。
    expect(within(list).queryByText("OpenAI")).not.toBeInTheDocument();
    const options = within(list).getAllByRole("option");
    expect(options[0]).toHaveTextContent(/CLI login state/);
    // provider-default 首项：强调底色 + 动态图标 + 「当前 <模型标识>」副行（mockup .opt.dyn）。
    expect(options[1]).toHaveAttribute("data-kind", "provider-default");
    // 核心语义分歧：跟随默认用 primary-soft 底 + primary-text 标题，与固定模型两态可分。
    expect(options[1]).toHaveClass("bg-primary-soft");
    expect(options[1]).toHaveTextContent(/Follow this provider's default/);
    expect(options[1]).toHaveTextContent("Currently claude-sonnet-4-6");
    // 只写「当前跑哪个模型」，不带「默认变了自动跟着变」这类后果从句。
    expect(options[1]).not.toHaveTextContent(/follows automatically/);
    expect(options[1]).not.toHaveTextContent(/never changes/);
    // 等宽只包模型标识本身，人读前缀不跟着走等宽（mockup 的 <span class="mono">）。
    const defaultSub = within(options[1] as HTMLElement).getByText(
      "claude-sonnet-4-6",
    );
    expect(defaultSub).toHaveClass("font-mono");
    expect(defaultSub.parentElement).not.toHaveClass("font-mono");
    // 模型标识绝不能被截断（348px 弹层里副行一旦被吃掉就白写了）。
    expect(defaultSub).not.toHaveClass("truncate");
    expect(
      within(options[1] as HTMLElement).getByText(
        "Follow this provider's default",
      ),
    ).toHaveClass("text-primary-text");
    expect(
      within(options[1] as HTMLElement).getByTestId("provider-default-icon"),
    ).toBeInTheDocument();
    // fixed-model 紧随其后（包括当前默认模型的固定目标）；固定态不共用强调底色。
    expect(options[2]).toHaveTextContent("claude-sonnet-4-6");
    expect(options[2]).not.toHaveClass("bg-primary-soft");
    expect(options[3]).toHaveTextContent("claude-opus-4-8");
    // 停用模型不可选。
    expect(options[4]).toHaveAttribute("aria-disabled", "true");

    await user.click(options[3]);
    expect(onChange).toHaveBeenCalledWith({
      providerKey: "k-anthropic",
      modelKey: "mk-opus",
    });
  });

  it("默认模型也作为 fixed-model 候选保留，让用户可以锁定当前默认而不随以后默认变化", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={onChange}
        catalog={catalog()}
      />,
    );

    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const list = await screen.findByRole("listbox", { name: "LLM Provider" });
    const fixedDefault = within(list).getByRole("option", {
      name: /Sonnet claude-sonnet-4-6/,
    });
    expect(fixedDefault).toHaveAttribute("data-kind", "fixed");

    await user.click(fixedDefault);
    expect(onChange).toHaveBeenCalledWith({
      providerKey: "k-anthropic",
      modelKey: "mk-1",
    });
  });

  it("provider-default 与 fixed-model 视觉分离：fixed-model 归入「固定到具体模型」分段", async () => {
    const user = userEvent.setup();
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const list = await screen.findByRole("listbox", { name: "LLM Provider" });
    expect(
      within(list).getByText("Fixed to a specific model"),
    ).toBeInTheDocument();
  });

  it("搜索只剩固定模型时跟随默认项被过滤掉", async () => {
    const user = userEvent.setup();
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    await user.type(
      screen.getByPlaceholderText("Search providers or models…"),
      "opus",
    );
    const list = await screen.findByRole("listbox", { name: "LLM Provider" });
    expect(
      within(list).queryByRole("option", {
        name: /Follow this provider's default/,
      }),
    ).not.toBeInTheDocument();
    expect(
      within(list).getByRole("option", { name: /claude-opus-4-8/ }),
    ).toBeInTheDocument();
  });

  it("弹层里不再有任何总述性图例：只留行内「当前 <模型>」", async () => {
    const user = userEvent.setup();
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="piagent"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const list = await screen.findByRole("listbox", { name: "LLM Provider" });
    // piagent 两个供应商都兼容 → 两条 provider-default 行，各自写自己的当前模型。
    expect(
      within(list).getAllByRole("option", {
        name: /Follow this provider's default/,
      }),
    ).toHaveLength(2);
    expect(screen.queryByTestId("dynamic-legend")).not.toBeInTheDocument();
    expect(screen.queryByText(/a fixed model never changes/)).toBeNull();
    const dyns = within(list).getAllByRole("option", {
      name: /Follow this provider's default/,
    });
    expect(dyns[0]).toHaveTextContent("Currently claude-sonnet-4-6");
    expect(dyns[1]).toHaveTextContent("Currently gpt-5.4");
  });

  it("没有默认模型时副行是回落说明，不被「当前」前缀包住、也不走等宽", async () => {
    const user = userEvent.setup();
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={[
          {
            providerKey: "k-anthropic",
            id: 1,
            name: "Anthropic",
            type: "anthropic",
            enabled: true,
            defaultModel: null,
            models: [
              { modelKey: "mk-1", modelId: "claude-sonnet-4-6", enabled: true },
            ],
          },
        ]}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const list = await screen.findByRole("listbox", { name: "LLM Provider" });
    const providerDefault = within(list).getAllByRole("option")[1];
    expect(providerDefault).toHaveAttribute("data-kind", "provider-default");
    // 「未设置默认模型」不是模型标识：既不加前缀，也不该被渲成等宽。
    expect(providerDefault).not.toHaveTextContent("Currently");
    const fallback = within(providerDefault as HTMLElement).getByText(
      "No default model set",
    );
    expect(fallback).not.toHaveClass("font-mono");
  });

  it("两个 locale 同步：副行前缀独立成 key，总述性文案与旧插值 key 两边都删掉", () => {
    // 前缀单独成 key，模型标识由渲染层包 mono —— 中英都是前缀在前，两边都成立。
    expect(
      (zhCommon.modelTargetPicker as Record<string, unknown>)
        .defaultCurrentPrefix,
    ).toBe("当前");
    expect(
      (enCommon.modelTargetPicker as Record<string, unknown>)
        .defaultCurrentPrefix,
    ).toBe("Currently");
    for (const locale of [enCommon, zhCommon]) {
      const picker = locale.modelTargetPicker as Record<string, unknown>;
      expect("dynamicLegend" in picker).toBe(false);
      expect("remoteGateHint" in picker).toBe(false);
      expect("compatibilityNote" in picker).toBe(false);
      // 插值版不留孤儿。
      expect("defaultCurrent" in picker).toBe(false);
    }
  });

  it("fixed-model 行不再重复供应商名：label=display name、副行=modelId、右侧上下文/最大输出，供应商名由组头承载", async () => {
    const user = userEvent.setup();
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const list = await screen.findByRole("listbox", { name: "LLM Provider" });
    const options = within(list).getAllByRole("option");
    const opus = options[3];
    expect(opus).toHaveTextContent("Opus");
    expect(opus).toHaveTextContent("claude-opus-4-8");
    expect(opus).not.toHaveTextContent("Anthropic");
    expect(opus).toHaveTextContent(/400K ctx · 64K out/);
    // 组头承载品牌标识 + 供应商名。
    expect(within(list).getByText("Anthropic")).toBeInTheDocument();
    expect(
      within(list).getByRole("img", { name: "Anthropic" }),
    ).toBeInTheDocument();
  });

  it("provider-default 已选中时触发按钮显示 Provider · 实际模型（路由摘要不得只显示 Provider 名称）", () => {
    render(
      <ModelTargetPicker
        scenario="route"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={{ providerKey: "k-anthropic", modelKey: "" }}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );
    // 摘要 = Provider + 当前默认模型（解析出的生效模型），不得只显示 Provider 名。
    expect(
      screen.getByRole("button", { name: "LLM Provider" }),
    ).toHaveTextContent("Anthropic · claude-sonnet-4-6");
  });

  it("triggerSub 存在时触发按钮以主副两行呈现，未传时保持既有单行消费方兼容", () => {
    const { rerender } = render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="Model binding"
        backendType="claudecode"
        selected={{ providerKey: "k-anthropic", modelKey: "" }}
        onChange={vi.fn()}
        catalog={catalog()}
        triggerLabel="Anthropic · Follow default"
        triggerSub="Effective model: claude-sonnet-4-6 · changes next turn"
      />,
    );
    const trigger = screen.getByRole("button", { name: "Model binding" });
    expect(trigger).toHaveTextContent("Anthropic · Follow default");
    expect(trigger).toHaveTextContent(
      "Effective model: claude-sonnet-4-6 · changes next turn",
    );
    expect(
      within(trigger).getByTestId("model-target-trigger-sub"),
    ).toBeVisible();

    rerender(
      <ModelTargetPicker
        scenario="chat"
        aria-label="Model binding"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );
    expect(
      within(
        screen.getByRole("button", { name: "Model binding" }),
      ).queryByTestId("model-target-trigger-sub"),
    ).not.toBeInTheDocument();
  });

  it("triggerLabel 可以是节点：节点原样渲染在主行，无障碍名来自显式字符串而不是节点内容", () => {
    // 主行要放品牌标识（role=img，自带标签）+ 模式徽标，纯字符串装不下。
    const nodeLabel = (
      <span>
        <span
          role="img"
          aria-label="Anthropic brand"
          data-testid="trigger-brand"
        />
        <span>Anthropic</span>
        <span data-testid="trigger-mode-chip">Follow default</span>
      </span>
    );
    const { rerender } = render(
      <ModelTargetPicker
        scenario="backend"
        backendType="claudecode"
        selected={{ providerKey: "k-anthropic", modelKey: "" }}
        onChange={vi.fn()}
        catalog={catalog()}
        triggerLabel={nodeLabel}
      />,
    );
    // 没传 aria-label 时名字回落到目录解析出的字符串，绝不让节点里的品牌标识
    // 和徽标参与无障碍名计算（否则屏幕阅读器会念出重复的品牌名）。
    const trigger = screen.getByRole("button", {
      name: "Anthropic · claude-sonnet-4-6",
    });
    expect(within(trigger).getByTestId("trigger-brand")).toBeInTheDocument();
    expect(within(trigger).getByTestId("trigger-mode-chip")).toHaveTextContent(
      "Follow default",
    );

    // 显式 aria-label 仍然优先 —— 既有消费方按名字定位不回归。
    rerender(
      <ModelTargetPicker
        scenario="backend"
        aria-label="Model binding"
        backendType="claudecode"
        selected={{ providerKey: "k-anthropic", modelKey: "" }}
        onChange={vi.fn()}
        catalog={catalog()}
        triggerLabel={nodeLabel}
      />,
    );
    expect(
      screen.getByRole("button", { name: "Model binding" }),
    ).toBeInTheDocument();
  });

  it("搜索可过滤，空目录渲染空态", async () => {
    const user = userEvent.setup();
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={[]}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    await screen.findByRole("listbox", { name: "LLM Provider" });
    expect(screen.getByText(/No available providers/)).toBeInTheDocument();
  });

  it("没有任何与当前 backend 类型兼容的供应商时给出一句说明", async () => {
    const user = userEvent.setup();
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={[
          {
            providerKey: "k-openai",
            id: 2,
            name: "OpenAI",
            type: "openai-response",
            enabled: true,
            defaultModel: null,
            models: [],
          },
        ]}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    expect(
      await screen.findByText(/No providers compatible with this backend type/),
    ).toBeInTheDocument();
  });

  it("loading / error 状态各自呈现", async () => {
    const user = userEvent.setup();
    const { rerender } = render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={[]}
        loading
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    expect(
      await screen.findByText(/Loading model catalog/),
    ).toBeInTheDocument();

    rerender(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={[]}
        error
      />,
    );
    expect(
      await screen.findByText(/Failed to load model catalog/),
    ).toBeInTheDocument();
  });

  it("失效目标顶部警示写出供应商与模型，被删目标以禁用项保留，键盘方向键 + Enter 可选中", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <ModelTargetPicker
        scenario="route"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={{ providerKey: "k-gone", modelKey: "mk-gone" }}
        onChange={onChange}
        catalog={catalog()}
        invalid
      />,
    );
    // 已失效目标回显原始 key。
    expect(
      screen.getByRole("button", { name: "LLM Provider" }),
    ).toHaveTextContent(/k-gone/);
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    // 顶部警示（弹层内上方，不是底部 footer）。
    const banner = await screen.findByTestId("invalid-banner");
    expect(banner).toHaveTextContent(/no longer valid/);
    expect(banner).toHaveTextContent("k-gone");
    expect(banner).toHaveTextContent("mk-gone");
    // 被删目标以禁用项保留在列表中。
    const list = screen.getByRole("listbox", { name: "LLM Provider" });
    const options = within(list).getAllByRole("option");
    const preserved = options.find((o) => o.textContent?.includes("k-gone"));
    expect(preserved).toBeDefined();
    expect(preserved).toHaveAttribute("aria-disabled", "true");

    // 键盘：↑↓ 移动，Enter 选中。
    await user.keyboard("{ArrowDown}");
    await user.keyboard("{Enter}");
    expect(onChange).toHaveBeenCalled();
  });

  it("失效的当前选择行内写出「当前选择 · 已失效」与失效标记，不只靠顶部横幅", async () => {
    const user = userEvent.setup();
    render(
      <ModelTargetPicker
        scenario="route"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={{ providerKey: "k-gone", modelKey: "mk-gone" }}
        onChange={vi.fn()}
        catalog={catalog()}
        invalid
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const list = await screen.findByRole("listbox", { name: "LLM Provider" });
    const preserved = within(list)
      .getAllByRole("option")
      .find((o) => o.textContent?.includes("k-gone"));
    expect(preserved).toBeDefined();
    expect(preserved).toHaveTextContent("Current selection · no longer valid");
    expect(
      within(preserved as HTMLElement).getByText("Invalid"),
    ).toBeInTheDocument();
  });

  it("route 场景顶部特殊项是继承主绑定，recent 只展示兼容项（chip）", async () => {
    const user = userEvent.setup();
    recordRecentTarget("route", "", {
      providerKey: "k-anthropic",
      modelKey: "mk-opus",
    });
    recordRecentTarget("route", "", {
      providerKey: "k-openai",
      modelKey: "mk-2",
    });
    render(
      <ModelTargetPicker
        scenario="route"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );

    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const list = await screen.findByRole("listbox", { name: "LLM Provider" });
    // inherit-main 特殊项在最前。
    expect(
      within(list).getByRole("option", { name: /Inherit main binding/ }),
    ).toBeInTheDocument();
    // recent 是 chip：兼容的 Anthropic 可见，OpenAI 被隐藏。
    expect(
      screen.getByRole("button", { name: "claude-opus-4-8" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /gpt-5/ }),
    ).not.toBeInTheDocument();
  });

  it("最近使用为单行横向可移除 chip，每条可单独移除，移除后从 localStorage 删除", async () => {
    const user = userEvent.setup();
    recordRecentTarget("backend", "", {
      providerKey: "k-anthropic",
      modelKey: "mk-opus",
    });
    recordRecentTarget("backend", "", {
      providerKey: "k-anthropic",
      modelKey: "",
    });
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );

    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const chips = await screen.findByTestId("recent-chips");
    // 单行横向、不换行。
    expect(chips).toHaveClass("flex-nowrap", "overflow-x-auto");
    // chip 主体可点击选择。
    const opusChip = screen.getByRole("button", { name: "claude-opus-4-8" });
    expect(opusChip).not.toBeDisabled();
    // 单条移除。
    await user.click(
      screen.getByRole("button", { name: "Remove claude-opus-4-8" }),
    );
    expect(readRecentTargets("backend", "")).toEqual([
      { providerKey: "k-anthropic", modelKey: "" },
    ]);
    expect(
      screen.queryByRole("button", { name: "claude-opus-4-8" }),
    ).not.toBeInTheDocument();
  });

  it("最近使用排在顶部特殊项之后、供应商分组之前，并带「最近使用」标签", async () => {
    const user = userEvent.setup();
    recordRecentTarget("backend", "", {
      providerKey: "k-anthropic",
      modelKey: "mk-opus",
    });
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );

    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const list = await screen.findByRole("listbox", { name: "LLM Provider" });
    const special = within(list).getByRole("option", {
      name: /CLI login state/,
    });
    const chips = within(list).getByTestId("recent-chips");
    const providerDefault = within(list).getByRole("option", {
      name: /Follow this provider's default/,
    });
    // 顺序：特殊项 → 最近使用 → 供应商分组。
    expect(
      special.compareDocumentPosition(chips) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(
      chips.compareDocumentPosition(providerDefault) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    // chip 横条自带标签，不再是一排无名 chip。
    expect(within(chips).getByText("Recently used")).toBeInTheDocument();
  });

  it("recent chip 里非默认但启用的固定模型仍可选", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    recordRecentTarget("backend", "", {
      providerKey: "k-anthropic",
      modelKey: "mk-opus", // 非默认（默认 mk-1）但启用
    });
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={onChange}
        catalog={catalog()}
      />,
    );

    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const chip = await screen.findByRole("button", {
      name: "claude-opus-4-8",
    });
    expect(chip).not.toBeDisabled();
    await user.click(chip);
    expect(onChange).toHaveBeenCalledWith({
      providerKey: "k-anthropic",
      modelKey: "mk-opus",
    });
  });

  it("recent chip 保留当前默认模型的 fixed 语义，不降级成 provider-default", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    recordRecentTarget("backend", "", {
      providerKey: "k-anthropic",
      modelKey: "mk-1",
    });
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={onChange}
        catalog={catalog()}
      />,
    );

    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const chip = await screen.findByRole("button", {
      name: "claude-sonnet-4-6",
    });
    await user.click(chip);
    expect(onChange).toHaveBeenCalledWith({
      providerKey: "k-anthropic",
      modelKey: "mk-1",
    });
  });

  it("禁用项给出行内原因：供应商停用 / 模型停用", async () => {
    const user = userEvent.setup();
    // 供应商停用：default 与 fixed 都给出「供应商已停用」。
    const { unmount } = render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalogWithDisabledProvider()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    let list = await screen.findByRole("listbox", { name: "LLM Provider" });
    let options = within(list).getAllByRole("option");
    expect(options[1]).toHaveTextContent("This provider is disabled");
    expect(options[2]).toHaveTextContent("This provider is disabled");
    unmount();

    // 供应商启用、模型停用：该模型行给出「模型已停用」。
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    list = await screen.findByRole("listbox", { name: "LLM Provider" });
    options = within(list).getAllByRole("option");
    const off = options.find((o) => o.textContent?.includes("claude-old"));
    expect(off).toBeDefined();
    expect(off).toHaveTextContent("This model is disabled");
  });
});

describe("ModelTargetPicker remote gating (task 6)", () => {
  function remoteCatalogWith() {
    return [
      {
        providerKey: "k-anthropic",
        id: 0,
        name: "Anthropic",
        type: "anthropic",
        enabled: true,
        defaultModel: {
          modelKey: "mk-1",
          modelId: "claude-sonnet-4-6",
          enabled: true,
        },
        models: [
          { modelKey: "mk-1", modelId: "claude-sonnet-4-6", enabled: true },
        ],
      },
    ];
  }

  it("旧 daemon（不支持 fixed-model）禁用所有 fixed-model，provider-default 仍可选", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={onChange}
        catalog={catalog()}
        executionLocation="7"
        supportsFixedModel={false}
        remoteCatalog={remoteCatalogWith()}
      />,
    );

    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const list = await screen.findByRole("listbox", { name: "LLM Provider" });
    const options = within(list).getAllByRole("option");
    // provider-default（Anthropic）可选；所有 fixed-model（包括当前默认模型）被禁用。
    expect(options[1]).not.toHaveAttribute("aria-disabled", "true");
    expect(options[2]).toHaveAttribute("aria-disabled", "true");
    expect(options[3]).toHaveAttribute("aria-disabled", "true");
    expect(options[3]).toHaveAttribute("title");
    expect(options[3]).toHaveTextContent(
      "This device does not support fixed models",
    );
  });

  it("模型在 daemon 上不存在 → fixed-model、最近使用都禁用，并提供重新同步入口", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const onSyncProvider = vi.fn();
    recordRecentTarget("backend", "7", {
      providerKey: "k-anthropic",
      modelKey: "mk-opus",
    });
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={onChange}
        onSyncProvider={onSyncProvider}
        catalog={catalog()}
        executionLocation="7"
        supportsFixedModel
        remoteCatalog={remoteCatalogWith()}
      />,
    );

    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const list = await screen.findByRole("listbox", { name: "LLM Provider" });
    const options = within(list).getAllByRole("option");
    // daemon 上存在 k-anthropic 的默认模型，但没有 mk-opus → 该 fixed-model 需同步。
    expect(options[1]).not.toHaveAttribute("aria-disabled", "true");
    // 当前默认模型在 daemon 上存在，可固定；mk-opus 缺失，需先同步。
    expect(options[2]).not.toHaveAttribute("aria-disabled", "true");
    expect(options[3]).toHaveAttribute("aria-disabled", "true");
    expect(options[3]).toHaveTextContent(
      "Local only — sync to this device first",
    );

    // 最近使用不能绕过同一远端门控。
    const recent = screen.getByRole("button", { name: "claude-opus-4-8" });
    expect(recent).toBeDisabled();
    await user.click(recent);
    expect(onChange).not.toHaveBeenCalled();

    // Provider 已存在但目录过期时仍需提供整包重新同步入口。
    const sync = screen.getByRole("button", {
      name: "Sync Anthropic to this device",
    });
    await user.click(sync);
    expect(onSyncProvider).toHaveBeenCalledWith(
      expect.objectContaining({ providerKey: "k-anthropic" }),
    );
  });

  it("daemon 的默认模型与本机不一致时，provider-default 禁用并要求同步", async () => {
    const user = userEvent.setup();
    const localCatalog = catalog();
    localCatalog[0] = {
      ...localCatalog[0],
      defaultModel: localCatalog[0].models[1],
    };
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        onSyncProvider={vi.fn()}
        catalog={localCatalog}
        executionLocation="7"
        supportsFixedModel
        remoteCatalog={remoteCatalogWith()}
      />,
    );

    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const list = await screen.findByRole("listbox", { name: "LLM Provider" });
    const providerDefault = within(list).getByRole("option", {
      name: /Follow this provider's default/i,
    });
    expect(providerDefault).toHaveAttribute("aria-disabled", "true");
    expect(providerDefault).toHaveTextContent(
      "Local only — sync to this device first",
    );
    expect(
      screen.getByRole("button", { name: "Sync Anthropic to this device" }),
    ).toBeInTheDocument();
  });

  it("远端场景顶部说明以目标设备的配置为准，设备上已有的供应商组带标记；未传设备名时不渲染说明", async () => {
    const user = userEvent.setup();
    const { unmount } = render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="piagent"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
        executionLocation="7"
        supportsFixedModel
        remoteCatalog={remoteCatalogWith()}
        deviceLabel="build-01"
      />,
    );

    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const header = await screen.findByTestId("remote-device-header");
    expect(header).toHaveTextContent("build-01");
    expect(header).toHaveTextContent(/configuration/i);
    // 设备上已有的供应商组标注，只在设备上真有的那一组出现。
    const list = screen.getByRole("listbox", { name: "LLM Provider" });
    const groups = within(list).getAllByTestId("picker-group");
    const anthropic = groups.find(
      (g) => g.getAttribute("data-provider-key") === "k-anthropic",
    );
    const openai = groups.find(
      (g) => g.getAttribute("data-provider-key") === "k-openai",
    );
    expect(
      within(anthropic as HTMLElement).getByText("Already on this device"),
    ).toBeInTheDocument();
    expect(
      within(openai as HTMLElement).queryByText("Already on this device"),
    ).not.toBeInTheDocument();
    unmount();

    // 本机 / 未传设备名 → 不渲染设备说明（既有消费方保持原形态）。
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="piagent"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
        executionLocation="7"
        supportsFixedModel
        remoteCatalog={remoteCatalogWith()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    await screen.findByRole("listbox", { name: "LLM Provider" });
    expect(
      screen.queryByTestId("remote-device-header"),
    ).not.toBeInTheDocument();
  });

  it("「同步过去」落在需要同步的那一行内，而不是列表底部聚合", async () => {
    const user = userEvent.setup();
    const onSyncProvider = vi.fn();
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="piagent"
        selected={null}
        onChange={vi.fn()}
        onSyncProvider={onSyncProvider}
        catalog={catalog()}
        executionLocation="7"
        supportsFixedModel
        remoteCatalog={remoteCatalogWith()}
      />,
    );

    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const list = await screen.findByRole("listbox", { name: "LLM Provider" });
    // 按钮在列表里、与它会修复的那一行同处一个 li；每个供应商只出现一次。
    const syncs = within(list).getAllByRole("button", {
      name: "Sync OpenAI to this device",
    });
    expect(syncs).toHaveLength(1);
    const row = syncs[0].closest("li") as HTMLElement;
    expect(within(row).getByRole("option")).toHaveTextContent(
      "Follow this provider's default",
    );
    expect(within(row).getByRole("option")).toHaveAttribute(
      "aria-disabled",
      "true",
    );
    await user.click(syncs[0]);
    expect(onSyncProvider).toHaveBeenCalledWith(
      expect.objectContaining({ providerKey: "k-openai" }),
    );
  });

  it("没有真实同步入口（未传 onSyncProvider）时不渲染任何同步按钮", async () => {
    const user = userEvent.setup();
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="piagent"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
        executionLocation="7"
        supportsFixedModel
        remoteCatalog={remoteCatalogWith()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    await screen.findByRole("listbox", { name: "LLM Provider" });
    expect(
      screen.queryByRole("button", { name: /Sync .* to this device/ }),
    ).not.toBeInTheDocument();
  });

  it("本机（无 remoteCatalog）fixed-model 照常可选，不受门控影响", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={onChange}
        catalog={catalog()}
      />,
    );

    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const list = await screen.findByRole("listbox", { name: "LLM Provider" });
    const options = within(list).getAllByRole("option");
    expect(options[3]).not.toHaveAttribute("aria-disabled", "true");
    await user.click(options[3]);
    expect(onChange).toHaveBeenCalledWith({
      providerKey: "k-anthropic",
      modelKey: "mk-opus",
    });
  });

  it("远端底部只留「同步会复制 API Key」一句，不再总述灰色项原因；无同步入口时整块不渲染", async () => {
    const user = userEvent.setup();
    const { unmount } = render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="piagent"
        selected={null}
        onChange={vi.fn()}
        onSyncProvider={vi.fn()}
        catalog={catalog()}
        executionLocation="7"
        supportsFixedModel
        remoteCatalog={remoteCatalogWith()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    await screen.findByRole("listbox", { name: "LLM Provider" });
    expect(
      screen.getByText(/Syncing copies the API key to that device/),
    ).toBeInTheDocument();
    // 行内每条已写自己的原因，底部不再总述一遍。
    expect(screen.queryByText(/Disabled items need sync/)).toBeNull();
    expect(
      screen.getByRole("button", { name: "Sync OpenAI to this device" }),
    ).toHaveClass("cursor-pointer");
    unmount();

    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="piagent"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
        executionLocation="7"
        supportsFixedModel
        remoteCatalog={remoteCatalogWith()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    await screen.findByRole("listbox", { name: "LLM Provider" });
    expect(screen.queryByText(/Syncing copies the API key/)).toBeNull();
  });

  it("禁用项收敛成两行：行内原因取代模型标识副行，不在 modelId 之下再叠第三行", async () => {
    const user = userEvent.setup();
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
        executionLocation="7"
        supportsFixedModel
        remoteCatalog={remoteCatalogWith()}
      />,
    );

    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const list = await screen.findByRole("listbox", { name: "LLM Provider" });
    const options = within(list).getAllByRole("option");
    // mockup 的停用行是「模型名 / 原因」两行 —— 原因取代 modelId 副行。
    expect(options[3]).toHaveTextContent("Opus");
    expect(options[3]).toHaveTextContent(
      "Local only — sync to this device first",
    );
    expect(
      within(options[3] as HTMLElement).queryByText("claude-opus-4-8"),
    ).not.toBeInTheDocument();
  });
});

describe("ModelTargetPicker mockup 结构对齐", () => {
  it("顶部特殊项是独立描边卡片，未选中虚线、选中转实线 primary", async () => {
    const user = userEvent.setup();
    const { unmount } = render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={{ providerKey: "k-anthropic", modelKey: "mk-opus" }}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    let special = await screen.findByRole("option", {
      name: /CLI login state/,
    });
    expect(special).toHaveClass(
      "border",
      "border-dashed",
      "border-border-strong",
    );
    expect(special).not.toHaveClass("border-primary");
    unmount();

    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    special = await screen.findByRole("option", { name: /CLI login state/ });
    expect(special).toHaveClass("border-solid", "border-primary");
  });

  it("特殊项图标分场景：chat=UserRound / backend=Lock / route=GitBranch", async () => {
    const user = userEvent.setup();
    async function iconFor(scenario: "chat" | "backend" | "route") {
      const view = render(
        <ModelTargetPicker
          scenario={scenario}
          aria-label="LLM Provider"
          backendType="claudecode"
          selected={null}
          onChange={vi.fn()}
          catalog={catalog()}
        />,
      );
      await user.click(screen.getByRole("button", { name: "LLM Provider" }));
      const icon = await screen.findByTestId("special-icon");
      const className = icon.getAttribute("class") ?? "";
      const scenarioAttr = icon.getAttribute("data-scenario");
      view.unmount();
      return { className, scenarioAttr };
    }

    const chat = await iconFor("chat");
    expect(chat.scenarioAttr).toBe("chat");
    expect(chat.className).toContain("lucide-user-round");

    const backend = await iconFor("backend");
    expect(backend.scenarioAttr).toBe("backend");
    expect(backend.className).toContain("lucide-lock");

    const route = await iconFor("route");
    expect(route.scenarioAttr).toBe("route");
    expect(route.className).toContain("lucide-git-branch");
  });

  it("specialSublabel 可以是节点：节点原样渲染，搜索只按特殊项标题匹配、不混进 [object Object]", async () => {
    const user = userEvent.setup();
    render(
      <ModelTargetPicker
        scenario="chat"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
        specialSublabel={
          <span>
            <span
              data-testid="special-sub-brand"
              role="img"
              aria-label="Claude"
            />
            <span>Anthropic · claude-sonnet-4-6</span>
          </span>
        }
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const special = await screen.findByRole("option", {
      name: /Follow agent binding/,
    });
    expect(
      within(special).getByTestId("special-sub-brand"),
    ).toBeInTheDocument();
    // 节点绝不能被拼成字符串（`sublabel` 曾同时用于渲染与搜索）。
    expect(screen.queryByText(/\[object Object\]/)).not.toBeInTheDocument();

    const search = screen.getByPlaceholderText("Search providers or models…");
    await user.type(search, "object");
    expect(
      screen.queryByRole("option", { name: /Follow agent binding/ }),
    ).not.toBeInTheDocument();

    await user.clear(search);
    await user.type(search, "agent binding");
    expect(
      await screen.findByRole("option", { name: /Follow agent binding/ }),
    ).toBeInTheDocument();
  });

  it("固定模型行不再逐行重复品牌标识，品牌只由 sticky 组头承载", async () => {
    const user = userEvent.setup();
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const list = await screen.findByRole("listbox", { name: "LLM Provider" });
    const options = within(list).getAllByRole("option");
    expect(options[2]).toHaveAttribute("data-kind", "fixed");
    expect(within(options[2] as HTMLElement).queryByRole("img")).toBeNull();
    expect(within(options[3] as HTMLElement).queryByRole("img")).toBeNull();
    // 组头仍然带品牌标识。
    const group = within(list).getAllByTestId("picker-group")[0];
    expect(
      within(group).getByRole("img", { name: "Anthropic" }),
    ).toBeInTheDocument();
  });

  it("最近使用 chip 带品牌标识并改成矮方角描边形，按钮无障碍名仍只是模型标识", async () => {
    const user = userEvent.setup();
    recordRecentTarget("backend", "", {
      providerKey: "k-anthropic",
      modelKey: "mk-opus",
    });
    recordRecentTarget("backend", "", {
      providerKey: "k-anthropic",
      modelKey: "",
    });
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    // 无障碍名不被品牌标识污染（品牌标识 aria-hidden）。
    const label = await screen.findByRole("button", {
      name: "claude-opus-4-8",
    });
    expect(label).toHaveClass("font-mono");
    const chip = label.parentElement as HTMLElement;
    expect(chip).toHaveClass(
      "h-[22px]",
      "rounded-md",
      "border-border",
      "bg-card",
    );
    expect(chip).not.toHaveClass("rounded-full");
    // 品牌标识确实渲染了，只是对无障碍树隐藏。
    expect(within(chip).getByRole("img", { hidden: true })).toBeInTheDocument();

    // provider-default 型 recent 用供应商标识。
    const providerChip = (
      screen.getByRole("button", { name: "Anthropic" }) as HTMLElement
    ).parentElement as HTMLElement;
    expect(
      within(providerChip).getByRole("img", { hidden: true }),
    ).toHaveAttribute("aria-label", "Anthropic");
  });

  it("有供应商因兼容性被隐藏时不挂总述说明；一个兼容供应商都没有才给空态说明", async () => {
    const user = userEvent.setup();
    const { unmount } = render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );
    // claudecode 只收 anthropic → OpenAI 被隐藏，但不为此在列表末尾加一条总述。
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    await screen.findByRole("listbox", { name: "LLM Provider" });
    expect(screen.queryByTestId("compatibility-note")).not.toBeInTheDocument();
    expect(screen.queryByText(/hidden/)).toBeNull();
    unmount();

    // 空态说明保留：一个兼容供应商都没有时仍要解释为什么列表是空的。
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={[
          {
            providerKey: "k-openai",
            id: 2,
            name: "OpenAI",
            type: "openai-response",
            enabled: true,
            defaultModel: null,
            models: [],
          },
        ]}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    expect(
      await screen.findByText(/No providers compatible with this backend type/),
    ).toBeInTheDocument();
  });

  it("搜索框是有边框的输入框，放大镜绝对定位在框内", async () => {
    const user = userEvent.setup();
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const input = await screen.findByPlaceholderText(
      "Search providers or models…",
    );
    // mockup .search .input：h32 / 有边框 / input-bg 底 / 左内边距给放大镜让位。
    expect(input).toHaveClass("h-8", "bg-input-bg", "pl-7");
    expect(input).not.toHaveClass("border-0");
    expect(input.parentElement).toHaveClass("relative");
    // 清空按钮仍在。
    await user.type(input, "opus");
    expect(
      screen.getByRole("button", { name: "Clear search" }),
    ).toBeInTheDocument();
  });

  it("列表可视高度对齐 mockup .plistw（330px）", async () => {
    const user = userEvent.setup();
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const list = await screen.findByRole("listbox", { name: "LLM Provider" });
    expect(list.parentElement).toHaveClass("max-h-[330px]");
    expect(list.parentElement).not.toHaveClass("max-h-64");
  });

  it("失效横幅拆成加粗标题 + 正文；footer 用 card 底", async () => {
    const user = userEvent.setup();
    render(
      <ModelTargetPicker
        scenario="route"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={{ providerKey: "k-gone", modelKey: "mk-gone" }}
        onChange={vi.fn()}
        catalog={catalog()}
        invalid
        footer="Applies from the next turn"
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const banner = await screen.findByTestId("invalid-banner");
    const title = within(banner).getByText("This target is no longer valid");
    expect(title.tagName).toBe("STRONG");
    // 正文继续用既有 invalidTarget 文案。
    expect(banner).toHaveTextContent("k-gone");
    expect(banner).toHaveTextContent(/will not automatically fall back/);
    // mockup .pop .foot { background: var(--card) }
    expect(screen.getByText("Applies from the next turn")).toHaveClass(
      "bg-card",
    );
  });

  it("所有可点元素都是手形光标，禁用项保持 not-allowed", async () => {
    const user = userEvent.setup();
    recordRecentTarget("backend", "", {
      providerKey: "k-anthropic",
      modelKey: "mk-opus",
    });
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );
    const trigger = screen.getByRole("button", { name: "LLM Provider" });
    expect(trigger).toHaveClass("cursor-pointer");

    await user.click(trigger);
    const list = await screen.findByRole("listbox", { name: "LLM Provider" });
    const options = within(list).getAllByRole("option");
    expect(options[1]).toHaveClass("cursor-pointer");
    // mockup .opt.dis { cursor: not-allowed }
    expect(options[1]).toHaveClass("disabled:cursor-not-allowed");

    expect(screen.getByRole("button", { name: "claude-opus-4-8" })).toHaveClass(
      "cursor-pointer",
    );
    expect(
      screen.getByRole("button", { name: "Remove claude-opus-4-8" }),
    ).toHaveClass("cursor-pointer");

    await user.type(
      screen.getByPlaceholderText("Search providers or models…"),
      "opus",
    );
    expect(screen.getByRole("button", { name: "Clear search" })).toHaveClass(
      "cursor-pointer",
    );
  });

  it("供应商组头用弹层底色，并对齐 mockup .pgrp 的字号/字重/字距", async () => {
    const user = userEvent.setup();
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const list = await screen.findByRole("listbox", { name: "LLM Provider" });
    const group = within(list).getAllByTestId("picker-group")[0];
    // bg-background 在暗色下比 popover 更黑，会在弹层里拉出一条黑带。
    expect(group).toHaveClass("bg-popover");
    expect(group).not.toHaveClass("bg-background");
    expect(group).toHaveClass(
      "text-[10px]",
      "font-semibold",
      "uppercase",
      "tracking-[0.06em]",
    );
  });

  it("触发按钮与弹层容器对齐 mockup .trigger / .pop，且消费方 className 仍可覆盖", async () => {
    const user = userEvent.setup();
    const { rerender } = render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );
    const trigger = screen.getByRole("button", { name: "LLM Provider" });
    expect(trigger).toHaveClass("rounded-lg", "border-input", "bg-input-bg");

    await user.click(trigger);
    const pop = await screen.findByTestId("model-target-popover");
    expect(pop).toHaveClass("rounded-[10px]", "overflow-hidden");

    // composer pill 靠 className 改外观：cn() 里消费方必须在最后。
    rerender(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
        className="rounded-full bg-transparent"
      />,
    );
    const overridden = screen.getByRole("button", { name: "LLM Provider" });
    expect(overridden).toHaveClass("rounded-full", "bg-transparent");
    expect(overridden).not.toHaveClass("rounded-lg", "bg-input-bg");
  });

  it("选中态用中性强调：选中的固定模型行是 bg-accent，primary-soft 只留给跟随默认行", async () => {
    const user = userEvent.setup();
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={{ providerKey: "k-anthropic", modelKey: "mk-opus" }}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const list = await screen.findByRole("listbox", { name: "LLM Provider" });
    const options = within(list).getAllByRole("option");
    // 把活动态移到别处，确保断言测的是「选中」而不是「活动」。
    await user.hover(options[2]);
    const selectedFixed = options[3];
    expect(selectedFixed).toHaveAttribute("aria-selected", "true");
    expect(selectedFixed).toHaveAttribute("data-kind", "fixed");
    expect(selectedFixed).toHaveClass("bg-accent");
    expect(selectedFixed).not.toHaveClass("bg-primary-soft");

    // 守卫：蓝底是「动态跟随」的语义编码，整张列表里只能出现在 provider-default 行上。
    for (const option of options) {
      if (option.classList.contains("bg-primary-soft")) {
        expect(option).toHaveAttribute("data-kind", "provider-default");
      }
    }
    // 跟随默认行即便没被选中也保持蓝底，不因选中态改中性而丢掉语义。
    expect(options[1]).toHaveAttribute("data-kind", "provider-default");
    expect(options[1]).toHaveClass("bg-primary-soft");
  });

  it("跟随默认行被选中时保留蓝底，不被中性选中底盖掉", async () => {
    const user = userEvent.setup();
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={{ providerKey: "k-anthropic", modelKey: "" }}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const list = await screen.findByRole("listbox", { name: "LLM Provider" });
    const options = within(list).getAllByRole("option");
    await user.hover(options[2]);
    expect(options[1]).toHaveAttribute("aria-selected", "true");
    expect(options[1]).toHaveClass("bg-primary-soft");
    expect(options[1]).not.toHaveClass("bg-accent");
  });

  it("跟随默认行的 hover / 键盘活动态是提亮高亮，不是描边 ring", async () => {
    const user = userEvent.setup();
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const list = await screen.findByRole("listbox", { name: "LLM Provider" });
    const providerDefault = within(list).getAllByRole("option")[1];
    expect(providerDefault).toHaveAttribute("data-kind", "provider-default");
    // mockup .opt.dyn:hover 只改 filter，底色不换、更不画边。
    expect(providerDefault).toHaveClass(
      "hover:brightness-95",
      "dark:hover:brightness-125",
    );
    expect(providerDefault).not.toHaveClass("ring-1");
    expect(providerDefault).not.toHaveClass("ring-ring/40");

    // 键盘/指针活动态用同一套提亮，不换成 ring。
    await user.hover(providerDefault);
    expect(providerDefault).toHaveClass("brightness-95", "dark:brightness-125");
    expect(providerDefault).not.toHaveClass("ring-1");
    // 焦点可见性不能丢。
    expect(providerDefault).toHaveClass("focus-visible:ring-2");
  });

  it("普通行 hover 是满色 accent，禁用行完全没有 hover 高亮", async () => {
    const user = userEvent.setup();
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));
    const list = await screen.findByRole("listbox", { name: "LLM Provider" });
    const options = within(list).getAllByRole("option");
    expect(options[2]).toHaveClass("hover:bg-accent");
    expect(options[2]).not.toHaveClass("hover:bg-accent/60");
    // 选中项 hover 不翻色（mockup .opt.sel 排在 .opt:hover 之后）。
    // 先把活动态移开，否则 options[0] 自带 active 的底色，断言会空过。
    await user.hover(options[2]);
    expect(options[0]).toHaveAttribute("aria-selected", "true");
    expect(options[0]).toHaveClass("bg-accent");
    expect(options[0]).not.toHaveClass("hover:bg-accent");
    expect(options[0]).not.toHaveClass("hover:bg-accent/60");
    // mockup .opt.dis:hover { background: transparent }
    const disabled = options.find(
      (o) => o.getAttribute("aria-disabled") === "true",
    ) as HTMLElement;
    expect(disabled).toBeDefined();
    expect(disabled).not.toHaveClass("hover:bg-accent");
    expect(disabled).toHaveClass("disabled:cursor-not-allowed");
  });

  it("最近使用 chip 整颗是手形且 hover 加深描边；停用 chip 不给 hover 提示、光标 not-allowed", async () => {
    const user = userEvent.setup();
    recordRecentTarget("backend", "", {
      providerKey: "k-anthropic",
      modelKey: "mk-opus",
    });
    recordRecentTarget("backend", "", {
      providerKey: "k-anthropic",
      modelKey: "mk-off", // 模型已停用 → chip 不可选
    });
    render(
      <ModelTargetPicker
        scenario="backend"
        aria-label="LLM Provider"
        backendType="claudecode"
        selected={null}
        onChange={vi.fn()}
        catalog={catalog()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "LLM Provider" }));

    const okChip = (
      await screen.findByRole("button", { name: "claude-opus-4-8" })
    ).parentElement as HTMLElement;
    expect(okChip).toHaveClass("cursor-pointer", "hover:border-border-strong");

    const offChip = (screen.getByRole("button", { name: "claude-old" })
      .parentElement || null) as HTMLElement;
    expect(offChip).toHaveClass("cursor-not-allowed");
    expect(offChip).not.toHaveClass("hover:border-border-strong");

    // mockup .rchip .rx：subtle 基线色，hover 才上 accent 底 + foreground 字。
    expect(
      screen.getByRole("button", { name: "Remove claude-opus-4-8" }),
    ).toHaveClass(
      "text-muted-foreground",
      "hover:bg-accent",
      "hover:text-foreground",
    );
  });

  it("两个 locale 同步：invalidTitle 两边都有", () => {
    for (const locale of [enCommon, zhCommon]) {
      const picker = locale.modelTargetPicker as Record<string, unknown>;
      expect(typeof picker.invalidTitle).toBe("string");
    }
    expect(
      (zhCommon.modelTargetPicker as Record<string, unknown>).invalidTitle,
    ).toBe("当前目标已失效");
    expect(
      (enCommon.modelTargetPicker as Record<string, unknown>).invalidTitle,
    ).toBe("This target is no longer valid");
  });
});
