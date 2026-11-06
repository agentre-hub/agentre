import type * as React from "react";
import { act, render, renderHook, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const appMocks = vi.hoisted(() => ({ ProjectReorder: vi.fn() }));
vi.mock("../../../../../wailsjs/go/app/App", () => appMocks);

const sonnerMocks = vi.hoisted(() => {
  const toast = Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn() });
  return { toast };
});
vi.mock("sonner", () => sonnerMocks);

import type { app } from "../../../../../wailsjs/go/models";
import { useProjectReorder } from "../use-project-reorder";

type ToastOptions = {
  description?: React.ReactNode;
  action?: { label: string; onClick: () => void };
};

const projects = new Map<number, app.ProjectItem>(
  [1, 2, 3].map((id) => [
    id,
    { id, parentID: 0, name: `p${id}` } as app.ProjectItem,
  ]),
);

function setup() {
  const refreshProjectData = vi.fn();
  const hook = renderHook(() =>
    useProjectReorder({
      axis: "project",
      filtering: false,
      projectByID: projects,
      refreshProjectData,
    }),
  );
  return { hook, refreshProjectData };
}

/** 把 p3 拖到 p1 的位置。 */
async function dropThirdOnFirst(hook: ReturnType<typeof setup>["hook"]) {
  await act(() =>
    hook.result.current.handleDragEnd({
      active: { id: "project-3" },
      over: { id: "project-1" },
    } as never),
  );
}

function lastErrorToast(): { title: unknown; options: ToastOptions } {
  const calls = sonnerMocks.toast.error.mock.calls;
  const [title, options] = calls[calls.length - 1] ?? [];
  return { title, options: (options ?? {}) as ToastOptions };
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("useProjectReorder save failure (S5)", () => {
  it("Given the save is rejected, When a project is dropped, Then a toast says the order was restored and offers Retry instead of a sidebar error bar", async () => {
    appMocks.ProjectReorder.mockRejectedValue(
      "rpc error: code = Unavailable desc = connection refused",
    );
    const { hook, refreshProjectData } = setup();

    await dropThirdOnFirst(hook);

    const { title, options } = lastErrorToast();
    expect(title).toBe(
      "Project order wasn't saved — the original order is back",
    );
    expect(options.action?.label).toBe("Retry");
    expect(refreshProjectData).not.toHaveBeenCalled();
    expect(hook.result.current).not.toHaveProperty("reorderError");
  });

  it("Given an uncoded raw error, When the toast is shown, Then the raw text sits only inside a collapsed Details disclosure", async () => {
    appMocks.ProjectReorder.mockRejectedValue(
      new Error("rpc error: code = Unavailable desc = connection refused"),
    );
    const { hook } = setup();

    await dropThirdOnFirst(hook);

    const { title, options } = lastErrorToast();
    expect(String(title)).not.toContain("rpc error");
    render(<>{options.description}</>);
    const details = screen.getByText(/connection refused/).closest("details");
    expect(details).not.toBeNull();
    expect(details).not.toHaveAttribute("open");
    expect(screen.getByText("Details").tagName).toBe("SUMMARY");
  });

  it("Given a coded backend error, When the toast is shown, Then the backend sentence is the description without the code prefix", async () => {
    appMocks.ProjectReorder.mockRejectedValue(
      new Error("agentre-code:18000 Project not found"),
    );
    const { hook } = setup();

    await dropThirdOnFirst(hook);

    render(<>{lastErrorToast().options.description}</>);
    expect(screen.getByText("Project not found")).toBeInTheDocument();
    expect(screen.queryByText(/agentre-code/)).toBeNull();
    expect(document.querySelector("details")).toBeNull();
  });

  it("Given a failed save, When Retry is clicked and the save then succeeds, Then the same order is sent again and the tree is refreshed", async () => {
    appMocks.ProjectReorder.mockRejectedValueOnce(new Error("boom"));
    const { hook, refreshProjectData } = setup();
    await dropThirdOnFirst(hook);
    const firstPayload = appMocks.ProjectReorder.mock.calls[0]?.[0];
    expect(firstPayload).toEqual({ parentID: 0, orderedIDs: [3, 1, 2] });

    appMocks.ProjectReorder.mockResolvedValueOnce(undefined);
    await act(async () => {
      lastErrorToast().options.action?.onClick();
    });

    expect(appMocks.ProjectReorder).toHaveBeenCalledTimes(2);
    expect(appMocks.ProjectReorder.mock.calls[1]?.[0]).toEqual(firstPayload);
    expect(refreshProjectData).toHaveBeenCalledTimes(1);
  });

  it("Given Retry fails again, When clicked, Then another failure toast with Retry is shown", async () => {
    appMocks.ProjectReorder.mockRejectedValue(new Error("boom"));
    const { hook } = setup();
    await dropThirdOnFirst(hook);

    await act(async () => {
      lastErrorToast().options.action?.onClick();
    });

    expect(sonnerMocks.toast.error).toHaveBeenCalledTimes(2);
    expect(lastErrorToast().options.action?.label).toBe("Retry");
  });
});
