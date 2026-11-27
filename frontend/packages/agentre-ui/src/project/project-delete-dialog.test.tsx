import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { TouchProvider } from "../ui/touch";
import { ProjectDeleteDialog } from "./project-delete-dialog";
import type { ProjectDeletePorts, ProjectWriteOutcome } from "./ports";

/**
 * 删除项目的确认，两端共用那一份（规格 2026-08-22 B 段，决策 8）。
 *
 * 危险确认是**一种形态**而不是一段文案：头部 danger、主按钮 destructive、
 * 后果写在正文不写进标题。四件事各不相同又都容易被想错，所以逐条写。
 *
 * **要求输入完整项目名才放行**：两端向更谨慎的那一端（桌面端）对齐 —— 删除会连
 * 子项目一起删，而组头 ⋮ 上「删除」与「设置」只隔两项，误点代价不对称。
 */

function ports(over: Partial<ProjectDeletePorts> = {}): ProjectDeletePorts {
  return {
    deleteProject: vi.fn(
      async (): Promise<ProjectWriteOutcome> => ({ ok: true }),
    ),
    ...over,
  };
}

function open(
  over: {
    ports?: Partial<ProjectDeletePorts>;
    childCount?: number;
    offlineMachines?: string[];
    memoryCount?: number;
  } = {},
) {
  const p = ports(over.ports);
  const onDeleted = vi.fn();
  const view = render(
    <ProjectDeleteDialog
      open
      onOpenChange={() => {}}
      project={{ id: "p1", name: "Atlas" }}
      childCount={over.childCount ?? 0}
      offlineMachines={over.offlineMachines ?? []}
      memoryCount={over.memoryCount}
      ports={p}
      onDeleted={onDeleted}
    />,
  );
  return { ...view, ports: p, onDeleted };
}

beforeEach(() => vi.clearAllMocks());

describe("名下的记忆", () => {
  it("宿主给了条数就说会删掉几条，子项目的也算在内由宿主汇总", () => {
    open({ memoryCount: 7 });
    expect(screen.getByTestId("delete-project-memories").textContent).toContain(
      "7",
    );
  });

  it("只有一条用单数", () => {
    open({ memoryCount: 1 });
    expect(screen.getByTestId("delete-project-memories").textContent).toMatch(
      /1 memory/,
    );
  });

  it("一条都没有也说一句，不是留空", () => {
    open({ memoryCount: 0 });
    expect(screen.getByTestId("delete-project-memories").textContent).toMatch(
      /no memories/i,
    );
  });

  it("宿主不传（server 今天的用法）就不画这一行", () => {
    open();
    expect(screen.queryByTestId("delete-project-memories")).toBeNull();
  });
});

describe("四件后果逐条写清", () => {
  it("子项目一并删 —— 有几个就说几个", () => {
    open({ childCount: 3 });
    expect(screen.getByTestId("delete-project-children").textContent).toContain(
      "3",
    );
  });

  it("没有子项目时也说一句，不是留空", () => {
    open({ childCount: 0 });
    expect(
      screen.getByTestId("delete-project-children").textContent,
    ).toBeTruthy();
  });

  it("对话一条都不删，机器上的目录一个字节都不动", () => {
    open();
    expect(
      screen.getByTestId("delete-project-sessions").textContent,
    ).toBeTruthy();
    expect(screen.getByTestId("delete-project-files").textContent).toBeTruthy();
  });

  it("此刻离线的机器点名说出来，要等下次上线才跟着删", () => {
    open({ offlineMachines: ["build-01", "office-imac"] });
    const line = screen.getByTestId("delete-project-offline").textContent ?? "";
    expect(line).toContain("build-01");
    expect(line).toContain("office-imac");
  });

  it("都在线时说的是另一句，不是同一句留个空名单", () => {
    open({ offlineMachines: [] });
    const line = screen.getByTestId("delete-project-offline").textContent ?? "";
    expect(line).toBeTruthy();
    expect(line).not.toContain("build-01");
  });
});

