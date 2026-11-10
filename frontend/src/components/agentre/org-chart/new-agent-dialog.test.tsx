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

// B3 / B1 弹窗半边 / B7：提交中不可关、不重复；失败留窗、页脚说事实；有草稿时点遮罩不关。
describe("NewAgentDialog submit", () => {
  async function pointerDownOnOverlay() {
    await new Promise((resolve) => setTimeout(resolve, 0));
    fireEvent.pointerDown(
      document.querySelector('[data-slot="dialog-shell-overlay"]')!,
      { button: 0 },
    );
  }

  function typeName(value: string) {
    fireEvent.change(screen.getByRole("textbox", { name: "Name" }), {
      target: { value },
    });
  }

  it("Given a create in flight, When the user tries to submit again or close, Then nothing closes and only one create is sent", async () => {
    let resolve!: () => void;
    const onSubmit = vi.fn(
      () => new Promise<void>((r) => (resolve = () => r())),
    );
    const { onClose } = renderDialog({ onSubmit });
    typeName("Zoe");
    const create = screen.getByRole("button", { name: "Create" });
    fireEvent.click(create);
    fireEvent.submit(create.closest("form")!);

    await waitFor(() => expect(create).toBeDisabled());
    expect(create.querySelector(".animate-spin")).not.toBeNull();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Close" })).toBeDisabled();
    fireEvent.keyDown(document.body, { key: "Escape" });
    expect(onClose).not.toHaveBeenCalled();
    expect(onSubmit).toHaveBeenCalledOnce();

    resolve();
    await waitFor(() => expect(create).not.toBeDisabled());
  });

  it("Given the backend rejects with a code, When submitted, Then the dialog stays, input is kept and the footer says the backend's sentence with the raw text in title", async () => {
    const raw = "agentre-code:14001 Agent name already exists";
    const onSubmit = vi.fn().mockRejectedValue(new Error(raw));
    const { onClose } = renderDialog({ onSubmit });
    typeName("Zoe");
    fireEvent.click(screen.getByRole("button", { name: "Create" }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Agent name already exists");
    expect(alert).not.toHaveTextContent("agentre-code");
    expect(alert).toHaveAttribute("title", raw);
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByRole("textbox", { name: "Name" })).toHaveValue("Zoe");
    expect(screen.getByRole("button", { name: "Create" })).not.toBeDisabled();
  });

  it("Given an uncoded failure, When submitted, Then the footer uses the generic sentence and the raw text only in title", async () => {
    const onSubmit = vi.fn().mockRejectedValue(new Error("SQLITE_BUSY"));
    renderDialog({ onSubmit });
    typeName("Zoe");
    fireEvent.click(screen.getByRole("button", { name: "Create" }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("The agent wasn't created");
    expect(alert).not.toHaveTextContent("SQLITE_BUSY");
    expect(alert).toHaveAttribute("title", "SQLITE_BUSY");
  });

  it("Given a typed draft, When the overlay is clicked, Then the dialog stays; Esc still closes", async () => {
    const { onClose } = renderDialog();
    typeName("Zoe");
    await pointerDownOnOverlay();
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.keyDown(document.body, { key: "Escape" });
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("Given an untouched form, When the overlay is clicked, Then the dialog closes", async () => {
    const { onClose } = renderDialog();
    await pointerDownOnOverlay();
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("Given an untouched form, When a background reload puts another backend and department first, Then the overlay click still closes", async () => {
    const onClose = vi.fn();
    const backend = (id: number) =>
      ({ id, name: `backend-${id}` }) as NewAgentProps["backends"][number];
    const props: NewAgentProps = {
      open: true,
      departments: [DEPARTMENT],
      agents: [CEO],
      backends: [backend(7)],
      onSubmit: vi.fn().mockResolvedValue(undefined),
      onClose,
    };
    const view = render(
      <MemoryRouter>
        <NewAgentDialog {...props} />
      </MemoryRouter>,
    );
    view.rerender(
      <MemoryRouter>
        <NewAgentDialog
          {...props}
          departments={[
            { id: 2, name: "设计部", parentId: 0 } as unknown as OrgDepartment,
            DEPARTMENT,
          ]}
          backends={[backend(3), backend(7)]}
        />
      </MemoryRouter>,
    );
    await pointerDownOnOverlay();
    expect(onClose).toHaveBeenCalledOnce();
  });
});
