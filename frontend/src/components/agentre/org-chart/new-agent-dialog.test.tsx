import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { useSettingsDialogStore } from "@/stores/settings-dialog-store";

import { NewAgentDialog, type NewAgentProps } from "./new-agent-dialog";
import type { OrgAgent, OrgDepartment } from "../org/types";

// 共享身份区只修改草稿，输入框用可及名代替独立标签。

const DEPARTMENT = {
  id: 1,
  name: "工程部",
  parentId: 0,
} as unknown as OrgDepartment;

const CEO = {
  id: 1,
  name: "CEO 助手",
  systemBadge: "DEFAULT",
  departmentId: 0,
  parentAgentId: 0,
} as unknown as OrgAgent;

function renderDialog(overrides: Partial<NewAgentProps> = {}) {
  const props: NewAgentProps = {
    open: true,
    departments: [DEPARTMENT],
    agents: [CEO],
    backends: [],
    onSubmit: vi.fn().mockResolvedValue(undefined),
    onClose: vi.fn(),
    ...overrides,
  };
  render(
    <MemoryRouter>
      <NewAgentDialog {...props} />
    </MemoryRouter>,
  );
  return props;
}

describe("NewAgentDialog accessibility", () => {
  it("names the agent name and description fields", () => {
    renderDialog();
    expect(screen.getByRole("textbox", { name: "Name" })).toBeInTheDocument();
    expect(
      screen.getByRole("textbox", { name: /^Description/ }),
    ).toBeInTheDocument();
  });

  it("Given a new draft, When rendered, Then one identity area replaces separate labels and disallows upload until creation", () => {
    renderDialog();
    expect(
      screen.getAllByRole("button", { name: "Change avatar" }),
    ).toHaveLength(1);
    for (const label of ["Name", "Description", "Avatar", "Avatar Color"]) {
      expect(screen.queryByText(label, { selector: "label, span" })).toBeNull();
    }
    expect(screen.getByRole("button", { name: "Create" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Change avatar" }));
    fireEvent.click(screen.getByRole("tab", { name: "Image" }));
    expect(
      screen.getByText(
        "Upload an avatar after creating this agent. Pick an icon or initials first.",
      ),
    ).toBeInTheDocument();
    expect(document.querySelector('input[type="file"]')).toBeNull();
  });

  it("Given untouched optional fields, When created, Then defaults are preserved and unset description/icon are omitted", async () => {
    const { onSubmit } = renderDialog();
    fireEvent.change(screen.getByRole("textbox", { name: "Name" }), {
      target: { value: " New Agent " },
    });
    expect(onSubmit).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledOnce());
    const payload = vi.mocked(onSubmit).mock.calls[0][0];
    expect(payload).toMatchObject({
      name: "New Agent",
      avatarColor: "agent-1",
      departmentId: 1,
      parentAgentId: 0,
      agentBackendId: 0,
      prompt: [],
      skills: [],
    });
    expect(payload).not.toHaveProperty("avatarIcon");
    expect(payload).not.toHaveProperty("description");
  });

  it("Given identity edits, When a preset and color are picked, Then only the draft changes until submit includes them", async () => {
    const { onSubmit } = renderDialog();
    fireEvent.change(screen.getByRole("textbox", { name: "Name" }), {
      target: { value: "Draft" },
    });
    fireEvent.change(screen.getByRole("textbox", { name: "Description" }), {
      target: { value: " Description " },
    });
    fireEvent.click(screen.getByRole("button", { name: "Change avatar" }));
    fireEvent.click(screen.getByRole("button", { name: "agent-16" }));
    fireEvent.click(screen.getByRole("tab", { name: "Presets" }));
    fireEvent.click(screen.getByRole("radio", { name: "CEO Assistant" }));
    expect(onSubmit).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledExactlyOnceWith(
        expect.objectContaining({
          name: "Draft",
          description: "Description",
          avatarColor: "agent-16",
          avatarIcon: "preset:agent-assistant",
        }),
      ),
    );
  });
});

describe("NewAgentDialog settings entry", () => {
  beforeEach(() => {
    useSettingsDialogStore.setState({ open: false, page: "appearance" });
  });

  it("closes itself first, then opens the settings dialog on the agent-backend page", () => {
    let settingsOpenWhenClosed: boolean | null = null;
    const onClose = vi.fn(() => {
      settingsOpenWhenClosed = useSettingsDialogStore.getState().open;
    });
    renderDialog({ fromIntent: true, onClose });

    fireEvent.click(screen.getByRole("button", { name: /Go to settings/ }));

    expect(onClose).toHaveBeenCalledTimes(1);
    expect(settingsOpenWhenClosed).toBe(false);
    expect(useSettingsDialogStore.getState()).toMatchObject({
      open: true,
      page: "agent-backend",
    });
  });
});
