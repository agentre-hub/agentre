import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { TooltipProvider } from "../ui/tooltip";
import i18next from "i18next";

import { AGENTRE_UI_NAMESPACE } from "../i18n";

// 期望文案按 key 算而不是硬编码字符串。包的文案住在自己的 namespace 下
// （`useUiTranslation()` = `useTranslation(AGENTRE_UI_NAMESPACE)`），而默认实例
// 由宿主持有：独立跑包测试时 vitest.setup.ts 扮演最小宿主，跑在宿主 vitest 里
// 时宿主的 src/i18n 已把同一份 bundle 合进去。两种情况下这条都成立。
const i18n = {
  t: (key: string, opts?: Record<string, unknown>) =>
    i18next.t(key, { ...opts, ns: AGENTRE_UI_NAMESPACE }),
};
import {
  TranscriptRenderContext,
  TranscriptRowView,
} from "./transcript-row-view";

import type { TranscriptRow } from "./transcript-rows";

// 供应商回退等持久 notice 走既有 NoticeBlock 渲染：Text 原样显示。
// （#26 的结构化模型偏离提示已随 model_override 整体移除，ChatBlock 不再有
// selectedModel / actualModel 字段，见 specs/2026-08-09-new-session-provider-select.md。）

type NoticeBlock = {
  text?: string;
  providerKey?: string;
  providerName?: string;
  modelKey?: string;
  modelName?: string;
  noticeKind?: string;
  reasoningEffort?: string;
};

function noticeRow(block: NoticeBlock): TranscriptRow {
  return {
    key: "message:9:notice:0",
    messageId: 9,
    message: {
      id: 9,
      role: "assistant",
      blocks: [{ type: "notice", ...block }],
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
      block: { type: "notice", ...block },
    },
    isFirstOfMessage: true,
    isLastOfMessage: true,
    autonomous: false,
  } as unknown as TranscriptRow;
}

