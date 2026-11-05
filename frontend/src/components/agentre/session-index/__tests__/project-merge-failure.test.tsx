import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ProjectMergeDialog } from "../../project-merge-dialog";

/**
 * 合并失败按类别说中文事实（S5）。合并对话框由会话索引的组头菜单打开。
 * 机器原话只进默认折叠的「详情」，不当正文。
 */

const preview = {
  keep: { id: 1, name: "agentre-hub", parentPath: [], claimed: false },
  drop: { id: 2, name: "agentre-hub (copy)", parentPath: [], claimed: false },
};

function stubMergeRejecting(reason: unknown) {
  const ProjectMerge = vi.fn(() => Promise.reject(reason));
  const ProjectMergePreview = vi.fn(() => Promise.resolve(preview));
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  (window as any).go = { app: { App: { ProjectMerge, ProjectMergePreview } } };
}

afterEach(() => {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  delete (window as any).go;
});

async function mergeAndFail(reason: unknown) {
  stubMergeRejecting(reason);
  const onMerged = vi.fn();
  render(
    <ProjectMergeDialog
      source={{ id: 2, name: "agentre-hub (copy)" }}
      candidates={[{ id: 1, name: "agentre-hub" }]}
      onClose={vi.fn()}
      onMerged={onMerged}
    />,
  );
  await userEvent.click(screen.getByRole("combobox", { name: "Merge with" }));
  await userEvent.click(
    await screen.findByRole("option", { name: "agentre-hub" }),
  );
  await userEvent.click(
    await screen.findByRole("button", { name: "Merge as agentre-hub" }),
  );
  return { onMerged, alert: await screen.findByRole("alert") };
}

describe("ProjectMergeDialog failure by kind", () => {
  it("Given an uncoded raw error, When merging fails, Then a plain fact is shown and the raw text only sits in a collapsed Details", async () => {
    const { onMerged, alert } = await mergeAndFail(
      new Error("sql: database is locked"),
    );

    expect(alert).toHaveTextContent("Merge failed");
    const raw = screen.getByText("sql: database is locked");
    const details = raw.closest("details");
    expect(details).not.toBeNull();
    expect(details).not.toHaveAttribute("open");
    expect(screen.getByText("Details").tagName).toBe("SUMMARY");
    expect(onMerged).not.toHaveBeenCalled();
  });

  it("Given a coded conflict, When merging fails, Then the backend sentence shows without the code prefix and no Details", async () => {
    const { alert } = await mergeAndFail(
      new Error("agentre-code:18001 A sibling project already has that name"),
    );

    expect(alert).toHaveTextContent("Merge failed");
    expect(alert).toHaveTextContent("A sibling project already has that name");
    expect(alert).not.toHaveTextContent("agentre-code");
    expect(alert.querySelector("details")).toBeNull();
  });

  it("Given the project is gone (notFound), When merging fails, Then it says so and points back to picking another project", async () => {
    const { alert } = await mergeAndFail(
      "agentre-code:18000 Project not found",
    );

    expect(alert).toHaveTextContent(
      "One of these projects no longer exists — pick another project to merge with",
    );
    expect(alert).not.toHaveTextContent("agentre-code");
  });
});
