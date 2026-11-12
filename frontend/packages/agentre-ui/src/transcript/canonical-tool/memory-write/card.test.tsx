import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { MemoryEntryView } from "../../../memory/types";
import type { TranscriptBlock } from "../../dto";
import type { MemoryTranscriptPorts, TranscriptPorts } from "../../ports";
import { TranscriptPortsProvider } from "../../ports-context";
import { makeTestPorts } from "../../__testing__/ports";

import { MemoryWriteCard } from "./card";

const entry = (over: Partial<MemoryEntryView> = {}): MemoryEntryView => ({
  id: 7,
  summary: "Run lint before pushing",
  detail: "CI does not run it",
  pinned: false,
  archived: false,
  hasPrevious: false,
  creator: { kind: "user" },
  createdAt: 0,
  updatedAt: 0,
  ...over,
});

const call = {
  type: "tool_use",
  toolName: "Bash",
  toolInput: { command: 'agrctl create memory --summary "x"' },
} as unknown as TranscriptBlock;

const resultOf = (line: string) =>
  ({ type: "tool_result", text: line + "\n" }) as unknown as TranscriptBlock;

function memoryPort(
  over: Partial<MemoryTranscriptPorts> = {},
): MemoryTranscriptPorts {
  return {
    get: vi.fn(async () => entry()),
    undoWrite: vi.fn(async () => ({ status: "undone" as const })),
    save: vi.fn(async () => ({ ok: true as const })),
    ...over,
  };
}

function renderCard(
  line: string,
  memory: MemoryTranscriptPorts | undefined,
  extra: Partial<TranscriptPorts> = {},
) {
  return render(
    <TranscriptPortsProvider ports={makeTestPorts({ memory, ...extra })}>
      <MemoryWriteCard
        toolBlock={call}
        resultBlock={resultOf(line)}
        sessionId={70}
      />
    </TranscriptPortsProvider>,
  );
}

describe("MemoryWriteCard", () => {
  it.each([
    ["memory m7 created (agent A)", "Remembered"],
    ["memory m7 updated (agent A)", "Updated a memory"],
    ["memory m7 archived (agent A)", "Archived a memory"],
  ])("shows the action and the summary for %s", async (line, label) => {
    renderCard(line, memoryPort());
    expect(screen.getByText(label)).toBeDefined();
    expect(await screen.findByText("Run lint before pushing")).toBeDefined();
    expect(screen.queryByText("Global")).toBeNull();
  });

  it("names a shared layer on the card", async () => {
    renderCard("memory m7 created (project Harbor/App)", memoryPort());
    expect(await screen.findByText("Project Harbor/App")).toBeDefined();
  });

  it("names the global layer", async () => {
    renderCard("memory m7 updated (global)", memoryPort());
    expect(await screen.findByText("Global")).toBeDefined();
  });

  it("undoing a write calls the port with the session and action, then shows undone and no buttons", async () => {
    const memory = memoryPort();
    renderCard("memory m7 updated (agent A)", memory);
    await userEvent.click(await screen.findByRole("button", { name: "Undo" }));

    expect(memory.undoWrite).toHaveBeenCalledWith({
      id: 7,
      sessionId: 70,
      action: "updated",
    });
    expect(await screen.findByText("Undone")).toBeDefined();
    expect(screen.queryByRole("button", { name: "Undo" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
  });

  it("a refused undo keeps the buttons and explains why in place", async () => {
    const memory = memoryPort({
      undoWrite: vi.fn(async () => ({ status: "changed" as const })),
    });
    renderCard("memory m7 created (agent A)", memory);
    await userEvent.click(await screen.findByRole("button", { name: "Undo" }));

    expect(
      await screen.findByText(
        "Can't undo: this memory was changed after this write",
      ),
    ).toBeDefined();
    expect(screen.getByRole("button", { name: "Undo" })).toBeDefined();
    expect(screen.queryByText("Undone")).toBeNull();
  });

  it("a failed undo shows the host's reason and stays clickable", async () => {
    const memory = memoryPort({
      undoWrite: vi.fn(async () => ({
        status: "failed" as const,
        reason: "disk full",
      })),
    });
    renderCard("memory m7 archived (agent A)", memory);
    await userEvent.click(await screen.findByRole("button", { name: "Undo" }));
    expect(await screen.findByText("Couldn't undo: disk full")).toBeDefined();
    expect(screen.getByRole("button", { name: "Undo" })).toBeDefined();
  });

  it("an archived write offers undo but no edit", async () => {
    renderCard("memory m7 archived (agent A)", memoryPort());
    await screen.findByRole("button", { name: "Undo" });
    expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
  });

  it("a created write whose entry is already gone renders as undone", async () => {
    renderCard(
      "memory m7 created (agent A)",
      memoryPort({ get: vi.fn(async () => null) }),
    );
    expect(await screen.findByText("Undone")).toBeDefined();
    expect(screen.queryByRole("button", { name: "Undo" })).toBeNull();
  });

  it("an updated write whose entry is gone says so and offers nothing", async () => {
    renderCard(
      "memory m7 updated (agent A)",
      memoryPort({ get: vi.fn(async () => null) }),
    );
    expect(
      await screen.findByText("This memory no longer exists"),
    ).toBeDefined();
    expect(screen.queryByRole("button", { name: "Undo" })).toBeNull();
  });

  it("Edit opens the memory edit form in place and saving goes through the port", async () => {
    const memory = memoryPort();
    renderCard("memory m7 created (agent A)", memory);
    await userEvent.click(await screen.findByRole("button", { name: "Edit" }));

    const summary = screen.getByRole("textbox", { name: "Summary" });
    expect((summary as HTMLInputElement).value).toBe("Run lint before pushing");
    await userEvent.type(summary, "!");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(memory.save).toHaveBeenCalledWith(7, {
      summary: "Run lint before pushing!",
      detail: "CI does not run it",
    });
    await waitFor(() =>
      expect(screen.queryByRole("textbox", { name: "Summary" })).toBeNull(),
    );
    expect(memory.get).toHaveBeenCalledTimes(2);
  });

  it("a failed save keeps the form open with the reason", async () => {
    const memory = memoryPort({
      save: vi.fn(async () => ({ ok: false as const, reason: "too long" })),
    });
    renderCard("memory m7 created (agent A)", memory);
    await userEvent.click(await screen.findByRole("button", { name: "Edit" }));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByRole("alert")).toBeDefined();
    expect(screen.getByRole("textbox", { name: "Summary" })).toBeDefined();
  });

  it("an already-exists result means nothing changed: no undo, no edit", async () => {
    const memory = memoryPort();
    renderCard("memory m7 already exists (global)", memory);
    expect(screen.getByText("Already remembered")).toBeDefined();
    expect(await screen.findByText("Run lint before pushing")).toBeDefined();
    expect(screen.queryByRole("button", { name: "Undo" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
  });
});
