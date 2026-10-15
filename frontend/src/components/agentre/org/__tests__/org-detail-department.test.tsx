import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { OrgDetailDepartment } from "../org-detail-department";
import type { OrgAgent, OrgDepartment } from "../types";

const dept = (overrides: Partial<OrgDepartment> = {}): OrgDepartment =>
  ({
    id: 1,
    name: "工程部",
    description: "",
    icon: "hammer",
    accentColor: "agent-2",
    parentId: 0,
    leadAgentId: 0,
    leadAgentName: "",
    sortOrder: 0,
    directAgentCount: 0,
    subdepartmentCount: 0,
    memberCount: 0,
    createtime: 0,
    updatetime: 0,
    ...overrides,
  }) as OrgDepartment;

const agent = (overrides: Partial<OrgAgent> = {}): OrgAgent =>
  ({
    id: 2,
    name: "Boris",
    description: "后端工程师",
    avatarColor: "agent-3",
    avatarIcon: "",
    avatarDataUrl: "",
    systemBadge: "",
    departmentId: 1,
    departmentName: "开发组",
    parentAgentId: 0,
    parentAgentName: "",
    agentBackendId: 1,
    sortOrder: 0,
    prompt: [],
    skills: [],
    createtime: 0,
    updatetime: 0,
    ...overrides,
  }) as OrgAgent;

describe("OrgDetailDepartment editor layout", () => {
  it("matches the department drawer labels and source-of-truth previews", () => {
    const parent = dept({
      id: 9,
      name: "工程部",
      icon: "code-xml",
      accentColor: "agent-3",
    });
    const current = dept({
      id: 1,
      name: "开发组",
      icon: "hammer",
      accentColor: "agent-2",
      parentId: parent.id,
      leadAgentId: 2,
    });
    const lead = agent();

    render(
      <OrgDetailDepartment
        department={current}
        allDepartments={[parent, current]}
        allAgents={[lead]}
        leadCandidates={[lead]}
        onUpdate={vi.fn()}
        onMove={vi.fn()}
        onDelete={vi.fn()}
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    );

    expect(screen.getByRole("button", { name: "Icon" })).toHaveTextContent(
      "Hammer",
    );
    expect(
      screen.getByRole("radiogroup", { name: "Theme Color" }),
    ).toBeInTheDocument();
    expect(
      screen.getAllByRole("radio", { name: /Theme color agent-/ }),
    ).toHaveLength(16);
    expect(
      screen.getByRole("radio", { name: "Theme color agent-16" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Parent" })).toHaveTextContent(
      "工程部",
    );
    expect(screen.getByRole("heading", { name: "Leader" })).toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { name: "部门长" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Members" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { name: "成员速览" }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Leader" })).toHaveTextContent(
      "Boris",
    );
    expect(screen.getAllByText("后端工程师").length).toBeGreaterThan(0);
  });
});

describe("OrgDetailDepartment auto-save", () => {
  const baseProps = (onUpdate = vi.fn().mockResolvedValue(undefined)) => ({
    department: dept({ id: 1, name: "开发组", parentId: 0, leadAgentId: 0 }),
    allDepartments: [dept({ id: 1, name: "开发组" })],
    allAgents: [],
    leadCandidates: [],
    onUpdate,
    onMove: vi.fn().mockResolvedValue(undefined),
    onDelete: vi.fn().mockResolvedValue(undefined),
    onSelect: vi.fn(),
    onClose: vi.fn(),
  });

  it("has no header rule, member-row frames or dashed boxes", () => {
    const current = dept({ id: 1, name: "开发组", leadAgentId: 2 });
    const lead = agent();
    const { container } = render(
      <OrgDetailDepartment
        department={current}
        allDepartments={[current]}
        allAgents={[lead]}
        leadCandidates={[lead]}
        onUpdate={vi.fn()}
        onMove={vi.fn()}
        onDelete={vi.fn()}
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    );

    expect(
      [container, ...Array.from(container.querySelectorAll("*"))]
        .flatMap((el) => (el.getAttribute("class") ?? "").split(/\s+/))
        .filter((t) => /^(?:border|divide)(?:-|$)/.test(t)),
    ).toEqual([]);
  });

  it("removes the cancel and save footer buttons", () => {
    render(<OrgDetailDepartment {...baseProps()} />);
    expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Cancel" })).toBeNull();
  });

  it("saves the name on blur", async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    render(<OrgDetailDepartment {...baseProps(onUpdate)} />);
    const input = screen.getByLabelText("Name");
    fireEvent.change(input, { target: { value: "开发二组" } });
    await act(async () => {
      fireEvent.blur(input);
    });
    expect(onUpdate).toHaveBeenCalledWith(
      expect.objectContaining({ id: 1, name: "开发二组" }),
    );
  });

  it("saves immediately when a theme color is picked", async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    render(<OrgDetailDepartment {...baseProps(onUpdate)} />);
    await act(async () => {
      fireEvent.click(screen.getByRole("radio", { name: /agent-5/ }));
    });
    expect(onUpdate).toHaveBeenCalledWith(
      expect.objectContaining({ accentColor: "agent-5" }),
    );
  });
});

describe("OrgDetailDepartment delete dialog", () => {
  it("submits reparent strategy by default", async () => {
    const onDelete = vi.fn().mockResolvedValue(undefined);
    render(
      <OrgDetailDepartment
        department={dept()}
        allDepartments={[]}
        allAgents={[]}
        leadCandidates={[]}
        onUpdate={vi.fn()}
        onMove={vi.fn()}
        onDelete={onDelete}
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    );
    await userEvent.click(
      screen.getAllByRole("button", { name: /Delete department/ })[0],
    );
    await userEvent.click(
      screen.getByRole("button", { name: /Confirm Delete/ }),
    );
    expect(onDelete).toHaveBeenCalledWith({ id: 1, strategy: "reparent" });
  });

  it("submits cascade strategy when picked", async () => {
    const onDelete = vi.fn().mockResolvedValue(undefined);
    render(
      <OrgDetailDepartment
        department={dept()}
        allDepartments={[]}
        allAgents={[]}
        leadCandidates={[]}
        onUpdate={vi.fn()}
        onMove={vi.fn()}
        onDelete={onDelete}
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    );
    await userEvent.click(
      screen.getAllByRole("button", { name: /Delete department/ })[0],
    );
    fireEvent.click(screen.getByLabelText(/Recursively delete/));
    await userEvent.click(
      screen.getByRole("button", { name: /Confirm Delete/ }),
    );
    expect(onDelete).toHaveBeenCalledWith({ id: 1, strategy: "cascade" });
  });
});
