import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { OrgDetailAgent } from "../org-detail-agent";
import type { OrgAgent, OrgDepartment } from "../types";

const agent = {
  id: 7,
  name: "Eva",
  description: "Engineering lead",
  avatarColor: "agent-2",
  avatarIcon: "",
  avatarDataUrl: "",
  departmentId: 1,
  prompt: [],
  execTargets: [{ agentBackendId: 5, skills: [] }],
} as unknown as OrgAgent;
const department = {
  id: 1,
  name: "Engineering",
  icon: "preset:team-dev",
  accentColor: "agent-3",
} as OrgDepartment;

function setup(overrides: Partial<OrgAgent> = {}, icon = department.icon) {
  const onUpdate = vi.fn().mockResolvedValue(undefined);
  const onClose = vi.fn();
  const view = render(
    <MemoryRouter>
      <OrgDetailAgent
        agent={{ ...agent, ...overrides } as OrgAgent}
        departments={[{ ...department, icon }]}
        agents={[]}
        backends={[]}
        isLeadOf={department}
        onUpdate={onUpdate}
        onMove={vi.fn().mockResolvedValue(undefined)}
        onDelete={vi.fn().mockResolvedValue(undefined)}
        onUploadAvatar={vi.fn().mockResolvedValue(undefined)}
        onDeleteAvatar={vi.fn().mockResolvedValue(undefined)}
        onClose={onClose}
      />
    </MemoryRouter>,
  );
  return { ...view, onUpdate, onClose };
}

function openMark() {
  fireEvent.click(screen.getByRole("button", { name: "Change avatar" }));
}

beforeEach(() => {
  window.go = {
    app: {
      App: {
        GetBackendCapabilities: vi.fn().mockResolvedValue({ capabilities: [] }),
        ListAgentExecTargetAvailability: vi.fn().mockResolvedValue([]),
      },
    },
  };
});
afterEach(() => {
  vi.useRealTimers();
  delete window.go;
});

