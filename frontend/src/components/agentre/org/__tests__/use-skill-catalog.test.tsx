import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { useSkillCatalog } from "../use-skill-catalog";

function stubBinding(packs: unknown[]) {
  const fn = vi.fn().mockResolvedValue({ packs });
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  (window as any).go = { app: { App: { ListAgentSkillPacks: fn } } };
  return fn;
}

afterEach(() => {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  delete (window as any).go;
});

describe("useSkillCatalog", () => {
  it("does not fetch until load() is called", () => {
    const fn = stubBinding([]);
    const { result } = renderHook(() => useSkillCatalog(7, 51));
    expect(fn).not.toHaveBeenCalled();
    expect(result.current.items).toEqual([]);
    expect(result.current.fetched).toBe(false);
  });

  it("loads and maps packs on load(false), scoped to the given exec target's backend", async () => {
    const fn = stubBinding([
      {
        id: "sp@m",
        name: "superpowers",
        description: "d",
        skills: ["a"],
        source: "installed",
        recommended: false,
        installed: true,
        enabled: true,
      },
    ]);
    const { result } = renderHook(() => useSkillCatalog(7, 51));
    await act(async () => {
      await result.current.load(false);
    });
    expect(fn).toHaveBeenCalledWith(7, 51, false);
    await waitFor(() => expect(result.current.items).toHaveLength(1));
    expect(result.current.items[0].name).toBe("superpowers");
    expect(result.current.fetched).toBe(true);
  });

  it("rescan calls the binding with refresh=true", async () => {
    const fn = stubBinding([]);
    const { result } = renderHook(() => useSkillCatalog(7, 51));
    await act(async () => {
      await result.current.load(true);
    });
    expect(fn).toHaveBeenCalledWith(7, 51, true);
  });

  it("captures errors classified: raw machine text goes to detail, never to the message", async () => {
    const fn = vi.fn().mockRejectedValue(new Error("boom"));
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (window as any).go = { app: { App: { ListAgentSkillPacks: fn } } };
    const { result } = renderHook(() => useSkillCatalog(7, 51));
    let returned: unknown;
    await act(async () => {
      returned = await result.current.load(false);
    });
    await waitFor(() =>
      expect(result.current.error).toEqual({
        kind: "unknown",
        message: "",
        detail: "boom",
      }),
    );
    // load 把这一次的失败交回调用方，好让弹层里的触发点就地报。
    expect(returned).toEqual(result.current.error);
    expect(result.current.fetched).toBe(false);
  });

  it("clears the error when a later load succeeds", async () => {
    const fn = vi
      .fn()
      .mockRejectedValueOnce(new Error("boom"))
      .mockResolvedValue({ packs: [] });
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (window as any).go = { app: { App: { ListAgentSkillPacks: fn } } };
    const { result } = renderHook(() => useSkillCatalog(7, 51));
    await act(async () => {
      await result.current.load(false);
    });
    expect(result.current.error).not.toBeNull();
    let returned: unknown;
    await act(async () => {
      returned = await result.current.load(false);
    });
    expect(result.current.error).toBeNull();
    expect(returned).toBeNull();
  });

  it("auto-loads once on mount when autoLoad is true", async () => {
    const fn = stubBinding([]);
    renderHook(() => useSkillCatalog(7, 51, true));
    await waitFor(() => expect(fn).toHaveBeenCalledTimes(1));
    expect(fn).toHaveBeenCalledWith(7, 51, false);
  });

  it("re-fetches when the exec target's backend id changes (different block, same agent)", async () => {
    const fn = stubBinding([]);
    const { rerender } = renderHook(
      ({ backendId }) => useSkillCatalog(7, backendId, true),
      { initialProps: { backendId: 51 } },
    );
    await waitFor(() => expect(fn).toHaveBeenCalledTimes(1));
    rerender({ backendId: 52 });
    await waitFor(() => expect(fn).toHaveBeenCalledTimes(2));
    expect(fn).toHaveBeenLastCalledWith(7, 52, false);
  });
});
