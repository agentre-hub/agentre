import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import {
  NewDepartmentDialog,
  type NewDeptProps,
} from "./new-department-dialog";
import type { OrgDepartment } from "../org/types";

// 新建部门用共享身份区：只改草稿，「创建」时一起提交；默认值仍是 hammer + agent-2；
// 用户没设置的字段（描述、被清空的图标）不进请求。

const DEPARTMENT = {
  id: 1,
  name: "工程部",
  parentId: 0,
} as unknown as OrgDepartment;

function renderDialog(overrides: Partial<NewDeptProps> = {}) {
  const props: NewDeptProps = {
    open: true,
    departments: [DEPARTMENT],
    agents: [],
    onSubmit: vi.fn().mockResolvedValue(undefined),
    onClose: vi.fn(),
    ...overrides,
  };
  render(<NewDepartmentDialog {...props} />);
  return props;
}

describe("NewDepartmentDialog identity area", () => {
  it("Given a new draft, When rendered, Then one identity area replaces the separate name, icon and color fields", () => {
    renderDialog();

    expect(screen.getByRole("textbox", { name: "Name" })).toBeInTheDocument();
    expect(
      screen.getByRole("textbox", { name: "Description" }),
    ).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Change icon" })).toHaveLength(
      1,
    );
    for (const label of ["Name", "Description", "Icon", "Theme Color"]) {
      expect(screen.queryByText(label, { selector: "label, span" })).toBeNull();
    }
    // 原有的归属下拉还在。
    expect(
      screen.getByRole("combobox", { name: "Parent" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create" })).toBeDisabled();
  });

  it("Given untouched optional fields, When created, Then the hammer icon and agent-2 defaults are kept and the empty description is omitted", async () => {
    const { onSubmit } = renderDialog();

    // 默认记号是 hammer、默认主题色是 agent-2，在身份区里就能看到。
    expect(
      screen.getByRole("button", { name: "Change icon" }).querySelector("svg"),
    ).toHaveClass("lucide-hammer");
    fireEvent.click(screen.getByRole("button", { name: "Change icon" }));
    expect(screen.getByRole("button", { name: "agent-2" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    fireEvent.click(screen.getByRole("button", { name: "Change icon" }));

    fireEvent.change(screen.getByRole("textbox", { name: "Name" }), {
      target: { value: " 设计部 " },
    });
    expect(onSubmit).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledOnce());
    const payload = vi.mocked(onSubmit).mock.calls[0][0];
    expect(payload).toEqual({
      name: "设计部",
      icon: "hammer",
      accentColor: "agent-2",
      parentId: 0,
    });
    expect(payload).not.toHaveProperty("description");
  });

  it("Given a description and a preset, When created, Then only the draft changes and the payload carries both", async () => {
    const { onSubmit } = renderDialog();
    fireEvent.change(screen.getByRole("textbox", { name: "Name" }), {
      target: { value: "设计部" },
    });
    fireEvent.change(screen.getByRole("textbox", { name: "Description" }), {
      target: { value: " 负责视觉 " },
    });
    fireEvent.click(screen.getByRole("button", { name: "Change icon" }));
    fireEvent.click(screen.getByRole("tab", { name: "Presets" }));
    fireEvent.click(screen.getByRole("radio", { name: "Design" }));
    expect(onSubmit).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledExactlyOnceWith(
        expect.objectContaining({
          name: "设计部",
          description: "负责视觉",
          icon: "preset:team-design",
          accentColor: "agent-2",
        }),
      ),
    );
  });

  it("Given a cleared icon, When created, Then the icon is omitted from the payload", async () => {
    const { onSubmit } = renderDialog();
    fireEvent.change(screen.getByRole("textbox", { name: "Name" }), {
      target: { value: "设计部" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Change icon" }));
    fireEvent.click(screen.getByRole("button", { name: "Clear icon" }));
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledOnce());
    expect(vi.mocked(onSubmit).mock.calls[0][0]).not.toHaveProperty("icon");
  });
});
