import "@testing-library/jest-dom/vitest";

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const getMock = vi.fn(
  (_req: { key: string }): Promise<{ value: string }> =>
    Promise.reject(new Error("nf")),
);
const updateMock = vi.fn((_req: unknown) => Promise.resolve({}));
vi.mock("../../../../../wailsjs/go/app/App", () => ({
  GetAppSetting: (req: { key: string }) => getMock(req),
  UpdateAppSettings: (req: unknown) => updateMock(req),
}));

import i18n from "../../../../i18n";
import { MemorySettings } from "../memory";

beforeEach(async () => {
  vi.clearAllMocks();
  getMock.mockImplementation(() => Promise.reject(new Error("nf")));
  await i18n.changeLanguage("zh-CN");
});

describe("MemorySettings", () => {
  it("键没被设置过时默认显示「保留」", async () => {
    render(<MemorySettings />);
    expect(
      await screen.findByRole("combobox", { name: "Claude Code 自带记忆" }),
    ).toHaveTextContent("保留");
    expect(getMock).toHaveBeenCalledWith({ key: "memory.claude_native" });
  });

  it("已存 false 时显示「关闭」", async () => {
    getMock.mockImplementation(() => Promise.resolve({ value: "false" }));
    render(<MemorySettings />);
    expect(
      await screen.findByRole("combobox", { name: "Claude Code 自带记忆" }),
    ).toHaveTextContent("关闭");
  });

  it("切到「关闭」写回 false,切回「保留」写回 true", async () => {
    const user = userEvent.setup();
    render(<MemorySettings />);
    const select = await screen.findByRole("combobox", {
      name: "Claude Code 自带记忆",
    });
    await user.click(select);
    await user.click(screen.getByRole("option", { name: "关闭" }));
    expect(updateMock).toHaveBeenLastCalledWith({
      entries: [{ key: "memory.claude_native", value: "false" }],
    });
    await user.click(select);
    await user.click(screen.getByRole("option", { name: "保留" }));
    expect(updateMock).toHaveBeenLastCalledWith({
      entries: [{ key: "memory.claude_native", value: "true" }],
    });
  });

  it("en 文案", async () => {
    await i18n.changeLanguage("en");
    render(<MemorySettings />);
    expect(
      await screen.findByRole("combobox", {
        name: "Claude Code native memory",
      }),
    ).toHaveTextContent("Keep");
  });
});
