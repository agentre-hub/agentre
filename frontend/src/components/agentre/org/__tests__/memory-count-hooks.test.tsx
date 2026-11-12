import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { MemoryOwnerRef } from "@agentre-hub/agentre-ui";

const appMocks = vi.hoisted(() => ({
  MemoryCountsByOwner: vi.fn(),
  MemoryCountOwned: vi.fn(),
}));
vi.mock("../../../../../wailsjs/go/app/App", () => appMocks);

import { useMemoryDeleteCount } from "../use-memory-delete-count";
import { useMemoryOwnerEntry } from "../use-memory-owner-entry";

const inRouter = ({ children }: { children: ReactNode }) => (
  <MemoryRouter>{children}</MemoryRouter>
);

function deferred<T>() {
  let resolve: (v: T) => void = () => {};
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("useMemoryOwnerEntry", () => {
  it("Given no project yet (id 0, the closed settings drawer), Then it reads nothing and draws no row", async () => {
    const { result } = renderHook(
      () => useMemoryOwnerEntry({ kind: "project", id: 0 }),
      { wrapper: inRouter },
    );
    await act(async () => {});
    expect(appMocks.MemoryCountsByOwner).not.toHaveBeenCalled();
    expect(result.current).toBeNull();
  });

  it("When the owner changes, Then the previous owner's count is not shown while the new one loads", async () => {
    appMocks.MemoryCountsByOwner.mockResolvedValueOnce([
      { owner: { kind: "project", id: 4 }, count: 5 },
    ]);
    const { result, rerender } = renderHook(
      ({ owner }: { owner: MemoryOwnerRef }) => useMemoryOwnerEntry(owner),
      {
        wrapper: inRouter,
        initialProps: { owner: { kind: "project", id: 4 } },
      },
    );
    await waitFor(() => expect(result.current?.count).toBe(5));

    const next = deferred<unknown>();
    appMocks.MemoryCountsByOwner.mockReturnValueOnce(next.promise);
    rerender({ owner: { kind: "project", id: 8 } });
    expect(result.current).toBeNull();

    await act(async () =>
      next.resolve([{ owner: { kind: "project", id: 8 }, count: 2 }]),
    );
    expect(result.current?.count).toBe(2);
  });
});

describe("useMemoryDeleteCount", () => {
  it("When the owners change (another delete strategy), Then the old count is withdrawn until the new one arrives", async () => {
    appMocks.MemoryCountOwned.mockResolvedValueOnce(2);
    const { result, rerender } = renderHook(
      ({ owners }: { owners: MemoryOwnerRef[] }) =>
        useMemoryDeleteCount(owners, true),
      { initialProps: { owners: [{ kind: "department", id: 1 }] } },
    );
    await waitFor(() => expect(result.current).toBe(2));

    const next = deferred<number>();
    appMocks.MemoryCountOwned.mockReturnValueOnce(next.promise);
    rerender({
      owners: [
        { kind: "department", id: 1 },
        { kind: "agent", id: 3 },
      ],
    });
    expect(result.current).toBeNull();

    await act(async () => next.resolve(9));
    expect(result.current).toBe(9);
  });
});
