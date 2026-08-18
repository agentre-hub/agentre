import { fireEvent, render, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { copyTextWithToast } = vi.hoisted(() => ({
  copyTextWithToast: vi.fn(),
}));

// copyTextWithToast 已搬进共享包，所以桩要打在包上；用 importOriginal 展开原模块
// 再覆盖这一个导出 —— 整包替换会连带把 cn / Button 等同源导出一起打空。
vi.mock("@agentre-ai/agentre-ui", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@agentre-ai/agentre-ui")>()),
  copyTextWithToast,
}));

import en from "../../../i18n/locales/en";
import zhCN from "../../../i18n/locales/zh-CN";
import { CommandCard } from "./command-card";

describe("CommandCard", () => {
  beforeEach(() => {
    copyTextWithToast.mockReset();
    copyTextWithToast.mockResolvedValue(true);
  });

  it("renders selectable command text and copies the exact command through the shared toast helper", () => {
    const command = "agentred service status";
    render(<CommandCard command={command} label="Check status" />);

    const region = screen.getByRole("group", { name: "Check status" });
    expect(within(region).getByText(command)).toHaveAttribute(
      "data-selectable-text",
      "true",
    );

    fireEvent.click(
      within(region).getByRole("button", { name: "Copy Check status" }),
    );
    expect(copyTextWithToast).toHaveBeenCalledWith(command, {
      successTitle: "Command copied",
    });
  });

  it("keeps every static onboarding command identical in both locales", () => {
    expect(zhCN.remoteDevices.onboarding.commands).toEqual(
      en.remoteDevices.onboarding.commands,
    );
  });
});
