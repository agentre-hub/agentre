import { describe, expect, it } from "vitest";

import enCommon from "@/i18n/locales/en";
import zhCommon from "@/i18n/locales/zh-CN";

// 宿主 common 语言树里的 modelTargetPicker 子树守卫。Picker 自身的行为用例已随
// 组件迁入共享包（packages/agentre-ui/src/engine/model-target-picker/picker.test.tsx），
// 这里只留下断言宿主语言树的那两条 —— 它们读的是 @/i18n/locales，不属于包。
describe("ModelTargetPicker", () => {
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
});

describe("ModelTargetPicker mockup 结构对齐", () => {
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
