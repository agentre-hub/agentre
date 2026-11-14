import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { MemoryRow } from "./memory-row";
import type { MemoryEntryView, MemoryRowProps } from "./types";

const THIS_YEAR = new Date().getFullYear();

function entry(overrides: Partial<MemoryEntryView> = {}): MemoryEntryView {
  return {
    id: 7,
    summary: "Run lint before pushing",
    detail: "",
    pinned: false,
    archived: false,
    hasPrevious: false,
    creator: { kind: "user" },
    createdAt: new Date(THIS_YEAR, 9, 21).getTime(),
    updatedAt: new Date(THIS_YEAR, 10, 4).getTime(),
    ...overrides,
  };
}

function renderRow(overrides: Partial<MemoryRowProps> = {}) {
  const props: MemoryRowProps = {
    entry: entry(),
    editing: false,
    onEditingChange: vi.fn(),
    onSave: vi.fn(async () => ({ ok: true as const })),
    onAction: vi.fn(async () => ({ ok: true as const })),
    moveTargets: [
      { key: "global", label: "Global" },
      { key: "project:3", label: "Harbor", group: "Projects" },
    ],
    ...overrides,
  };
  const view = render(<MemoryRow {...props} />);
  return { ...view, props };
}

async function openMenu() {
  await userEvent.click(screen.getByRole("button", { name: "More actions" }));
}

afterEach(() => {
  vi.useRealTimers();
});

describe("MemoryRow display", () => {
  it("Given a user-written entry with detail, Then it shows the summary, who wrote it, the last-modified date and a detail chip", () => {
    renderRow({ entry: entry({ detail: "why" }) });
    const row = screen.getByTestId("memory-row-7");
    expect(row).toHaveTextContent("Run lint before pushing");
    expect(row).toHaveTextContent("11-04");
    const you = within(row).getByRole("img", { name: "Written by you" });
    expect(you.querySelector("svg")).toHaveClass("lucide-user");
    expect(you).toHaveAttribute("title", "Written by you");
    const detail = within(row).getByRole("img", { name: "Has detail" });
    expect(detail.querySelector("svg")).toHaveClass("lucide-text");
    expect(detail).toHaveAttribute("title", "Has detail");
    expect(within(row).queryByText("Written by you")).toBeNull();
    expect(within(row).queryByText("Has detail")).toBeNull();
  });

  it("Given an AI-written entry, Then the creator links back to its session", async () => {
    const onOpen = vi.fn();
    renderRow({
      entry: entry({
        creator: { kind: "ai", label: "Fix login retry", onOpen },
      }),
    });
    expect(screen.queryByRole("img", { name: "Has detail" })).toBeNull();
    await userEvent.click(
      screen.getByRole("button", { name: "Fix login retry" }),
    );
    expect(onOpen).toHaveBeenCalledTimes(1);
    const from = screen.getByRole("img", { name: "From session" });
    expect(from.querySelector("svg")).toHaveClass("lucide-message-square");
    expect(from).toHaveAttribute("title", "From session");
    expect(screen.queryByText("From session")).toBeNull();
  });

  it("When the row is clicked, Then it asks to start editing; while editing it shows the form", async () => {
    const { props, rerender } = renderRow();
    await userEvent.click(
      screen.getByRole("button", { name: "Edit Run lint before pushing" }),
    );
    expect(props.onEditingChange).toHaveBeenCalledWith(true);

    rerender(<MemoryRow {...props} editing />);
    expect(screen.getByRole("textbox", { name: "Summary" })).toHaveValue(
      "Run lint before pushing",
    );
  });

  it("Given editing and the save succeeds, Then the row leaves edit mode", async () => {
    const { props } = renderRow({ editing: true });
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(props.onSave).toHaveBeenCalled();
    expect(props.onEditingChange).toHaveBeenCalledWith(false);
  });

  it("Given editing and the save fails, Then the row stays in edit mode", async () => {
    const onSave = vi.fn(async () => ({ ok: false as const }));
    const { props } = renderRow({ editing: true, onSave });
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await screen.findByRole("alert");
    expect(props.onEditingChange).not.toHaveBeenCalledWith(false);
  });
});

