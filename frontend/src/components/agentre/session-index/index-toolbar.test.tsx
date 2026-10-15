import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import "@/i18n";

import { IndexToolbar } from "./index-toolbar";

describe("IndexToolbar search field", () => {
  // 规格「控件 · 字段」：搜索框静止态无边、相对前景色 5% 填充。会话列表落在外框上，
  // 这颗搜索框若换回某个静止表面色（bg-background），就成了外框上一块白底。
  it("Given the session list on the frame, When the toolbar renders, Then its search box keeps the shared field fill", () => {
    render(
      <IndexToolbar
        query=""
        setQuery={vi.fn()}
        axis="project"
        setAxis={vi.fn()}
        statusFilter={null}
        setStatusFilter={vi.fn()}
        unreadCount={0}
        onOpenCommandPalette={vi.fn()}
        onCreateProject={vi.fn()}
        onNewAgent={vi.fn()}
      />,
    );

    const search = screen.getByRole("textbox", {
      name: "Search sessions, projects or agents",
    });
    expect(search).toHaveClass("bg-field-fill");
    expect(search).not.toHaveClass("bg-background");
  });
});
