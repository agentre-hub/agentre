import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { mergeCandidates, ProjectMergeDialog } from "../project-merge-dialog";

type Preview = {
  keep: { id: number; name: string; parentPath: string[]; claimed: boolean };
  drop: { id: number; name: string; parentPath: string[]; claimed: boolean };
};

// 后端的判据（chooseMergeWinner）与用户选的方向无关：这里故意让「源」留下来，
// 对话框必须照后端说的显示，不能按「合并到目标」去猜。
const sourceKeptPreview: Preview = {
  keep: {
    id: 1,
    name: "agentre-hub (local)",
    parentPath: [],
    claimed: false,
  },
  drop: {
    id: 2,
    name: "agentre-hub",
    parentPath: ["org"],
    claimed: false,
  },
};

function stubApp({
  merge = () => Promise.resolve({ id: 1 }),
  preview = () => Promise.resolve(sourceKeptPreview),
}: {
  merge?: (...args: unknown[]) => unknown;
  preview?: (...args: unknown[]) => unknown;
} = {}) {
  const ProjectMerge = vi.fn(merge);
  const ProjectMergePreview = vi.fn(preview);
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  (window as any).go = { app: { App: { ProjectMerge, ProjectMergePreview } } };
  return { ProjectMerge, ProjectMergePreview };
}

afterEach(() => {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  delete (window as any).go;
});

async function pick(optionName: string) {
  await userEvent.click(screen.getByRole("combobox", { name: "Merge with" }));
  await userEvent.click(
    await screen.findByRole("option", { name: optionName }),
  );
}