describe("MemoryRow actions", () => {
  it("Given a normal entry without a previous version, Then the menu offers pin, archive, move and delete but not restore previous", async () => {
    renderRow();
    await openMenu();
    const items = (await screen.findAllByRole("menuitem")).map(
      (item) => item.textContent,
    );
    expect(items).toEqual(
      expect.arrayContaining(["Pin", "Archive", "Move to…", "Delete"]),
    );
    expect(items).not.toContain("Restore previous version");
  });

  it("Given a pinned, archived entry with a previous version, Then the menu says unpin, restore and restore previous version", async () => {
    const { props } = renderRow({
      entry: entry({ pinned: true, archived: true, hasPrevious: true }),
    });
    await openMenu();
    await userEvent.click(
      await screen.findByRole("menuitem", { name: "Unpin" }),
    );
    expect(props.onAction).toHaveBeenLastCalledWith({
      type: "pin",
      pinned: false,
    });

    await openMenu();
    await userEvent.click(
      await screen.findByRole("menuitem", { name: "Restore" }),
    );
    expect(props.onAction).toHaveBeenLastCalledWith({ type: "unarchive" });

    await openMenu();
    await userEvent.click(
      await screen.findByRole("menuitem", {
        name: "Restore previous version",
      }),
    );
    expect(props.onAction).toHaveBeenLastCalledWith({
      type: "restorePrevious",
    });
  });

  it("When Move to… a target is picked, Then onAction gets that target key", async () => {
    const { props } = renderRow();
    await openMenu();
    await userEvent.click(
      await screen.findByRole("menuitem", { name: "Move to…" }),
    );
    expect(await screen.findByText("Projects")).toBeInTheDocument();
    await userEvent.click(
      await screen.findByRole("menuitem", { name: "Harbor" }),
    );
    expect(props.onAction).toHaveBeenCalledWith({
      type: "move",
      targetKey: "project:3",
    });
  });

  it("When Delete is clicked once, Then it turns into a confirmation and deletes only on the second click", async () => {
    const { props } = renderRow();
    await openMenu();
    await userEvent.click(
      await screen.findByRole("menuitem", { name: "Delete" }),
    );
    expect(props.onAction).not.toHaveBeenCalled();
    const confirm = await screen.findByRole("menuitem", {
      name: "Confirm delete?",
    });
    await userEvent.click(confirm);
    expect(props.onAction).toHaveBeenCalledWith({ type: "delete" });
  });

  it("Given Delete was clicked once, When about 2 seconds pass, Then it reverts and a later click only asks again", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { props } = renderRow();
    const user = userEvent.setup({
      advanceTimers: vi.advanceTimersByTime.bind(vi),
    });
    await user.click(screen.getByRole("button", { name: "More actions" }));
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
    expect(
      screen.getByRole("menuitem", { name: "Confirm delete?" }),
    ).toBeInTheDocument();

    act(() => {
      vi.advanceTimersByTime(2500);
    });

    const reverted = screen.getByRole("menuitem", { name: "Delete" });
    await user.click(reverted);
    expect(props.onAction).not.toHaveBeenCalled();
  });

  it("Given an action fails, Then the failure is reported under the row", async () => {
    renderRow({
      onAction: vi.fn(async () => ({
        ok: false as const,
        reason: "The memory no longer exists",
      })),
    });
    await openMenu();
    await userEvent.click(
      await screen.findByRole("menuitem", { name: "Archive" }),
    );
    const alert = await within(screen.getByTestId("memory-row-7")).findByRole(
      "alert",
    );
    expect(alert).toHaveTextContent("Didn't go through");
    expect(alert).toHaveTextContent("The memory no longer exists");
  });
});

// 日期：同一年只写月-日，跨年带上年份。
describe("MemoryRow date", () => {
  it("Given an entry last modified in another year, Then the year is shown", () => {
    renderRow({
      entry: entry({ updatedAt: new Date(THIS_YEAR - 2, 0, 2).getTime() }),
    });
    expect(screen.getByTestId("memory-row-7")).toHaveTextContent(
      `${THIS_YEAR - 2}-01-02`,
    );
  });
});
