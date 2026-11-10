import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// config:changed / sync:applied 是「本机或另一台设备写完了这五类资源」的两条事件
// （docs/specs/2026-09-22-agrctl-resource-management.md「Real-time refresh」）；组织页
// 要在它们到达时就地重拉，不显示任何提示条或 loading 横幅。
const runtimeHandlers = new Map<string, Set<(payload: unknown) => void>>();
vi.mock("../../../../../wailsjs/runtime/runtime", () => ({
  EventsOn: (name: string, cb: (payload: unknown) => void) => {
    const set =
      runtimeHandlers.get(name) ?? new Set<(payload: unknown) => void>();
    set.add(cb);
    runtimeHandlers.set(name, set);
    return () => set.delete(cb);
  },
}));

// 测试用的 App 替身（vite.config 的别名）没有两个重排绑定；补上并照样转给
// window.go，下面的 installAppMock 才接得住。
vi.mock("../../../../../wailsjs/go/app/App", async (importOriginal) => {
  const callApp =
    (name: string) =>
    (...args: unknown[]) =>
      (
        window as unknown as {
          go: { app: { App: Record<string, (...a: unknown[]) => unknown> } };
        }
      ).go.app.App[name](...args);
  return {
    ...(await importOriginal<Record<string, unknown>>()),
    ReorderAgents: callApp("ReorderAgents"),
    ReorderDepartments: callApp("ReorderDepartments"),
  };
});

function emitRuntimeEvent(name: string) {
  for (const cb of [...(runtimeHandlers.get(name) ?? [])]) cb(undefined);
}

import { useOrgData } from "../use-org-data";

// App.js delegates to window.go.app.App at runtime, so we patch that directly.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
(globalThis as any).window = globalThis;

type AnyFn = (...args: unknown[]) => unknown;

type AppMockShape = {
  LoadOrg: AnyFn;
  ListAgentBackends: AnyFn;
  CreateDepartment: AnyFn;
  UpdateDepartment: AnyFn;
  MoveDepartment: AnyFn;
  DeleteDepartment: AnyFn;
  CreateAgent: AnyFn;
  UpdateAgent: AnyFn;
  MoveAgent: AnyFn;
  DeleteAgent: AnyFn;
  ReorderAgents: AnyFn;
  ReorderDepartments: AnyFn;
};

let appMock: AppMockShape;

function installAppMock(overrides: Partial<AppMockShape> = {}) {
  const base: AppMockShape = {
    LoadOrg: vi.fn(() => Promise.resolve({ departments: [], agents: [] })),
    ListAgentBackends: vi.fn(() => Promise.resolve({ items: [] })),
    CreateDepartment: vi.fn(() => Promise.resolve({ item: {} })),
    UpdateDepartment: vi.fn(() => Promise.resolve({ item: {} })),
    MoveDepartment: vi.fn(() => Promise.resolve({ item: {} })),
    DeleteDepartment: vi.fn(() => Promise.resolve({})),
    CreateAgent: vi.fn(() => Promise.resolve({ item: {} })),
    UpdateAgent: vi.fn(() => Promise.resolve({ item: {} })),
    MoveAgent: vi.fn(() => Promise.resolve({ item: {} })),
    DeleteAgent: vi.fn(() => Promise.resolve({})),
    ReorderAgents: vi.fn(() => Promise.resolve()),
    ReorderDepartments: vi.fn(() => Promise.resolve()),
  };
  appMock = { ...base, ...overrides };
  Object.defineProperty(window, "go", {
    configurable: true,
    value: { app: { App: appMock } },
  });
  return appMock;
}

beforeEach(() => {
  installAppMock();
});

afterEach(() => {
  Reflect.deleteProperty(window, "go");
  vi.resetAllMocks();
});

