import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { agentreUiResources } from "../i18n";
import { AutoTriggerBanner } from "./auto-trigger-banner";

const en = agentreUiResources.en;
const zh = agentreUiResources["zh-CN"];

// 这张分隔卡的职责是解释「为什么凭空多出一条 assistant 消息」。sess-3797 之前非用户
// 发起的轮只有一种(CLI 的后台任务完成续轮),文案因此可以写死;现在多了一种 ——
// 子进程被 agentre 之外的东西叫醒、自己起的一轮。两者在转录结构上一模一样,只能靠
// 消息自己带的 trigger 分辨,否则外部唤醒的轮会被谎报成「后台任务完成」。
describe("AutoTriggerBanner", () => {
  it("不传 trigger 时仍是后台任务完成续轮那句 —— 既有调用点逐字不变", () => {
    render(<AutoTriggerBanner />);

    expect(screen.getByRole("separator")).toHaveTextContent(
      en.chatPanel.autonomous.banner,
    );
  });

  it("external 的一轮说外部唤醒,不谎报成后台任务完成", () => {
    render(<AutoTriggerBanner trigger="external" />);

    const separator = screen.getByRole("separator");
    expect(separator).toHaveTextContent(en.chatPanel.autonomous.externalBanner);
    expect(separator).not.toHaveTextContent(en.chatPanel.autonomous.banner);
  });

  it("未知 trigger 退回既有那句,不把 key 直接印给用户", () => {
    // 后端将来多一种 trigger 时,前端最坏也只是少说一句,不能空着或漏出取值本身。
    render(<AutoTriggerBanner trigger="some-future-trigger" />);

    const separator = screen.getByRole("separator");
    expect(separator).toHaveTextContent(en.chatPanel.autonomous.banner);
    expect(separator).not.toHaveTextContent("some-future-trigger");
  });

  it("两种语言都给得出外部唤醒这句", () => {
    expect(en.chatPanel.autonomous.externalBanner).toBeTruthy();
    expect(zh.chatPanel.autonomous.externalBanner).toBeTruthy();
    expect(en.chatPanel.autonomous.externalAria).toBeTruthy();
    expect(zh.chatPanel.autonomous.externalAria).toBeTruthy();
  });
});
