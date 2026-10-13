import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { LeadBadge } from "../org-department-badges";

describe("LeadBadge", () => {
  it("Given a lead with an identity color, When the badge renders, Then only the crown carries that color and the label stays neutral", () => {
    // 身份色是为「色块 + 白字」调的：暗色下 16 个都压暗了，当文字对 bg-secondary
    // 只有 3.1，亮色也有 12 个低于 4.5。图形只要 3:1，所以颜色留在皇冠上。
    render(<LeadBadge color="agent-7" />);

    const label = screen.getByText("LEAD");
    const crown = label.querySelector("svg");

    expect(crown).toHaveClass("text-agent-7");
    expect(label.className).not.toMatch(/(^|\s)text-agent-\d/);
    expect(label).toHaveClass("text-secondary-foreground");
  });
});
