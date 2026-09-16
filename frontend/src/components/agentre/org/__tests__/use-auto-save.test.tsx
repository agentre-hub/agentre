import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useAutoSave } from "../use-auto-save";

type Form = { name: string; color: string };
const initial: Form = { name: "A", color: "red" };

describe("useAutoSave", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("immediate patch saves once with merged values", async () => {
    const save = vi.fn().mockResolvedValue(undefined);
    const { result } = renderHook(() => useAutoSave({ initial, save }));

    act(() => result.current.patch({ color: "blue" }, { immediate: true }));

    expect(save).toHaveBeenCalledTimes(1);
    expect(save).toHaveBeenCalledWith({ name: "A", color: "blue" });
    expect(result.current.values).toEqual({ name: "A", color: "blue" });
    await act(async () => {});
    expect(result.current.status).toBe("saved");
  });

  it("debounced patches coalesce into one save with the latest value", () => {
    const save = vi.fn().mockResolvedValue(undefined);
    const { result } = renderHook(() => useAutoSave({ initial, save }));

    act(() => result.current.patch({ name: "AB" }));
    act(() => result.current.patch({ name: "ABC" }));
    expect(save).not.toHaveBeenCalled();

    act(() => vi.advanceTimersByTime(600));
    expect(save).toHaveBeenCalledTimes(1);
    expect(save).toHaveBeenCalledWith({ name: "ABC", color: "red" });
  });

  it("flush runs a pending debounced save immediately", () => {
    const save = vi.fn().mockResolvedValue(undefined);
    const { result } = renderHook(() => useAutoSave({ initial, save }));

    act(() => result.current.patch({ name: "AB" }));
    act(() => result.current.flush());
    expect(save).toHaveBeenCalledTimes(1);
    expect(save).toHaveBeenCalledWith({ name: "AB", color: "red" });
  });

  it("holds save when invalid, then saves once valid again", () => {
    const save = vi.fn().mockResolvedValue(undefined);
    const { result } = renderHook(() =>
      useAutoSave({
        initial,
        save,
        isValid: (v) => v.name.trim() !== "",
      }),
    );

    act(() => result.current.patch({ name: "" }, { immediate: true }));
    expect(save).not.toHaveBeenCalled();
    expect(result.current.pendingInvalid).toBe(true);

    act(() => result.current.patch({ name: "B" }, { immediate: true }));
    expect(save).toHaveBeenCalledTimes(1);
    expect(result.current.pendingInvalid).toBe(false);
  });

  it("sets error on rejection and retry re-runs the save", async () => {
    const save = vi
      .fn()
      .mockRejectedValueOnce(new Error("boom"))
      .mockResolvedValueOnce(undefined);
    const { result } = renderHook(() => useAutoSave({ initial, save }));

    await act(async () => {
      result.current.patch({ color: "blue" }, { immediate: true });
    });
    expect(result.current.status).toBe("error");

    await act(async () => {
      result.current.retry();
    });
    expect(save).toHaveBeenCalledTimes(2);
    expect(result.current.status).toBe("saved");
  });

  it("wrap drives status and returns the fn result", async () => {
    const save = vi.fn().mockResolvedValue(undefined);
    const { result } = renderHook(() => useAutoSave({ initial, save }));

    let ret: string | null = "x";
    await act(async () => {
      ret = await result.current.wrap(async () => "moved");
    });
    expect(ret).toBe("moved");
    expect(result.current.status).toBe("saved");
  });

  // ── 不合法只挡「发出去」，不挡「记下来」。表单整体不合法（例如执行目标列表为空的
  //    Agent，后端 buildExecTargets 一定拒）时用户仍然在改名字/描述/提示词，这些编辑
  //    必须留在缓冲里，等表单变回合法立刻随最新快照一起落库。────────────────────
  describe("editing while the form as a whole is invalid", () => {
    type AgentForm = { name: string; targets: number[] };
    const agentForm: AgentForm = { name: "A", targets: [] };
    // 与 org-detail-agent 的 isValid 同形：名字非空 且 至少一个执行目标。
    const agentIsValid = (v: AgentForm) =>
      v.name.trim() !== "" && v.targets.length > 0;

    it("keeps the edit buffered and saves it as soon as the form becomes valid again", () => {
      const save = vi.fn().mockResolvedValue(undefined);
      const { result } = renderHook(() =>
        useAutoSave({ initial: agentForm, save, isValid: agentIsValid }),
      );

      act(() => result.current.patch({ name: "Renamed" }));
      expect(save).not.toHaveBeenCalled();
      expect(result.current.pendingInvalid).toBe(true);
      expect(result.current.values.name).toBe("Renamed");

      // 补上一个执行目标 → 整体合法：刚才那次改名不用重新输入，跟着一起保存。
      act(() => result.current.patch({ targets: [51] }, { immediate: true }));
      expect(save).toHaveBeenCalledTimes(1);
      expect(save).toHaveBeenCalledWith({ name: "Renamed", targets: [51] });
    });

    it("flush on blur never dispatches a save the backend would reject", () => {
      const save = vi.fn().mockResolvedValue(undefined);
      const { result } = renderHook(() =>
        useAutoSave({ initial: agentForm, save, isValid: agentIsValid }),
      );

      act(() => result.current.patch({ name: "Renamed" }));
      act(() => result.current.flush());

      expect(save).not.toHaveBeenCalled();
      expect(result.current.pendingInvalid).toBe(true);
      expect(result.current.values.name).toBe("Renamed");
    });
  });

  it("saves a still-debounced edit on unmount instead of dropping it", () => {
    const save = vi.fn().mockResolvedValue(undefined);
    const { result, unmount } = renderHook(() =>
      useAutoSave({ initial, save }),
    );

    act(() => result.current.patch({ name: "AB" }));
    expect(save).not.toHaveBeenCalled();

    // 用户在防抖窗口内点了树上的另一个节点：keyed 面板被卸载。
    unmount();
    expect(save).toHaveBeenCalledTimes(1);
    expect(save).toHaveBeenCalledWith({ name: "AB", color: "red" });
  });

  // ── 串行化：连点两次「上移」会各发一次带完整顺序的 UpdateAgent，谁后落库谁说了算。
  //    必须排队，并且只保留最新的那一份快照。────────────────────────────────────
  describe("save serialisation", () => {
    function deferredSave() {
      const settle: Array<() => void> = [];
      const save = vi
        .fn()
        .mockImplementation(
          () => new Promise<void>((resolve) => settle.push(resolve)),
        );
      return { save, settle };
    }

    it("queues a second save behind the in-flight one instead of racing it", async () => {
      const { save, settle } = deferredSave();
      const { result } = renderHook(() =>
        useAutoSave({ initial: { order: [1, 2, 3] }, save }),
      );

      act(() =>
        result.current.patch({ order: [2, 1, 3] }, { immediate: true }),
      );
      expect(save).toHaveBeenCalledTimes(1);

      // 第一次保存还在飞，用户又点了一次「上移」。
      act(() =>
        result.current.patch({ order: [2, 3, 1] }, { immediate: true }),
      );
      expect(save).toHaveBeenCalledTimes(1);

      await act(async () => settle[0]());
      expect(save).toHaveBeenCalledTimes(2);
      expect(save).toHaveBeenLastCalledWith({ order: [2, 3, 1] });

      await act(async () => settle[1]());
      expect(result.current.status).toBe("saved");
    });

    it("coalesces queued saves so only the newest snapshot is written", async () => {
      const { save, settle } = deferredSave();
      const { result } = renderHook(() =>
        useAutoSave({ initial: { order: [1, 2, 3] }, save }),
      );

      act(() =>
        result.current.patch({ order: [2, 1, 3] }, { immediate: true }),
      );
      act(() =>
        result.current.patch({ order: [2, 3, 1] }, { immediate: true }),
      );
      act(() =>
        result.current.patch({ order: [3, 2, 1] }, { immediate: true }),
      );

      await act(async () => settle[0]());
      expect(save).toHaveBeenCalledTimes(2);
      expect(save).toHaveBeenLastCalledWith({ order: [3, 2, 1] });
    });
  });

  it("a slow failing action does not repaint the status of a newer successful save", async () => {
    const save = vi.fn().mockResolvedValue(undefined);
    const { result } = renderHook(() => useAutoSave({ initial, save }));

    // 头像上传（wrap）很慢，最后失败；中途的字段编辑已经保存成功。
    let failUpload: (e: unknown) => void = () => {};
    const uploading = new Promise<void>((_, reject) => {
      failUpload = reject;
    });
    let uploadDone: Promise<unknown> = Promise.resolve();
    act(() => {
      uploadDone = result.current.wrap(() => uploading);
    });

    await act(async () => {
      result.current.patch({ name: "AB" }, { immediate: true });
    });
    expect(result.current.status).toBe("saved");

    await act(async () => {
      failUpload(new Error("upload failed"));
      await uploadDone;
    });
    expect(result.current.status).toBe("saved");
  });

  it("retry re-runs the save that failed, not the action dispatched after it", async () => {
    const save = vi
      .fn()
      .mockRejectedValueOnce(new Error("boom"))
      .mockResolvedValue(undefined);
    const { result } = renderHook(() => useAutoSave({ initial, save }));

    await act(async () => {
      result.current.patch({ name: "AB" }, { immediate: true });
    });
    expect(result.current.status).toBe("error");

    // 保存失败之后又成功传了一张头像：它不该顶替「待重试」的那次保存。
    const upload = vi.fn().mockResolvedValue("ok");
    await act(async () => {
      await result.current.wrap(upload);
    });

    await act(async () => {
      result.current.retry();
    });
    expect(save).toHaveBeenCalledTimes(2);
    expect(save).toHaveBeenLastCalledWith({ name: "AB", color: "red" });
    expect(upload).toHaveBeenCalledTimes(1);
  });
});
