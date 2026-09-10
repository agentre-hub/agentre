import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";

vi.mock("../../../../wailsjs/go/app/App", () => ({
  Info: vi.fn().mockResolvedValue({ version: "0.6.0", commit: "9f8e7d6" }),
}));

import { Info } from "../../../../wailsjs/go/app/App";
import { INITIAL_UPDATE_STATE, useUpdateStore } from "@/stores/update-store";
import { useLatestAgentredVersion } from "./use-latest-agentred-version";

const mockInfo = Info as unknown as ReturnType<typeof vi.fn>;

const updateInfo = {
  hasUpdate: true,
  currentVersion: "0.6.0",
  latestVersion: "0.7.0",
  releaseNotes: "",
  releaseURL: "",
  publishedAt: "",
};

describe("useLatestAgentredVersion", () => {
  beforeEach(() => {
    useUpdateStore.setState(INITIAL_UPDATE_STATE);
    mockInfo.mockResolvedValue({ version: "0.6.0", commit: "9f8e7d6" });
  });

  // agentred 与桌面端同一条发布流:桌面端的更新检查已经知道最新版是哪一个,
  // 这里只读它的结果,不另发一次请求、不另开一条发布信息来源。
  it("reports the version the desktop's update check found", async () => {
    useUpdateStore.setState({ phase: { kind: "available", info: updateInfo } });

    const { result } = renderHook(() => useLatestAgentredVersion());

    await waitFor(() => expect(result.current).toBe("0.7.0"));
  });

  // 检查说「已是最新」时结果里不再带版本号,但桌面端自己就跑在一个已发布的版本上 ——
  // 这正是最常见的一台机器:桌面端是最新的,远端 agentred 落后了。
  it("falls back to the desktop's own release version when it is already up to date", async () => {
    useUpdateStore.setState({ phase: { kind: "uptodate" } });

    const { result } = renderHook(() => useLatestAgentredVersion());

    await waitFor(() => expect(result.current).toBe("0.6.0"));
  });

  // 桌面端自己也是本地构建时它的版本号不可比(决策 5 的同一道闸):
  // 没有检查结果就如实回「不知道」,界面据此不出徽标也不编版本(决策 19)。
  it("reports nothing when neither the check nor the desktop build is usable", async () => {
    mockInfo.mockResolvedValue({ version: "1.0.0", commit: "" });

    const { result } = renderHook(() => useLatestAgentredVersion());

    await waitFor(() => expect(mockInfo).toHaveBeenCalled());
    expect(result.current).toBe("");
  });

  it("survives a host without the Wails binding", async () => {
    mockInfo.mockRejectedValue(new Error("Wails binding Info not available"));

    const { result } = renderHook(() => useLatestAgentredVersion());

    await waitFor(() => expect(mockInfo).toHaveBeenCalled());
    expect(result.current).toBe("");
  });
});
