import { describe, expect, it } from "vitest";

import { routeConclusion } from "./agent-backends-shared";
import { resolveModelTarget, truncateFlashText } from "./agent-backends-utils";
import type { PickerProvider } from "./model-target-picker";

describe("truncateFlashText", () => {
  it("短文本原样返回，truncated=false", () => {
    const r = truncateFlashText("✅ 128ms · pong");
    expect(r.display).toBe("✅ 128ms · pong");
    expect(r.truncated).toBe(false);
    expect(r.full).toBe("✅ 128ms · pong");
  });

  it("超过 80 字时截断 + …，truncated=true，full 保留原文", () => {
    const long = "a".repeat(300);
    const r = truncateFlashText(long);
    expect(r.truncated).toBe(true);
    expect(r.display.endsWith("…")).toBe(true);
    expect(r.display.length).toBeLessThanOrEqual(81); // 80 + …
    expect(r.full).toBe(long);
  });

  it("换行/制表符压成单空格防止 flash 行高被撑起", () => {
    const r = truncateFlashText("line1\nline2\t\tline3");
    expect(r.display).toBe("line1 line2 line3");
  });
});

const catalog: PickerProvider[] = [
  {
    providerKey: "pk-1",
    id: 1,
    name: "Anthropic",
    type: "anthropic",
    enabled: true,
    defaultModel: {
      modelKey: "mk-sonnet",
      modelId: "claude-sonnet-4-6",
      name: "Sonnet 4.6",
      enabled: true,
    },
    models: [
      {
        modelKey: "mk-sonnet",
        modelId: "claude-sonnet-4-6",
        name: "Sonnet 4.6",
        enabled: true,
      },
      { modelKey: "mk-opus", modelId: "claude-opus-4-8", enabled: true },
    ],
  },
];

describe("resolveModelTarget", () => {
  // 后端编辑器里「生效模型」写的是人读展示名，没填展示名才回落模型 ID。
  it("跟随供应商默认：写默认模型的展示名", () => {
    expect(resolveModelTarget("pk-1", "", catalog)).toMatchObject({
      mode: "provider-default",
      providerName: "Anthropic",
      modelLabel: "Sonnet 4.6",
    });
  });

  it("固定模型没填展示名：回落模型 ID", () => {
    expect(resolveModelTarget("pk-1", "mk-opus", catalog)).toMatchObject({
      mode: "fixed",
      modelLabel: "claude-opus-4-8",
    });
  });

  it("固定模型在目录里找不到：失效，退回原始 key", () => {
    expect(resolveModelTarget("pk-1", "mk-gone", catalog)).toMatchObject({
      mode: "invalid",
      modelLabel: "mk-gone",
    });
  });
});

describe("routeConclusion", () => {
  const t = ((key: string, opts?: { target?: string }) =>
    `${key}:${opts?.target ?? ""}`) as never;

  it("继承主绑定：结论写主绑定解析到的模型展示名", () => {
    const main = resolveModelTarget("pk-1", "mk-sonnet", catalog);
    expect(
      routeConclusion(t, { providerKey: "", modelKey: "" }, main, catalog),
    ).toBe("agentBackends.modelRoutes.inheritsSummary:Sonnet 4.6");
  });

  it("固定到另一模型：结论写该模型的展示名", () => {
    const main = resolveModelTarget("pk-1", "", catalog);
    expect(
      routeConclusion(
        t,
        { providerKey: "pk-1", modelKey: "mk-sonnet" },
        main,
        catalog,
      ),
    ).toBe("agentBackends.modelRoutes.fixedSummary:Sonnet 4.6");
  });
});
