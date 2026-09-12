import { describe, expect, it } from "vitest";

import { agentreUiResources } from "../i18n";

import { routeConclusion } from "./agent-backends-shared";
import {
  messageFromError,
  resolveModelTarget,
  truncateFlashText,
} from "./agent-backends-utils";
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

/**
 * 判据是包自己的语言包，而且是 **zh-CN** 那一份：这个 helper 的输出直接进 flash
 * 条给用户看，写死读 en 的实现会让中文界面上冒出一句英文。
 */
function translator(language: "zh-CN" | "en") {
  return (key: string) => {
    const value = key
      .split(".")
      .reduce<unknown>(
        (node, part) => (node as Record<string, unknown>)?.[part],
        agentreUiResources[language],
      );
    return typeof value === "string" ? value : key;
  };
}

describe("messageFromError", () => {
  it("Error 取 message 原文，不进 i18n", () => {
    expect(messageFromError(new Error("boom"), translator("zh-CN"))).toBe(
      "boom",
    );
  });

  it("字符串原样返回", () => {
    expect(messageFromError("plain", translator("zh-CN"))).toBe("plain");
  });

  it("可序列化的对象序列化后返回", () => {
    expect(messageFromError({ code: 7 }, translator("zh-CN"))).toBe(
      '{"code":7}',
    );
  });

  it("Given 序列化不了的错误值, When 界面语言是 zh-CN, Then 兜底文案是中文", () => {
    const circular: Record<string, unknown> = {};
    circular.self = circular;
    expect(messageFromError(circular, translator("zh-CN"))).toBe("未知错误");
  });

  it("Given 同一个错误值, When 界面语言是 en, Then 兜底文案跟着宿主语言走", () => {
    const circular: Record<string, unknown> = {};
    circular.self = circular;
    expect(messageFromError(circular, translator("en"))).toBe("Unknown error");
  });
});
