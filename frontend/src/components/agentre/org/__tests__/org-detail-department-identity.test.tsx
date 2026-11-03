import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { OrgDetailDepartment } from "../org-detail-department";
import type { OrgDepartment } from "../types";

// 部门详情的身份只有**一行**（共享身份区），名字 / 描述 / 记号 / 主题色都在这一行；
// 三栏里不再有第二个名字框、第二个年龄……第二个图标选择器与调色板。
//
// 记号按规格 2026-11-02 的优先级显示：预设 ▸ 图标 ▸ 部门名首字，且**没有图标就是
// 没有图标**——旧代码把空 icon 改写成 "puzzle" 再写回库，这一条守的就是它别回来。

const parent: OrgDepartment = {
  id: 9,
  name: "工程部",
  description: "",
  icon: "code-xml",
  accentColor: "agent-3",
  parentId: 0,
  leadAgentId: 0,
  leadAgentName: "",
  sortOrder: 0,
  directAgentCount: 0,
  subdepartmentCount: 1,
  memberCount: 0,
  createtime: 0,
  updatetime: 0,
} as OrgDepartment;

const dept = (overrides: Partial<OrgDepartment> = {}): OrgDepartment =>
  ({
    id: 1,
    name: "开发组",
    description: "负责桌面端",
    icon: "hammer",
    accentColor: "agent-2",
    parentId: 9,
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

function setup(overrides: Partial<OrgDepartment> = {}) {
  const onUpdate = vi.fn().mockResolvedValue(undefined);
  const onClose = vi.fn();
  const current = dept(overrides);
  const view = render(
    <OrgDetailDepartment
      department={current}
      allDepartments={[parent, current]}
      allAgents={[]}
      leadCandidates={[]}
      onUpdate={onUpdate}
      onMove={vi.fn().mockResolvedValue(undefined)}
      onDelete={vi.fn().mockResolvedValue(undefined)}
      onSelect={vi.fn()}
      onClose={onClose}
    />,
  );
  return { ...view, onUpdate, onClose, current };
}

function openMark() {
  fireEvent.click(screen.getByRole("button", { name: "Change icon" }));
}

describe("desktop department shared identity", () => {
  it("Given a department, When the detail renders, Then one identity row holds its name, description, path and actions", () => {
    const { container, onClose } = setup();
    const header = container.querySelector("header")!;

    expect(within(header).getByRole("textbox", { name: "Name" })).toHaveValue(
      "开发组",
    );
    expect(
      within(header).getByRole("textbox", { name: "Description" }),
    ).toHaveValue("负责桌面端");
    // 原有的路径信息原样留在这一行里，不再单独占一条只读标题。
    expect(within(header).getByText("Path")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Change icon" })).toHaveLength(
      1,
    );
    expect(screen.getAllByRole("textbox", { name: "Name" })).toHaveLength(1);
    for (const label of ["Name", "Description", "Icon", "Theme Color"]) {
      expect(screen.queryByText(label, { selector: "label, span" })).toBeNull();
    }
    expect(
      screen.queryByRole("radiogroup", { name: "Theme Color" }),
    ).toBeNull();

    const actions = within(header);
    expect(
      actions.getByRole("button", { name: "Delete department" }),
    ).toBeEnabled();
    fireEvent.click(actions.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("Given name and description edits, When 600ms elapses, Then one current snapshot is saved", async () => {
    vi.useFakeTimers();
    const { onUpdate } = setup();
    fireEvent.change(screen.getByRole("textbox", { name: "Name" }), {
      target: { value: "开发二组" },
    });
    fireEvent.change(screen.getByRole("textbox", { name: "Description" }), {
      target: { value: "新简介" },
    });
    await act(() => vi.advanceTimersByTimeAsync(599));
    expect(onUpdate).not.toHaveBeenCalled();
    await act(() => vi.advanceTimersByTimeAsync(1));
    expect(onUpdate).toHaveBeenCalledExactlyOnceWith(
      expect.objectContaining({
        id: 1,
        name: "开发二组",
        description: "新简介",
      }),
    );
  });

  it.each(["Name", "Description"] as const)(
    "Given a pending %s edit, When blurred, Then it flushes without another timer save",
    async (field) => {
      vi.useFakeTimers();
      const { onUpdate } = setup();
      const input = screen.getByRole("textbox", { name: field });
      fireEvent.change(input, { target: { value: "改过" } });
      fireEvent.blur(input);
      expect(onUpdate).toHaveBeenCalledExactlyOnceWith(
        expect.objectContaining({ [field.toLowerCase()]: "改过" }),
      );
      await act(() => vi.advanceTimersByTimeAsync(600));
      expect(onUpdate).toHaveBeenCalledOnce();
    },
  );

  it("Given pending identity edits, When unmounted, Then the complete snapshot is flushed", async () => {
    vi.useFakeTimers();
    const { onUpdate, unmount } = setup();
    fireEvent.change(screen.getByRole("textbox", { name: "Name" }), {
      target: { value: "开发二组" },
    });
    unmount();
    expect(onUpdate).toHaveBeenCalledExactlyOnceWith(
      expect.objectContaining({ id: 1, name: "开发二组" }),
    );
    await act(() => vi.advanceTimersByTimeAsync(600));
    expect(onUpdate).toHaveBeenCalledOnce();
  });

  it("Given an empty name, When the snapshot is flushed, Then nothing is saved", async () => {
    vi.useFakeTimers();
    const { onUpdate } = setup();
    const input = screen.getByRole("textbox", { name: "Name" });
    fireEvent.change(input, { target: { value: "  " } });
    fireEvent.blur(input);
    await act(() => vi.advanceTimersByTimeAsync(600));
    expect(onUpdate).not.toHaveBeenCalled();
  });

  it.each(["preset", "color"] as const)(
    "Given a pending draft, When a %s is picked, Then the current snapshot saves immediately",
    async (pick) => {
      vi.useFakeTimers();
      const { onUpdate } = setup();
      fireEvent.change(screen.getByRole("textbox", { name: "Description" }), {
        target: { value: "待保存的简介" },
      });
      openMark();
      if (pick === "preset") {
        fireEvent.click(screen.getByRole("tab", { name: "Presets" }));
        fireEvent.click(screen.getByRole("radio", { name: "Engineering" }));
      } else {
        fireEvent.click(screen.getByRole("button", { name: "agent-5" }));
      }
      expect(onUpdate).toHaveBeenCalledExactlyOnceWith(
        expect.objectContaining({
          description: "待保存的简介",
          ...(pick === "preset"
            ? { icon: "preset:team-dev" }
            : { accentColor: "agent-5" }),
        }),
      );
      await act(() => vi.advanceTimersByTimeAsync(600));
      expect(onUpdate).toHaveBeenCalledOnce();
    },
  );

  it("Given a preset icon, When 取消预设 is picked, Then the snapshot saves an empty icon instead of a puzzle", () => {
    const { onUpdate } = setup({ icon: "preset:team-dev" });
    openMark();
    fireEvent.click(screen.getByRole("button", { name: "Clear preset" }));
    expect(onUpdate).toHaveBeenCalledExactlyOnceWith(
      expect.objectContaining({ id: 1, icon: "" }),
    );
  });

  it.each(["preset:team-dev", "hammer", ""] as const)(
    "Given department icon %s, When the identity row renders, Then its mark uses the preset, the icon or the first character",
    (icon) => {
      setup({ icon });
      const mark = screen.getByRole("img", { name: "开发组" });
      if (icon.startsWith("preset:")) {
        expect(mark.querySelector("img")).toHaveAttribute(
          "src",
          expect.stringContaining("team-dev.webp"),
        );
      } else if (icon) {
        expect(mark.querySelector("svg")).toHaveClass(`lucide-${icon}`);
      } else {
        expect(mark).toHaveTextContent(/^开$/);
        expect(mark.querySelector("svg")).toBeNull();
      }
    },
  );

  it("Given a department without an icon, When the name is edited and saved, Then the icon stays empty", async () => {
    vi.useFakeTimers();
    const { onUpdate } = setup({ icon: "" });
    fireEvent.change(screen.getByRole("textbox", { name: "Name" }), {
      target: { value: "开发二组" },
    });
    await act(() => vi.advanceTimersByTimeAsync(600));
    expect(onUpdate).toHaveBeenCalledExactlyOnceWith(
      expect.objectContaining({ name: "开发二组", icon: "" }),
    );
    expect(vi.mocked(onUpdate).mock.calls[0][0].icon).not.toBe("puzzle");
  });
});

afterEach(() => {
  vi.useRealTimers();
});
