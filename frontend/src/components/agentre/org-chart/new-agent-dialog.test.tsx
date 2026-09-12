import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { NewAgentDialog, type NewAgentProps } from "./new-agent-dialog";
import type { OrgAgent, OrgDepartment } from "../org/types";

// 可及性:名称 / 简介两个输入框必须有可及名称,且名称来自它自己的 <label> ——
// 读屏用户靠它分辨字段,键盘用户靠原生关联点标题就能聚焦到控件。

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

  it("associates each field with its own label element", () => {
    renderDialog();
    const name = screen.getByRole("textbox", { name: "Name" });
    const description = screen.getByRole("textbox", { name: /^Description/ });
    expect(name.closest("label")).not.toBeNull();
    expect(description.closest("label")).not.toBeNull();
    expect(name.closest("label")).not.toBe(description.closest("label"));
  });
});
