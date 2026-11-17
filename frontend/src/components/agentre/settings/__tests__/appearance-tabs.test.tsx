import "@testing-library/jest-dom/vitest";

import { render, screen, waitFor } from "@testing-library/react";
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

const toast = vi.hoisted(() => ({ error: vi.fn(), success: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

import i18n from "../../../../i18n";
import {
  DEFAULT_TAB_SETTINGS,
  useTabSettingsStore,
} from "../../../../stores/tab-settings-store";
import { AppearanceSettings } from "../appearance";

function renderPage() {
  return render(
    <AppearanceSettings
      effectiveTheme="light"
      onThemePreferenceChange={() => {}}
      themePreference="system"
    />,
  );
}

beforeEach(async () => {
  vi.clearAllMocks();
  getMock.mockImplementation(() => Promise.reject(new Error("nf")));
  updateMock.mockImplementation(() => Promise.resolve({}));
  useTabSettingsStore.setState({ settings: { ...DEFAULT_TAB_SETTINGS } });
  await i18n.changeLanguage("zh-CN");
});

describe("外观 · 标签页：新对话靠拢同项目", () => {
  it("Given 从未设置过 Then 出现「标签页」组，开关默认关闭，说明讲清规则", async () => {
    renderPage();
    expect(screen.getByRole("heading", { name: "标签页" })).toBeInTheDocument();
    const sw = await screen.findByRole("switch", { name: "新对话靠拢同项目" });
    expect(sw).toHaveAttribute("aria-checked", "false");
    expect(
      screen.getByText(
        "新建对话时，新标签页放在同一项目最后一个标签页的右边；没有同项目的标签页时仍放在最右边。",
      ),
    ).toBeInTheDocument();
    expect(getMock).toHaveBeenCalledWith({ key: "tabs.group_new_by_project" });
  });

  it("外观页描述涵盖标签页设置", () => {
    renderPage();
    expect(
      screen.getByText("主题偏好、语言和标签页设置保存在当前设备。"),
    ).toBeInTheDocument();
  });

  it("Given 已存 true Then 开关显示为开", async () => {
    getMock.mockImplementation(() => Promise.resolve({ value: "true" }));
    renderPage();
    const sw = screen.getByRole("switch", { name: "新对话靠拢同项目" });
    await waitFor(() => expect(sw).toHaveAttribute("aria-checked", "true"));
  });

  it("When 打开开关 Then 写入 true；再关掉写入 false", async () => {
    const user = userEvent.setup();
    renderPage();
    const sw = screen.getByRole("switch", { name: "新对话靠拢同项目" });
    await user.click(sw);
    expect(updateMock).toHaveBeenLastCalledWith({
      entries: [{ key: "tabs.group_new_by_project", value: "true" }],
    });
    await waitFor(() => expect(sw).toHaveAttribute("aria-checked", "true"));
    await user.click(sw);
    expect(updateMock).toHaveBeenLastCalledWith({
      entries: [{ key: "tabs.group_new_by_project", value: "false" }],
    });
    await waitFor(() => expect(sw).toHaveAttribute("aria-checked", "false"));
  });

  it("Given 写入失败 When 打开开关 Then 提示「保存失败」且开关保持关闭", async () => {
    updateMock.mockImplementationOnce(() => Promise.reject(new Error("db")));
    const user = userEvent.setup();
    renderPage();
    const sw = screen.getByRole("switch", { name: "新对话靠拢同项目" });
    await user.click(sw);
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("保存失败", expect.anything()),
    );
    expect(sw).toHaveAttribute("aria-checked", "false");
    expect(useTabSettingsStore.getState().settings.groupNewByProject).toBe(
      false,
    );
  });

  it("en 文案", async () => {
    await i18n.changeLanguage("en");
    renderPage();
    expect(screen.getByRole("heading", { name: "Tabs" })).toBeInTheDocument();
    expect(
      screen.getByRole("switch", {
        name: "Keep new chats next to their project",
      }),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        "New chats open right after the last tab of the same project, or at the far right if there is none.",
      ),
    ).toBeInTheDocument();
  });
});
