import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../../../wailsjs/go/app/App", () => ({
  GetAppSetting: vi.fn(),
  UpdateAppSettings: vi.fn(),
}));

import { GetAppSetting, UpdateAppSettings } from "../../../wailsjs/go/app/App";
import {
  DEFAULT_TAB_SETTINGS,
  useTabSettingsStore,
} from "../tab-settings-store";

const getMock = vi.mocked(GetAppSetting);
const updateMock = vi.mocked(UpdateAppSettings);

beforeEach(() => {
  vi.clearAllMocks();
  useTabSettingsStore.setState({ settings: { ...DEFAULT_TAB_SETTINGS } });
});

describe("tab-settings-store", () => {
  it("默认关闭：升级后不悄悄改变标签顺序", () => {
    expect(DEFAULT_TAB_SETTINGS).toEqual({ groupNewByProject: false });
  });

  it("Given 键从未设置过(reject) When load Then 按关闭处理", async () => {
    useTabSettingsStore.setState({ settings: { groupNewByProject: true } });
    getMock.mockRejectedValue(new Error("not found"));
    await useTabSettingsStore.getState().load();
    expect(getMock).toHaveBeenCalledWith({ key: "tabs.group_new_by_project" });
    expect(useTabSettingsStore.getState().settings.groupNewByProject).toBe(
      false,
    );
  });

  it("Given 已存 true When load Then 开启", async () => {
    getMock.mockResolvedValue({
      key: "tabs.group_new_by_project",
      value: "true",
    } as never);
    await useTabSettingsStore.getState().load();
    expect(useTabSettingsStore.getState().settings.groupNewByProject).toBe(
      true,
    );
  });

  it("Given 存的是非 true 的怪值 When load Then 按关闭处理", async () => {
    useTabSettingsStore.setState({ settings: { groupNewByProject: true } });
    getMock.mockResolvedValue({
      key: "tabs.group_new_by_project",
      value: "yes",
    } as never);
    await useTabSettingsStore.getState().load();
    expect(useTabSettingsStore.getState().settings.groupNewByProject).toBe(
      false,
    );
  });

  it('When save(true) Then 写入 "true" 并更新状态', async () => {
    updateMock.mockResolvedValue({} as never);
    await useTabSettingsStore.getState().save({ groupNewByProject: true });
    expect(updateMock).toHaveBeenCalledWith({
      entries: [{ key: "tabs.group_new_by_project", value: "true" }],
    });
    expect(useTabSettingsStore.getState().settings.groupNewByProject).toBe(
      true,
    );
  });

  it("Given load 在飞时 save(true) 已落库 When 更早发出的读取后回来 Then 不把开关盖回旧值", async () => {
    let resolveRead: (v: { key: string; value: string }) => void = () => {};
    getMock.mockReturnValue(
      new Promise((r) => {
        resolveRead = r;
      }) as never,
    );
    updateMock.mockResolvedValue({} as never);
    const loading = useTabSettingsStore.getState().load();
    await useTabSettingsStore.getState().save({ groupNewByProject: true });
    resolveRead({ key: "tabs.group_new_by_project", value: "false" });
    await loading;
    expect(useTabSettingsStore.getState().settings.groupNewByProject).toBe(
      true,
    );
  });

  it("Given 写入失败 When save Then 抛出且保持原值", async () => {
    updateMock.mockRejectedValue(new Error("db"));
    await expect(
      useTabSettingsStore.getState().save({ groupNewByProject: true }),
    ).rejects.toThrow("db");
    expect(useTabSettingsStore.getState().settings.groupNewByProject).toBe(
      false,
    );
  });
});
