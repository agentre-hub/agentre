import { describe, expect, it } from "vitest";

import { resolveProviderPillState } from "./pill-state";
import type { PickerModel, PickerProvider } from "./types";

function model(over: Partial<PickerModel> = {}): PickerModel {
  return {
    modelKey: "mk-sonnet",
    modelId: "claude-sonnet-4-6",
    name: "Sonnet 4.6",
    enabled: true,
    ...over,
  };
}

function provider(over: Partial<PickerProvider> = {}): PickerProvider {
  const models = over.models ?? [model()];
  return {
    providerKey: "pk-anthropic",
    id: 1,
    name: "Anthropic",
    type: "anthropic",
    enabled: true,
    defaultModel: models[0] ?? null,
    models,
    ...over,
  };
}

const catalog: PickerProvider[] = [provider()];

describe("resolveProviderPillState 跟随 Agent 绑定", () => {
  // 脸上要写「实际会跑哪个模型」，而绑定固定模型时那就是它本人 —— 不是 ↻。
  it("绑定固定模型：写出该模型的标识符，不标动态", () => {
    const s = resolveProviderPillState({
      boundProviderKey: "pk-anthropic",
      boundModelKey: "mk-sonnet",
      target: { providerKey: "", modelKey: "" },
      catalog,
    });
    expect(s.mode).toBe("follow-agent");
    expect(s.providerLabel).toBe("Anthropic");
    expect(s.modelLabel).toBe("claude-sonnet-4-6");
    expect(s.dynamic).toBe(false);
    expect(s.resolutionLabel).toBe("Anthropic · claude-sonnet-4-6");
  });

  // 只绑了供应商 = 跟着它当前的默认模型走，默认模型换了这里就跟着换 —— 那正是 ↻。
  it("只绑供应商：回落到该供应商当前默认模型并标动态", () => {
    const s = resolveProviderPillState({
      boundProviderKey: "pk-anthropic",
      boundModelKey: "",
      target: { providerKey: "", modelKey: "" },
      catalog,
    });
    expect(s.modelLabel).toBe("claude-sonnet-4-6");
    expect(s.dynamic).toBe(true);
  });

  // 绑定的 model key 还没拿到（新建会话的 Agent 条目就没有这一格）：即使目录里
  // 看得见默认模型，也不能据此断言它绑的是 provider-default —— 它也可能固定到了
  // 另一个模型。什么都不说才是实话。
  it("绑定 model key 还不知道时：不猜模型", () => {
    const s = resolveProviderPillState({
      boundProviderKey: "pk-anthropic",
      boundModelKey: undefined,
      target: { providerKey: "", modelKey: "" },
      catalog,
    });
    expect(s.modelLabel).toBe("");
    expect(s.dynamic).toBe(false);
    expect(s.resolutionLabel).toBe("Anthropic");
  });

  // cliLogin 是一句肯定的话，只有确知空串才配说；「还没拿到」说不出这句。
  it("确知没绑供应商才算 CLI 登录态", () => {
    expect(
      resolveProviderPillState({
        boundProviderKey: "",
        boundModelKey: "",
        target: { providerKey: "", modelKey: "" },
        catalog,
      }).cliLogin,
    ).toBe(true);
    expect(
      resolveProviderPillState({
        boundProviderKey: undefined,
        boundModelKey: undefined,
        target: { providerKey: "", modelKey: "" },
        catalog,
      }).cliLogin,
    ).toBe(false);
  });
});

describe("resolveProviderPillState 会话自己钉了目标", () => {
  it("只钉供应商 = 该供应商当前默认，标动态", () => {
    const s = resolveProviderPillState({
      boundProviderKey: "pk-other",
      boundModelKey: "",
      target: { providerKey: "pk-anthropic", modelKey: "" },
      catalog,
    });
    expect(s.mode).toBe("provider-default");
    expect(s.modelLabel).toBe("claude-sonnet-4-6");
    expect(s.dynamic).toBe(true);
    expect(s.cliLogin).toBe(false);
  });

  it("钉死模型 = 固定模型，不标动态", () => {
    const s = resolveProviderPillState({
      boundProviderKey: "",
      boundModelKey: "",
      target: { providerKey: "pk-anthropic", modelKey: "mk-sonnet" },
      catalog,
    });
    expect(s.mode).toBe("fixed");
    expect(s.modelLabel).toBe("claude-sonnet-4-6");
    expect(s.dynamic).toBe(false);
    // 会话自己选了目标，就不再由 CLI 登录账号决定 —— 哪怕绑定值是空的。
    expect(s.cliLogin).toBe(false);
  });
});

describe("resolveProviderPillState 失效", () => {
  it("供应商被停用：整个目标失效", () => {
    const s = resolveProviderPillState({
      boundProviderKey: "",
      boundModelKey: "",
      target: { providerKey: "pk-anthropic", modelKey: "mk-sonnet" },
      catalog: [provider({ enabled: false })],
    });
    expect(s.mode).toBe("invalid");
    expect(s.dynamic).toBe(false);
  });

  it("钉死的模型被停用：目标失效", () => {
    const s = resolveProviderPillState({
      boundProviderKey: "",
      boundModelKey: "",
      target: { providerKey: "pk-anthropic", modelKey: "mk-sonnet" },
      catalog: [provider({ models: [model({ enabled: false })] })],
    });
    expect(s.mode).toBe("invalid");
  });

  it("供应商在目录里根本找不到：失效，脸上退回原始 key", () => {
    const s = resolveProviderPillState({
      boundProviderKey: "",
      boundModelKey: "",
      target: { providerKey: "pk-gone", modelKey: "mk-gone" },
      catalog,
    });
    expect(s.mode).toBe("invalid");
    expect(s.providerLabel).toBe("pk-gone");
    expect(s.modelLabel).toBe("mk-gone");
  });

  it("只钉了供应商而它当前没有可用默认模型：不算失效，只是解析不到模型", () => {
    const s = resolveProviderPillState({
      boundProviderKey: "",
      boundModelKey: "",
      target: { providerKey: "pk-anthropic", modelKey: "" },
      catalog: [provider({ defaultModel: null })],
    });
    expect(s.mode).toBe("provider-default");
    expect(s.modelLabel).toBe("");
    expect(s.dynamic).toBe(false);
  });
});
