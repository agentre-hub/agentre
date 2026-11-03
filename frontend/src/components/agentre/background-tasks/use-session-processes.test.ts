import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const appMocks = vi.hoisted(() => ({ ListSessionProcesses: vi.fn() }));
vi.mock("../../../../wailsjs/go/app/App", () => appMocks);

import { useSessionProcesses } from "./use-session-processes";

const view = (pid: number) => ({
  pid,
  command: `cmd-${pid}`,
  ports: [3000],
  startedAt: 1_700_000_000_000,
  cwd: "/w",
});

async function flush() {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("useSessionProcesses", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    appMocks.ListSessionProcesses.mockReset();
    appMocks.ListSessionProcesses.mockResolvedValue({ processes: [view(1)] });
  });
  afterEach(() => vi.useRealTimers());

  it("Given active, When mounted, Then lists immediately and maps rows", async () => {
    const { result } = renderHook(() => useSessionProcesses(7, true));
    await flush();
    expect(appMocks.ListSessionProcesses).toHaveBeenCalledWith(7);
    expect(result.current.processes).toEqual([
      {
        pid: 1,
        command: "cmd-1",
        ports: [3000],
        startedAt: 1_700_000_000_000,
        cwd: "/w",
      },
    ]);
  });

  it("Given active, When 5s elapse, Then it polls again", async () => {
    renderHook(() => useSessionProcesses(7, true));
    await flush();
    expect(appMocks.ListSessionProcesses).toHaveBeenCalledTimes(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(appMocks.ListSessionProcesses).toHaveBeenCalledTimes(2);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(appMocks.ListSessionProcesses).toHaveBeenCalledTimes(3);
  });

  it("Given inactive, Then it never lists, and stops once deactivated", async () => {
    const { rerender } = renderHook(
      ({ active }) => useSessionProcesses(7, active),
      { initialProps: { active: false } },
    );
    await act(async () => {
      await vi.advanceTimersByTimeAsync(12000);
    });
    expect(appMocks.ListSessionProcesses).not.toHaveBeenCalled();
    rerender({ active: true });
    await flush();
    expect(appMocks.ListSessionProcesses).toHaveBeenCalledTimes(1);
    rerender({ active: false });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(12000);
    });
    expect(appMocks.ListSessionProcesses).toHaveBeenCalledTimes(1);
  });

  it("Given no session id, Then it does not list", async () => {
    renderHook(() => useSessionProcesses(undefined, true));
    await flush();
    expect(appMocks.ListSessionProcesses).not.toHaveBeenCalled();
  });

  it("Given the call fails, Then the list is empty and nothing throws", async () => {
    appMocks.ListSessionProcesses.mockRejectedValue(new Error("boom"));
    const { result } = renderHook(() => useSessionProcesses(7, true));
    await flush();
    expect(result.current.processes).toEqual([]);
  });

  it("Given a later failure, Then the list is emptied", async () => {
    const { result } = renderHook(() => useSessionProcesses(7, true));
    await flush();
    expect(result.current.processes).toHaveLength(1);
    appMocks.ListSessionProcesses.mockRejectedValue(new Error("boom"));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(result.current.processes).toEqual([]);
  });

  it("Given a slow response for the old session, When session switches, Then it is ignored", async () => {
    let resolveOld: (v: unknown) => void = () => undefined;
    appMocks.ListSessionProcesses.mockImplementation((id: number) =>
      id === 1
        ? new Promise((r) => {
            resolveOld = r;
          })
        : Promise.resolve({ processes: [view(2)] }),
    );
    const { result, rerender } = renderHook(
      ({ id }) => useSessionProcesses(id, true),
      { initialProps: { id: 1 } },
    );
    rerender({ id: 2 });
    await flush();
    expect(result.current.processes.map((p) => p.pid)).toEqual([2]);
    await act(async () => {
      resolveOld({ processes: [view(1)] });
    });
    expect(result.current.processes.map((p) => p.pid)).toEqual([2]);
  });

  it("Given rows for the old session, When session switches and the new list is slow, Then the old rows are dropped at once", async () => {
    appMocks.ListSessionProcesses.mockImplementation((id: number) =>
      id === 1
        ? Promise.resolve({ processes: [view(1)] })
        : new Promise(() => undefined),
    );
    const { result, rerender } = renderHook(
      ({ id }) => useSessionProcesses(id, true),
      { initialProps: { id: 1 } },
    );
    await flush();
    expect(result.current.processes.map((p) => p.pid)).toEqual([1]);
    rerender({ id: 2 });
    await flush();
    // 旧会话的行若还挂着,点「结束 / 打开」会带着新会话 id 去操作旧会话的进程。
    expect(result.current.processes).toEqual([]);
  });

  it("Given refresh(), Then it lists right away", async () => {
    const { result } = renderHook(() => useSessionProcesses(7, true));
    await flush();
    await act(async () => {
      await result.current.refresh();
    });
    expect(appMocks.ListSessionProcesses).toHaveBeenCalledTimes(2);
  });
});