describe("useOrgData", () => {
  it("loads org tree on mount", async () => {
    installAppMock({
      LoadOrg: vi.fn(() =>
        Promise.resolve({
          departments: [{ id: 1, name: "x", parentId: 0 } as never],
          agents: [{ id: 1, name: "CEO" } as never],
        }),
      ),
    });

    const { result } = renderHook(() => useOrgData());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.departments).toHaveLength(1);
    expect(result.current.agents).toHaveLength(1);
  });

  it("captures error and stops loading", async () => {
    installAppMock({
      LoadOrg: vi.fn(() => Promise.reject(new Error("boom"))),
    });

    const { result } = renderHook(() => useOrgData());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.error).toBe("boom");
  });

  it("reloads after a mutation", async () => {
    installAppMock({
      LoadOrg: vi.fn(() => Promise.resolve({ departments: [], agents: [] })),
      MoveAgent: vi.fn(() => Promise.resolve({ item: { id: 1 } as never })),
    });

    const { result } = renderHook(() => useOrgData());
    await waitFor(() => expect(result.current.loading).toBe(false));

    await act(async () => {
      await result.current.moveAgent({
        id: 1,
        newDepartmentId: 2,
        newParentAgentId: 0,
        newSortOrder: 0,
      });
    });

    expect(vi.mocked(appMock.LoadOrg)).toHaveBeenCalledTimes(2);
  });

  it.each(["config:changed", "sync:applied"])(
    "reloads silently (no loading flicker) when a %s event arrives",
    async (eventName) => {
      runtimeHandlers.clear();
      installAppMock({
        LoadOrg: vi.fn(() => Promise.resolve({ departments: [], agents: [] })),
      });

      const { result } = renderHook(() => useOrgData());
      await waitFor(() => expect(result.current.loading).toBe(false));
      vi.mocked(appMock.LoadOrg).mockClear();

      act(() => {
        emitRuntimeEvent(eventName);
      });

      await waitFor(() =>
        expect(vi.mocked(appMock.LoadOrg)).toHaveBeenCalledTimes(1),
      );
      // 就地刷新：不回退到 loading 占位。
      expect(result.current.loading).toBe(false);
    },
  );

  it("Given a mutation is still in flight, When config:changed arrives, Then it waits for the mutation's own reload", async () => {
    runtimeHandlers.clear();
    let finishMove: () => void = () => {};
    installAppMock({
      LoadOrg: vi.fn(() => Promise.resolve({ departments: [], agents: [] })),
      MoveAgent: vi.fn(
        () => new Promise((r) => (finishMove = () => r({ item: {} }))),
      ),
    });
    const { result } = renderHook(() => useOrgData());
    await waitFor(() => expect(result.current.loading).toBe(false));
    vi.mocked(appMock.LoadOrg).mockClear();

    let moving: Promise<unknown> = Promise.resolve();
    act(() => {
      moving = result.current.moveAgent({ id: 1 } as never);
    });
    // 本次写入自己发的 config:changed 先到：此刻重拉会拉回不含后续写入的旧数据。
    act(() => {
      emitRuntimeEvent("config:changed");
    });
    expect(vi.mocked(appMock.LoadOrg)).not.toHaveBeenCalled();

    await act(async () => {
      finishMove();
      await moving;
    });
    await waitFor(() =>
      expect(vi.mocked(appMock.LoadOrg)).toHaveBeenCalledTimes(1),
    );
  });

  it("Given the hook unmounts, When it goes away, Then its config:changed/sync:applied subscriptions go with it", () => {
    runtimeHandlers.clear();
    installAppMock({
      LoadOrg: vi.fn(() => Promise.resolve({ departments: [], agents: [] })),
    });

    const { unmount } = renderHook(() => useOrgData());
    unmount();

    expect(runtimeHandlers.get("config:changed")?.size ?? 0).toBe(0);
    expect(runtimeHandlers.get("sync:applied")?.size ?? 0).toBe(0);
  });
});

