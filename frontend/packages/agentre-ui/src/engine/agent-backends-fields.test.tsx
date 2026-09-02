import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ReasoningEffortField } from "./agent-backends-fields";

// spec 2026-09-01「三后端下发档位的收敛」+ 设计决策 2:档位表统一六档,删除
// REASONING_EFFORTS_CODEX 这条按后端裁剪的分支——编辑器对四个支持力度的后端
// (claudecode / codex / piagent / builtin，调用方按 type !== "openclaw" 门控是否
// 渲染这颗字段)呈现同一张六档表,codex 不再被单独藏 max。组件本身不再吃 type，
// 这张表因此对所有调用方恒等。
describe("ReasoningEffortField", () => {
  it("展示同一张六档表(含 max)", async () => {
    render(<ReasoningEffortField value="" onChange={vi.fn()} />);
    await userEvent.click(screen.getByRole("combobox"));
    const options = screen.getAllByRole("option");
    expect(options).toHaveLength(6);
    expect(options.some((opt) => opt.textContent?.includes("max"))).toBe(
      true,
    );
  });
});
