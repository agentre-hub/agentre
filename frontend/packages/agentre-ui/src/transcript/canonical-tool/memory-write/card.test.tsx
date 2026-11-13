import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { MemoryEntryView } from "../../../memory/types";
import type { TranscriptBlock } from "../../dto";
import type { MemoryTranscriptPorts, TranscriptPorts } from "../../ports";
import { TranscriptPortsProvider } from "../../ports-context";
import { makeTestPorts } from "../../__testing__/ports";

import { CanonicalToolRouter } from "../registry";

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
    ["memory m7 created (agent A)", "Remembered", "lucide-bookmark-plus"],
    ["memory m7 updated (agent A)", "Updated a memory", "lucide-pencil"],
    ["memory m7 archived (agent A)", "Archived a memory", "lucide-archive"],
  ])(
    "Given %s, Then the action is an icon named %s and the summary is shown",
    async (line, name, icon) => {
      renderCard(line, memoryPort());
      const mark = screen.getByRole("img", { name });
      expect(mark.querySelector("svg")).toHaveClass(icon);
      expect(mark).toHaveAttribute("title", name);
      expect(screen.queryByText(name)).toBeNull();
      expect(await screen.findByText("Run lint before pushing")).toBeDefined();
      expect(screen.queryByRole("img", { name: "Global" })).toBeNull();
    },
  );

  it("Given a project layer, Then it shows a folder icon named Project beside the name", async () => {
    renderCard("memory m7 created (project Harbor/App)", memoryPort());
    expect(await screen.findByText("Harbor/App")).toBeDefined();
    const mark = screen.getByRole("img", { name: "Project" });
    expect(mark.querySelector("svg")).toHaveClass("lucide-folder");
    expect(mark).toHaveAttribute("title", "Project");
  });

  it("Given a department layer, Then it shows a users icon named Department beside the name", async () => {
    renderCard("memory m7 created (department Platform)", memoryPort());
    expect(await screen.findByText("Platform")).toBeDefined();
    const mark = screen.getByRole("img", { name: "Department" });
    expect(mark.querySelector("svg")).toHaveClass("lucide-users");
  });

  it("Given the global layer, Then it is a globe icon only, named Global", async () => {
    renderCard("memory m7 updated (global)", memoryPort());
    const mark = await screen.findByRole("img", { name: "Global" });
    expect(mark.querySelector("svg")).toHaveClass("lucide-globe");
    expect(mark).toHaveAttribute("title", "Global");
    expect(screen.queryByText("Global")).toBeNull();
  });

  it("Given an editable write, Then edit and undo are icon buttons with a name and a tip", async () => {
    renderCard("memory m7 updated (agent A)", memoryPort());
    const edit = await screen.findByRole("button", { name: "Edit" });
    const undo = screen.getByRole("button", { name: "Undo" });
    expect(edit).toHaveAttribute("title", "Edit");
    expect(undo).toHaveAttribute("title", "Undo");
    expect(edit.querySelector("svg")).toHaveClass("lucide-pencil");
    expect(undo.querySelector("svg")).toHaveClass("lucide-undo2");
    expect(edit).toHaveTextContent("");
    expect(undo).toHaveTextContent("");
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
    const mark = await screen.findByRole("img", { name: "Undone" });
    expect(mark.querySelector("svg")).toHaveClass("lucide-undo2");
    expect(
      (await screen.findByText("Run lint before pushing")).className,
    ).toContain("line-through");
    expect(screen.queryByRole("button", { name: "Undo" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
  });

  // 撤销后卡片显示为已撤销、不可再点——重新打开会话（卡片重新挂载）之后也是。撤销与记忆页
  // 的「恢复上一版」/「恢复」是同一个动作（决策 9），所以条目最近一次变更是用户做的那一步
  // 就说明这次写入已被撤回。
  it.each([
    [
      "memory m7 updated (agent A)",
      entry({ lastChange: { action: "restored", actor: "user" } }),
    ],
    [
      "memory m7 archived (agent A)",
      entry({ lastChange: { action: "unarchived", actor: "user" } }),
    ],
  ])(
    "a write the user already undid shows undone with no buttons when the card mounts again (%s)",
    async (line, current) => {
      renderCard(line, memoryPort({ get: vi.fn(async () => current) }));
      expect(await screen.findByRole("img", { name: "Undone" })).toBeDefined();
      expect(screen.queryByRole("button", { name: "Undo" })).toBeNull();
      expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
    },
  );

  it("a write changed since by someone else still offers undo (which then explains the refusal)", async () => {
    renderCard(
      "memory m7 updated (agent A)",
      memoryPort({
        get: vi.fn(async () =>
          entry({ lastChange: { action: "edited", actor: "user" } }),
        ),
      }),
    );
    expect(await screen.findByRole("button", { name: "Undo" })).toBeDefined();
    expect(screen.queryByRole("img", { name: "Undone" })).toBeNull();
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
    expect(screen.queryByRole("img", { name: "Undone" })).toBeNull();
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
    expect(await screen.findByRole("img", { name: "Undone" })).toBeDefined();
    expect(screen.queryByRole("button", { name: "Undo" })).toBeNull();
  });

  it("a created write whose entry cannot be read is not mistaken for undone or gone", async () => {
    const get = vi.fn(async () => {
      throw new Error("ipc down");
    });
    renderCard("memory m7 created (agent A)", memoryPort({ get }));
    await waitFor(() => expect(get).toHaveBeenCalled());
    await Promise.resolve();
    const card = screen.getByTestId("memory-write-card");
    expect(card.getAttribute("data-state")).toBe("created");
    expect(screen.getByRole("img", { name: "Remembered" })).toBeDefined();
    expect(screen.queryByRole("img", { name: "Undone" })).toBeNull();
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
    const mark = screen.getByRole("img", { name: "Already remembered" });
    expect(mark.querySelector("svg")).toHaveClass("lucide-bookmark-check");
    expect(await screen.findByText("Run lint before pushing")).toBeDefined();
    expect(screen.queryByRole("button", { name: "Undo" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
  });
});

describe("CanonicalToolRouter with a Codex-wrapped memory write", () => {
  it("renders the memory card instead of the raw command row", () => {
    const codexCall = {
      type: "tool_use",
      toolName: "command_execution",
      toolInput: {
        command: `/usr/bin/zsh -lc "/root/code/memory-round/desktop/build/bin/agrctl create memory --summary '用户偏好简短的中文回复'"`,
      },
    } as unknown as TranscriptBlock;
    const codexResult = {
      type: "tool_result",
      text: JSON.stringify({
        exitCode: 0,
        output: "memory m1 created (agent CEO 助手)\n",
        status: "completed",
      }),
    } as unknown as TranscriptBlock;
    render(
      <TranscriptPortsProvider
        ports={{ ...makeTestPorts(), memory: memoryPort() }}
      >
        <CanonicalToolRouter
          toolBlock={codexCall}
          resultBlock={codexResult}
          sessionId={70}
        />
      </TranscriptPortsProvider>,
    );
    expect(screen.getByTestId("memory-write-card")).toBeDefined();
    expect(screen.queryByTestId("raw-tool-card")).toBeNull();
  });
});