describe("desktop Agent shared identity", () => {
  it("Given a selected lead, When rendered, Then one editable identity row contains meta and actions without duplicate fields", () => {
    const { container, onClose } = setup();
    const header = container.querySelector("header")!;
    expect(within(header).getByRole("textbox", { name: "Name" })).toHaveValue(
      "Eva",
    );
    expect(
      within(header).getByRole("textbox", { name: "Description" }),
    ).toHaveValue("Engineering lead");
    expect(
      within(header).getByText("Engineering department lead"),
    ).toBeInTheDocument();
    expect(
      screen.getAllByRole("button", { name: "Change avatar" }),
    ).toHaveLength(1);
    expect(screen.getAllByRole("textbox", { name: "Name" })).toHaveLength(1);
    expect(screen.queryByText("Eva")).toBeNull();
    for (const label of ["Name", "Description", "Avatar", "Avatar Color"]) {
      expect(screen.queryByText(label, { selector: "label, span" })).toBeNull();
    }
    expect(
      within(header).getByRole("button", { name: "Delete Agent" }),
    ).toBeEnabled();
    fireEvent.click(within(header).getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("Given name and description edits, When 600ms elapses, Then one current snapshot is saved", async () => {
    vi.useFakeTimers();
    const { onUpdate } = setup();
    fireEvent.change(screen.getByRole("textbox", { name: "Name" }), {
      target: { value: "Eva II" },
    });
    fireEvent.change(screen.getByRole("textbox", { name: "Description" }), {
      target: { value: "New description" },
    });
    await act(() => vi.advanceTimersByTimeAsync(599));
    expect(onUpdate).not.toHaveBeenCalled();
    await act(() => vi.advanceTimersByTimeAsync(1));
    expect(onUpdate).toHaveBeenCalledExactlyOnceWith(
      expect.objectContaining({
        name: "Eva II",
        description: "New description",
      }),
    );
  });

  it.each(["Name", "Description"])(
    "Given a pending %s edit, When blurred, Then it flushes without another timer save",
    async (field) => {
      vi.useFakeTimers();
      const { onUpdate } = setup();
      const input = screen.getByRole("textbox", { name: field });
      fireEvent.change(input, { target: { value: "Changed" } });
      fireEvent.blur(input);
      expect(onUpdate).toHaveBeenCalledExactlyOnceWith(
        expect.objectContaining({ [field.toLowerCase()]: "Changed" }),
      );
      await act(() => vi.advanceTimersByTimeAsync(600));
      expect(onUpdate).toHaveBeenCalledOnce();
    },
  );

  it("Given pending identity edits, When unmounted, Then the complete snapshot is flushed", async () => {
    vi.useFakeTimers();
    const { onUpdate, unmount } = setup();
    fireEvent.change(screen.getByRole("textbox", { name: "Name" }), {
      target: { value: "Eva II" },
    });
    fireEvent.change(screen.getByRole("textbox", { name: "Description" }), {
      target: { value: "New description" },
    });
    unmount();
    expect(onUpdate).toHaveBeenCalledExactlyOnceWith(
      expect.objectContaining({
        name: "Eva II",
        description: "New description",
      }),
    );
    await act(() => vi.advanceTimersByTimeAsync(600));
    expect(onUpdate).toHaveBeenCalledOnce();
  });

  it.each(["preset", "color"])(
    "Given a pending draft, When a %s is picked, Then the current snapshot saves immediately",
    async (pick) => {
      vi.useFakeTimers();
      const { onUpdate } = setup();
      fireEvent.change(screen.getByRole("textbox", { name: "Description" }), {
        target: { value: "Pending description" },
      });
      openMark();
      if (pick === "preset") {
        fireEvent.click(screen.getByRole("tab", { name: "Presets" }));
        fireEvent.click(screen.getByRole("radio", { name: "CEO Assistant" }));
      } else {
        fireEvent.click(screen.getByRole("button", { name: "agent-16" }));
      }
      expect(onUpdate).toHaveBeenCalledExactlyOnceWith(
        expect.objectContaining({
          description: "Pending description",
          ...(pick === "preset"
            ? { avatarIcon: "preset:agent-assistant" }
            : { avatarColor: "agent-16" }),
        }),
      );
      await act(() => vi.advanceTimersByTimeAsync(600));
      expect(onUpdate).toHaveBeenCalledOnce();
    },
  );

  it.each([false, true])(
    "Given an invalid whole snapshot (missing target: %s), When picking a color or flushing, Then no save bypasses validation",
    async (missingTarget) => {
      vi.useFakeTimers();
      const { onUpdate, unmount } = setup(
        missingTarget ? { execTargets: [] } : {},
      );
      if (!missingTarget)
        fireEvent.change(screen.getByRole("textbox", { name: "Name" }), {
          target: { value: " " },
        });
      openMark();
      fireEvent.click(screen.getByRole("button", { name: "agent-16" }));
      fireEvent.blur(screen.getByRole("textbox", { name: "Name" }));
      await act(() => vi.advanceTimersByTimeAsync(600));
      unmount();
      expect(onUpdate).not.toHaveBeenCalled();
    },
  );

  it.each(["preset:team-dev", "hammer", ""])(
    "Given department icon %s, When placement renders, Then its mark uses preset, icon or the first character",
    (icon) => {
      setup({}, icon);
      const placement = screen.getByRole("combobox", { name: "Placement" });
      const mark = within(placement).getByRole("img", { name: "Engineering" });
      if (icon.startsWith("preset:"))
        expect(mark.querySelector("img")).toHaveAttribute(
          "src",
          expect.stringContaining("team-dev.webp"),
        );
      else if (icon) expect(mark.querySelector("svg")).toBeInTheDocument();
      else expect(mark).toHaveTextContent("E");
    },
  );
});