// B1：写失败不再把整页换成一句原话 —— 失败抛回调用方（弹窗 / 自动保存 / 拖拽各自
// 决定怎么说），页面内容原样留着；只有「从没加载出来过」才是整页的失败态。
describe("useOrgData 写失败与加载失败", () => {
  const EVA = { id: 2, name: "Eva", departmentId: 1, sortOrder: 1 };
  const DAN = { id: 5, name: "Dan", departmentId: 1, sortOrder: 2 };
  const ENGINEERING = { id: 1, name: "工程部", parentId: 0, sortOrder: 1 };
  const PRODUCT = { id: 3, name: "产品部", parentId: 0, sortOrder: 2 };
  const loaded = () =>
    Promise.resolve({
      departments: [ENGINEERING, PRODUCT],
      agents: [EVA, DAN],
    });

  it("Given a loaded page, When a write fails, Then the rejection reaches the caller and the page keeps its content", async () => {
    installAppMock({
      LoadOrg: vi.fn(loaded),
      CreateAgent: vi.fn(() =>
        Promise.reject(new Error("agentre-code:14001 已经有同名 Agent")),
      ),
    });
    const { result } = renderHook(() => useOrgData());
    await waitFor(() => expect(result.current.loading).toBe(false));

    let failure: unknown;
    await act(async () => {
      await result.current
        .createAgent({ name: "Eva" } as never)
        .catch((e: unknown) => {
          failure = e;
        });
    });

    expect(failure).toEqual(new Error("agentre-code:14001 已经有同名 Agent"));
    expect(result.current.error).toBeNull();
    expect(result.current.agents.map((a) => a.id)).toEqual([2, 5]);
    // 失败之后仍然重拉一次：乐观更新要以库里的为准。
    await waitFor(() =>
      expect(vi.mocked(appMock.LoadOrg)).toHaveBeenCalledTimes(2),
    );
  });

  it("Given an optimistic agent reorder, When the write fails and the follow-up reload fails too, Then the list is back in its original order", async () => {
    installAppMock({
      LoadOrg: vi
        .fn()
        .mockImplementationOnce(loaded)
        .mockImplementation(() => Promise.reject(new Error("db locked"))),
      ReorderAgents: vi.fn(() => Promise.reject(new Error("boom"))),
    });
    const { result } = renderHook(() => useOrgData());
    await waitFor(() => expect(result.current.loading).toBe(false));

    let failure: unknown;
    await act(async () => {
      await result.current.reorderAgents(1, 0, [5, 2]).catch((e: unknown) => {
        failure = e;
      });
    });

    expect(failure).toEqual(new Error("boom"));
    const order = [...result.current.agents]
      .sort((a, b) => a.sortOrder - b.sortOrder)
      .map((a) => a.id);
    expect(order).toEqual([2, 5]);
    expect(result.current.error).toBeNull();
  });

  it("Given an optimistic department reorder, When the write fails, Then the departments are back in their original order", async () => {
    installAppMock({
      LoadOrg: vi
        .fn()
        .mockImplementationOnce(loaded)
        .mockImplementation(() => Promise.reject(new Error("db locked"))),
      ReorderDepartments: vi.fn(() => Promise.reject(new Error("boom"))),
    });
    const { result } = renderHook(() => useOrgData());
    await waitFor(() => expect(result.current.loading).toBe(false));

    let failure: unknown;
    await act(async () => {
      await result.current.reorderDepartments(0, [3, 1]).catch((e: unknown) => {
        failure = e;
      });
    });

    expect(failure).toEqual(new Error("boom"));
    const order = [...result.current.departments]
      .sort((a, b) => a.sortOrder - b.sortOrder)
      .map((d) => d.id);
    expect(order).toEqual([1, 3]);
  });

  it("Given the page already loaded, When a background reload fails, Then the content stays and no page-level error appears", async () => {
    runtimeHandlers.clear();
    installAppMock({
      LoadOrg: vi
        .fn()
        .mockImplementationOnce(loaded)
        .mockImplementation(() => Promise.reject(new Error("db locked"))),
    });
    const { result } = renderHook(() => useOrgData());
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => {
      emitRuntimeEvent("config:changed");
    });
    await waitFor(() =>
      expect(vi.mocked(appMock.LoadOrg)).toHaveBeenCalledTimes(2),
    );
    await act(async () => {});

    expect(result.current.error).toBeNull();
    expect(result.current.agents).toHaveLength(2);
  });

  it("Given the first load failed, When retried and the backend answers, Then the error clears and the content shows", async () => {
    installAppMock({
      LoadOrg: vi
        .fn()
        .mockImplementationOnce(() => Promise.reject(new Error("boom")))
        .mockImplementation(loaded),
    });
    const { result } = renderHook(() => useOrgData());
    await waitFor(() => expect(result.current.error).toBe("boom"));

    act(() => {
      result.current.retryLoad();
    });
    // 重试期间回到加载占位，而不是一直挂着失败态。
    expect(result.current.loading).toBe(true);

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.error).toBeNull();
    expect(result.current.agents).toHaveLength(2);
  });
});
