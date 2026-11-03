import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { TaskExecPills } from "./exec-pills";

// 任务中的 Agent 选择也显示完整身份，不能在这里退回只有名字与颜色的副本。
describe("task agent identity", () => {
  it.each([
    [
      "preset:agent-architect",
      "data:image/png;base64,AA",
      "data:image/png;base64,AA",
    ],
    ["preset:agent-architect", "", "agent-architect.webp"],
    ["rocket", "", "icon"],
    ["preset:future", "", "letter"],
  ])(
    "Given key %s and upload %s When the task agent picker renders Then trigger and options show %s",
    (avatarIcon, avatarDataUrl, expected) => {
      render(
        <TaskExecPills
          agentId={7}
          projectId={null}
          backendId={null}
          agentOptions={[
            {
              id: 7,
              name: "Reviewer",
              color: "agent-3",
              avatarIcon,
              avatarDataUrl,
            },
          ]}
          onAgentChange={vi.fn()}
        />,
      );
      const assertMark = (container: HTMLElement) => {
        const mark = within(container).getByRole("img", { name: "Reviewer" });
        if (expected === "icon") {
          expect(mark.querySelector("svg")).toHaveClass("lucide-rocket");
        } else if (expected === "letter") {
          expect(mark).toHaveTextContent(/^R$/);
          expect(mark.querySelector("img, svg")).toBeNull();
        } else {
          expect(mark.querySelector("img")).toHaveAttribute(
            "src",
            expect.stringContaining(expected),
          );
        }
      };
      assertMark(screen.getByTestId("task-pill-agent"));
      fireEvent.pointerDown(screen.getByTestId("task-pill-agent"), {
        button: 0,
        ctrlKey: false,
        pointerType: "mouse",
      });
      assertMark(screen.getByTestId("task-agent-7"));
    },
  );
});