describe("ProjectMergeDialog", () => {
  it("closed when source is null", () => {
    render(
      <ProjectMergeDialog
        source={null}
        candidates={[{ id: 2, name: "agentre-server" }]}
        onClose={vi.fn()}
        onMerged={vi.fn()}
      />,
    );
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("no candidates: shows the empty hint and disables confirm", () => {
    render(
      <ProjectMergeDialog
        source={{ id: 1, name: "agentre-hub (local)" }}
        candidates={[]}
        onClose={vi.fn()}
        onMerged={vi.fn()}
      />,
    );
    expect(
      screen.getByText("No other project to merge into"),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Merge" })).toBeDisabled();
  });

  it("candidates read as parent / child paths and the input filters them", async () => {
    stubApp();
    render(
      <ProjectMergeDialog
        source={{ id: 1, name: "api" }}
        candidates={[
          { id: 2, name: "api", parentPath: ["agentre-server"] },
          { id: 3, name: "api", parentPath: ["agentre"] },
          { id: 4, name: "katch", parentPath: [] },
        ]}
        onClose={vi.fn()}
        onMerged={vi.fn()}
      />,
    );

    await userEvent.click(screen.getByRole("combobox", { name: "Merge with" }));
    const list = await screen.findByRole("listbox");
    // 两个同名的 api 靠父路径分开。
    expect(
      within(list)
        .getAllByRole("option")
        .map((o) => o.textContent),
    ).toEqual(["agentre / api", "agentre-server / api", "katch"]);

    await userEvent.type(
      screen.getByRole("searchbox", { name: "Filter projects…" }),
      "SERVER",
    );
    expect(
      within(list)
        .getAllByRole("option")
        .map((o) => o.textContent),
    ).toEqual(["agentre-server / api"]);

    await userEvent.clear(
      screen.getByRole("searchbox", { name: "Filter projects…" }),
    );
    await userEvent.type(
      screen.getByRole("searchbox", { name: "Filter projects…" }),
      "nothing-like-this",
    );
    expect(within(list).queryAllByRole("option")).toHaveLength(0);
    expect(screen.getByText("No matching project")).toBeInTheDocument();
  });

  it("after a target is chosen: shows the backend's keep / drop and confirms as the kept name", async () => {
    const { ProjectMerge, ProjectMergePreview } = stubApp();
    const onMerged = vi.fn();
    render(
      <ProjectMergeDialog
        source={{ id: 1, name: "agentre-hub (local)" }}
        candidates={[{ id: 2, name: "agentre-hub", parentPath: ["org"] }]}
        onClose={vi.fn()}
        onMerged={onMerged}
      />,
    );

    await pick("org / agentre-hub");

    await waitFor(() => expect(ProjectMergePreview).toHaveBeenCalledWith(1, 2));
    const keep = await screen.findByTestId("project-merge-keep");
    expect(keep).toHaveTextContent(
      "Kept after merge: agentre-hub (local) (top level · this device only)",
    );
    expect(screen.getByTestId("project-merge-drop")).toHaveTextContent(
      "Gone after merge: agentre-hub (under org · this device only)",
    );

    const confirm = screen.getByRole("button", {
      name: "Merge as agentre-hub (local)",
    });
    expect(confirm).toBeEnabled();
    await userEvent.click(confirm);

    await waitFor(() =>
      expect(ProjectMerge).toHaveBeenCalledWith({ sourceID: 1, targetID: 2 }),
    );
    await waitFor(() => expect(onMerged).toHaveBeenCalledWith(1));
  });

  it("an account-side keep is labelled as the account project", async () => {
    stubApp({
      preview: () =>
        Promise.resolve({
          keep: {
            id: 2,
            name: "api",
            parentPath: ["agentre-server"],
            claimed: true,
          },
          drop: { id: 1, name: "api", parentPath: ["agentre"], claimed: false },
        }),
    });
    render(
      <ProjectMergeDialog
        source={{ id: 1, name: "api" }}
        candidates={[{ id: 2, name: "api", parentPath: ["agentre-server"] }]}
        onClose={vi.fn()}
        onMerged={vi.fn()}
      />,
    );

    await pick("agentre-server / api");

    expect(await screen.findByTestId("project-merge-keep")).toHaveTextContent(
      "Kept after merge: api (under agentre-server · account project)",
    );
    expect(screen.getByRole("button", { name: "Merge as api" })).toBeEnabled();
  });

  it("preview failure: no raw error text, and merging stays unavailable", async () => {
    const { ProjectMerge } = stubApp({
      preview: () => Promise.reject(new Error("rpc boom")),
    });
    render(
      <ProjectMergeDialog
        source={{ id: 1, name: "agentre-hub (local)" }}
        candidates={[{ id: 2, name: "agentre-hub" }]}
        onClose={vi.fn()}
        onMerged={vi.fn()}
      />,
    );

    await pick("agentre-hub");

    expect(
      await screen.findByText(/Couldn't work out the merge result/),
    ).toBeInTheDocument();
    expect(screen.queryByText(/rpc boom/)).toBeNull();
    expect(screen.getByRole("button", { name: "Merge" })).toBeDisabled();
    expect(ProjectMerge).not.toHaveBeenCalled();
  });

  it("note bullets use the neutral colour, not a status colour", () => {
    render(
      <ProjectMergeDialog
        source={{ id: 1, name: "agentre-hub (local)" }}
        candidates={[{ id: 2, name: "agentre-hub" }]}
        onClose={vi.fn()}
        onMerged={vi.fn()}
      />,
    );
    const dialog = screen.getByRole("dialog");
    expect(dialog.querySelector('[class*="bg-status-"]')).toBeNull();
    expect(
      dialog.querySelectorAll(".bg-decorative-foreground").length,
    ).toBeGreaterThan(0);
  });

  it("merge failure shows the error and does not call onMerged", async () => {
    stubApp({ merge: () => Promise.reject(new Error("boom")) });
    const onMerged = vi.fn();
    render(
      <ProjectMergeDialog
        source={{ id: 1, name: "agentre-hub (local)" }}
        candidates={[{ id: 2, name: "agentre-hub" }]}
        onClose={vi.fn()}
        onMerged={onMerged}
      />,
    );

    await pick("agentre-hub");
    await userEvent.click(
      await screen.findByRole("button", {
        name: "Merge as agentre-hub (local)",
      }),
    );

    expect(await screen.findByText(/Merge failed/)).toBeInTheDocument();
    expect(onMerged).not.toHaveBeenCalled();
  });
});

describe("mergeCandidates", () => {
  const item = (id: number, name: string, parentID = 0) =>
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ({ id, name, parentID }) as any;

  it("gives every other project its parent path, at any depth, excluding the source", () => {
    const projects = [
      item(10, "agentre"),
      item(1, "api", 10),
      item(11, "docs", 10),
      item(20, "agentre-server"),
      item(2, "api", 20),
      item(3, "v2", 2),
    ];

    expect(mergeCandidates(projects, 1)).toEqual([
      { id: 10, name: "agentre", parentPath: [] },
      { id: 11, name: "docs", parentPath: ["agentre"] },
      { id: 20, name: "agentre-server", parentPath: [] },
      { id: 2, name: "api", parentPath: ["agentre-server"] },
      { id: 3, name: "v2", parentPath: ["agentre-server", "api"] },
    ]);
  });

  it("a child of the source keeps the source in its path; a broken or cyclic parent chain stops instead of looping", () => {
    const projects = [
      item(1, "api"),
      item(4, "v1", 1),
      item(5, "orphan", 999),
      item(6, "a", 7),
      item(7, "b", 6),
    ];

    expect(mergeCandidates(projects, 1)).toEqual([
      { id: 4, name: "v1", parentPath: ["api"] },
      { id: 5, name: "orphan", parentPath: [] },
      { id: 6, name: "a", parentPath: ["b"] },
      { id: 7, name: "b", parentPath: ["a"] },
    ]);
  });
});
