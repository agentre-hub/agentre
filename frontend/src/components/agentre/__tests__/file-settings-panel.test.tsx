import "@testing-library/jest-dom/vitest";

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const getMock = vi.fn(
  (_req: { key: string }): Promise<{ value: string }> =>
    Promise.reject(new Error("nf")),
);
const updateMock = vi.fn((_req: unknown) => Promise.resolve({}));
vi.mock("../../../../wailsjs/go/app/App", () => ({
  GetAppSetting: (req: { key: string }) => getMock(req),
  UpdateAppSettings: (req: unknown) => updateMock(req),
}));

vi.mock("@/hooks/use-chat-agents", () => ({
  useChatAgents: () => ({ agents: [] }),
}));
vi.mock("../sync", () => ({
  SyncPanel: () => null,
  useSyncStatus: () => ({ loading: false, status: null }),
}));

import i18n from "../../../i18n";
import {
  DEFAULT_FILE_SETTINGS,
  useFileSettingsStore,
} from "../../../stores/file-settings-store";
import { FileSettingsPanel } from "../file-settings-panel";
import { SettingsPage } from "../settings";

beforeEach(async () => {
  vi.clearAllMocks();
  getMock.mockImplementation(() => Promise.reject(new Error("nf")));
  await i18n.changeLanguage("zh-CN");
  useFileSettingsStore.setState({ settings: { ...DEFAULT_FILE_SETTINGS } });
});
afterEach(() => vi.restoreAllMocks());

describe("FileSettingsPanel", () => {
  it("读不到 files.open_action 时落回内置预览", async () => {
    render(<FileSettingsPanel />);
    // 读键失败(AppSettingNotFound)不该把这一页打空,它只是没被设置过。
    expect(
      await screen.findByRole("combobox", { name: "单击文件时" }),
    ).toHaveTextContent("在 agentre 内预览");
    expect(getMock).toHaveBeenCalledWith({ key: "files.open_action" });
  });

  it("已存 external 时显示外部应用", async () => {
    getMock.mockImplementation(() => Promise.resolve({ value: "external" }));
    render(<FileSettingsPanel />);
    expect(
      await screen.findByRole("combobox", { name: "单击文件时" }),
    ).toHaveTextContent("用外部应用打开");
  });

  it("切到外部应用会写回 files.open_action", async () => {
    const user = userEvent.setup();
    render(<FileSettingsPanel />);
    await user.click(
      await screen.findByRole("combobox", { name: "单击文件时" }),
    );
    await user.click(screen.getByRole("option", { name: "用外部应用打开" }));

    expect(updateMock).toHaveBeenCalledWith({
      entries: [{ key: "files.open_action", value: "external" }],
    });
    expect(useFileSettingsStore.getState().settings.openAction).toBe(
      "external",
    );
  });

  it("Row 的说明讲的是选中项的后果,不是又一条适用边界", () => {
    // spec「设置项」:Row 左侧是「单击文件时」与**一句后果说明**;两条「这个设置
    // 管不到」的情形归分区卡头部(决策 9),不在这里重复。
    render(<FileSettingsPanel />);
    expect(
      screen.getByText("预览会在右侧开一个标签，双击转成常驻标签。"),
    ).toBeVisible();
  });

  it("头部写明这个设置管不到的两种情形", () => {
    render(<FileSettingsPanel />);
    // spec 决策 9:适用边界写进分区卡头部,不做成第三个选项。
    expect(screen.getByRole("heading", { name: "打开方式" })).toBeVisible();
    expect(screen.getByText(/预览不了的文件/)).toBeVisible();
    expect(screen.getByText(/远端会话/)).toBeVisible();
  });
});

describe("设置 → 常规 → 文件", () => {
  it("「文件」是常规组的一项,选中后进的是文件设置页", async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={["/settings"]}>
        <SettingsPage
          effectiveTheme="light"
          onThemePreferenceChange={() => {}}
          themePreference="system"
        />
      </MemoryRouter>,
    );

    const nav = screen.getByTestId("settings-nav-files");
    await user.click(nav);

    expect(nav).toHaveAttribute("aria-current", "page");
    expect(
      await screen.findByRole("combobox", { name: "单击文件时" }),
    ).toBeVisible();
  });
});