describe("打字门槛", () => {
  it("名字没输对之前主按钮一直是禁用的", () => {
    open();
    const submit = screen.getByTestId(
      "delete-project-submit",
    ) as HTMLButtonElement;
    expect(submit.disabled).toBe(true);
    fireEvent.change(screen.getByTestId("delete-project-confirm"), {
      target: { value: "Atla" },
    });
    expect(submit.disabled).toBe(true);
  });

  it("输对了才放行，删完把 id 交回去", async () => {
    const { ports: p, onDeleted } = open();
    fireEvent.change(screen.getByTestId("delete-project-confirm"), {
      target: { value: "Atlas" },
    });
    const submit = screen.getByTestId(
      "delete-project-submit",
    ) as HTMLButtonElement;
    expect(submit.disabled).toBe(false);
    fireEvent.click(submit);
    await waitFor(() => expect(p.deleteProject).toHaveBeenCalledWith("p1"));
    await waitFor(() => expect(onDeleted).toHaveBeenCalledWith("p1"));
  });

  it("首尾空白不算错 —— 复制粘贴项目名常带一个尾空格", () => {
    open();
    fireEvent.change(screen.getByTestId("delete-project-confirm"), {
      target: { value: "  Atlas  " },
    });
    expect(
      (screen.getByTestId("delete-project-submit") as HTMLButtonElement)
        .disabled,
    ).toBe(false);
  });
});

describe("形态与失败", () => {
  it("是 danger 形态：主按钮 destructive", () => {
    open();
    const submit = screen.getByTestId("delete-project-submit");
    expect(submit.className).toContain("destructive");
  });

  it("删不掉时窗不关，错误落在脚部左侧", async () => {
    const { ports: p, onDeleted } = open({
      ports: {
        deleteProject: vi.fn(
          async (): Promise<ProjectWriteOutcome> => ({
            ok: false,
            failure: { kind: "unknown", message: "还有活跃会话，先结束它们" },
          }),
        ),
      },
    });
    fireEvent.change(screen.getByTestId("delete-project-confirm"), {
      target: { value: "Atlas" },
    });
    fireEvent.click(screen.getByTestId("delete-project-submit"));
    await waitFor(() => expect(p.deleteProject).toHaveBeenCalled());
    const footer = await screen.findByTestId("delete-project-footer");
    expect(footer.textContent).toContain("还有活跃会话，先结束它们");
    expect(onDeleted).not.toHaveBeenCalled();
  });
});

describe("F3 触屏档：底部弹层式的纵向按钮", () => {
  function openTouch() {
    return render(
      <TouchProvider>
        <ProjectDeleteDialog
          open
          onOpenChange={() => {}}
          project={{ id: "p1", name: "Atlas" }}
          childCount={0}
          offlineMachines={[]}
          ports={ports()}
          onDeleted={() => {}}
        />
      </TouchProvider>,
    );
  }

  it("删除键在上、取消在下，各占满一行且 44px", () => {
    openTouch();
    const footer = screen.getByTestId("delete-project-footer");
    const buttons = Array.from(footer.querySelectorAll("button"));
    expect(buttons.map((b) => b.textContent)).toEqual([
      "Delete project",
      "Cancel",
    ]);
    for (const b of buttons) {
      expect(b.className).toContain("min-h-11");
      expect(b.className).toContain("w-full");
    }
    expect(buttons[0]).toHaveAttribute("data-testid", "delete-project-submit");
    expect(
      footer.querySelector("[data-slot=dialog-shell-footer]")?.className,
    ).toContain("flex-col");
  });

  it("失败文案仍显示在按钮上方", async () => {
    const p = ports({
      deleteProject: vi.fn(async () => ({
        ok: false as const,
        failure: { kind: "unknown" as const, message: "boom" },
      })),
    });
    render(
      <TouchProvider>
        <ProjectDeleteDialog
          open
          onOpenChange={() => {}}
          project={{ id: "p1", name: "Atlas" }}
          childCount={0}
          offlineMachines={[]}
          ports={p}
          onDeleted={() => {}}
        />
      </TouchProvider>,
    );
    fireEvent.change(screen.getByTestId("delete-project-confirm"), {
      target: { value: "Atlas" },
    });
    fireEvent.click(screen.getByTestId("delete-project-submit"));
    expect(await screen.findByRole("alert")).toHaveTextContent("boom");
  });

  it("不开触屏：按钮仍是同一行的小按钮（桌面不变）", () => {
    open();
    const footer = screen.getByTestId("delete-project-footer");
    expect(
      footer.querySelector("[data-slot=dialog-shell-footer]")?.className,
    ).not.toContain("flex-col");
    for (const b of Array.from(footer.querySelectorAll("button"))) {
      expect(b.className).not.toContain("min-h-11");
    }
  });
});
