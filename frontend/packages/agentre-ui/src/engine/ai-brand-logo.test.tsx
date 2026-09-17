import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { AgentBackendLogo } from "./ai-brand-logo";

// 后端类型选择器里每个类型都必须有真品牌 mark。漏登记时不会报错，只会静默退化成
// 「灰底首字母」的 TextLogo——Hermes 就这么漏过一次，靠肉眼才看出来。新增后端类型
// 时先往 assets/brands/ 放 mark 并登记进 backendBrands，这条测试会拦住漏登记的。
const BACKEND_TYPES_WITH_BRAND = [
  "builtin",
  "claudecode",
  "codex",
  "piagent",
  "hermes",
  "openclaw",
] as const;

describe("AgentBackendLogo", () => {
  it.each(BACKEND_TYPES_WITH_BRAND)(
    "%s 渲染品牌 mark，而不是首字母兜底",
    (backendType) => {
      const { container } = render(
        <AgentBackendLogo backendType={backendType} />,
      );

      expect(container.querySelector("[data-brand]")).not.toBeNull();
      // TextLogo 会渲染首字母，品牌 mark 只渲染图
      expect(container.textContent).toBe("");
    },
  );

  it("Hermes 用官方徽章位图，不走单色 mask", () => {
    const { container } = render(<AgentBackendLogo backendType="hermes" />);

    const img = container.querySelector("img");
    expect(img).not.toBeNull();
    expect(img?.getAttribute("src")).toContain("hermes");
  });
});