function renderRow(row: TranscriptRow) {
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

describe("transcript notice block", () => {
  it("renders a notice text verbatim", () => {
    renderRow(
      noticeRow({
        text: "Selected provider X is unavailable; fell back to the agent binding",
      }),
    );

    expect(
      screen.getByText(
        "Selected provider X is unavailable; fell back to the agent binding",
      ),
    ).toBeDefined();
  });

  it("renders an empty-text notice as an empty status box (no crash)", () => {
    renderRow(noticeRow({ text: "" }));
    expect(screen.getByTestId("transcript-notice")).toBeInTheDocument();
  });

  it("renders a provider-fallback notice（结构化 providerKey）走 i18n 文案", () => {
    // 决策 8：会话所选供应商缺失/停用 → 回退 agent 绑定并追加持久 notice，
    // 文案「所选供应商 X 不可用，已回退 agent 绑定」必须显示，而不是空框。
    renderRow(noticeRow({ providerKey: "gone-provider" }));

    expect(
      screen.getByText(
        i18n.t("chat.notice.providerFallback.sentence", {
          provider: "gone-provider",
        }),
      ),
    ).toBeInTheDocument();
  });

  it("renders a provider-switch notice（noticeKind=switch,非空 providerKey）走切换文案（决策 9）", () => {
    // 用户在会话里切到某个具体供应商：与回退 notice 共用 providerKey 字段,但
    // noticeKind="switch" 才是判据 —— 文案必须是「切换」而不是「回退」。
    renderRow(
      noticeRow({ providerKey: "acme-anthropic", noticeKind: "switch" }),
    );

    expect(
      screen.getByText(
        i18n.t("chat.notice.providerSwitch.sentence", {
          provider: "acme-anthropic",
        }),
      ),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(
        i18n.t("chat.notice.providerFallback.sentence", {
          provider: "acme-anthropic",
        }),
      ),
    ).not.toBeInTheDocument();
  });

  it("renders a provider-switch notice 清回「跟随 agent 绑定」（noticeKind=switch,providerKey 空串）", () => {
    // 决策 9：providerKey 空串表示改回跟随 agent 绑定 / CLI 登录态 —— 不能靠
    // 「providerKey 非空」判定负载有效，必须走 noticeKind。
    renderRow(noticeRow({ providerKey: "", noticeKind: "switch" }));

    expect(
      screen.getByText(i18n.t("chat.notice.providerSwitch.followAgentBinding")),
    ).toBeInTheDocument();
  });

  it("renders a provider-switch notice 固定模型（noticeKind=switch + modelKey/modelName）走 fixed-model 文案", () => {
    // spec 2026-08-11 决策 1：切换 notice 扩展 modelKey/modelName 负载；fixed-model 切换
    // 时 transcript 要读得出「固定到哪个模型」，而不是只剩供应商名。
    renderRow(
      noticeRow({
        providerKey: "acme-anthropic",
        providerName: "Acme",
        modelKey: "mk-haiku",
        modelName: "Claude Haiku",
        noticeKind: "switch",
      }),
    );

    expect(
      screen.getByText(
        i18n.t("chat.notice.providerSwitch.fixedModel", {
          provider: "Acme",
          model: "Claude Haiku",
        }),
      ),
    ).toBeInTheDocument();
    // 不能掉回只有供应商名的旧文案，也不能泄漏裸 key。
    expect(
      screen.queryByText(
        i18n.t("chat.notice.providerSwitch.sentence", {
          provider: "Acme",
        }),
      ),
    ).not.toBeInTheDocument();
    expect(screen.queryByText(/mk-haiku/)).not.toBeInTheDocument();
  });

  it("renders a provider-switch notice 优先显示供应商展示名而非裸 key（决策 1）", () => {
    // 用户在 pill 上选的是供应商名，回看 transcript 应该读到同一个名字，而不是
    // providerKey 那串 UUID。
    renderRow(
      noticeRow({
        providerKey: "36a04495-dfe9-40ef-a3c5-2b62468db6b1",
        providerName: "中转 · GLM 5.2",
        noticeKind: "switch",
      }),
    );

    expect(
      screen.getByText(
        i18n.t("chat.notice.providerSwitch.sentence", {
          provider: "中转 · GLM 5.2",
        }),
      ),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(/36a04495-dfe9-40ef-a3c5-2b62468db6b1/),
    ).not.toBeInTheDocument();
  });

  it("renders a provider-fallback notice 优先显示供应商展示名而非裸 key（决策 2）", () => {
    // 回退 notice 与切换 notice 同构：供应商实体还在（只是停用/不兼容）时同样有名字。
    renderRow(
      noticeRow({ providerKey: "acme-anthropic", providerName: "备用网关" }),
    );

    expect(
      screen.getByText(
        i18n.t("chat.notice.providerFallback.sentence", {
          provider: "备用网关",
        }),
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText(/acme-anthropic/)).not.toBeInTheDocument();
  });

  it("Given a reasoning-effort switch notice, When it carries a level, Then it reads out which level took over（spec 2026-09-01 决策 7）", () => {
    // 后端把切换编码成 kind=reasoning_effort + reasoningEffort，Text 恒为空 ——
    // 这里读不出档位就意味着转录区落的是一个空灰框。
    renderRow(
      noticeRow({ noticeKind: "reasoning_effort", reasoningEffort: "xhigh" }),
    );

    expect(
      screen.getByText(
        i18n.t("chat.notice.reasoningEffortSwitch.sentence", {
          level: "xhigh",
        }),
      ),
    ).toBeInTheDocument();
  });

  it("Given a reasoning-effort switch notice, When the level is empty, Then it says the session went back to following the backend config", () => {
    // 空 reasoningEffort + 该 kind = 改回跟随后端配置：判据只能是 kind，
    // 不能看字段是否非空。
    renderRow(
      noticeRow({ noticeKind: "reasoning_effort", reasoningEffort: "" }),
    );

    expect(
      screen.getByText(
        i18n.t("chat.notice.reasoningEffortSwitch.followBackend"),
      ),
    ).toBeInTheDocument();
  });
});
