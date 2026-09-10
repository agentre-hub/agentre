import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ProjectCreateDialog } from "./project-create-dialog";
import type { ProjectCreatePorts, ProjectCreateOutcome } from "./ports";

/**
 * 新建项目 / 子项目，两端共用那一份（规格 2026-08-22 B 段，决策 9）。
 *
 * **路径不必填**：web 上建项目的人可能一台机器都没在线，挡住他等于把「只有
 * agentred 也能管理」堵在第一步。代价是这样建出来的项目在配好路径之前开不出对话
 * —— 所以表单里当场把这句话说出来，而不是等他开不出对话时才发现。
 *
 * 本机路径与 git 探测是**宿主能力**：桌面端挂，web 不挂。没挂那个 port 就没有那一格。
 */

function ports(over: Partial<ProjectCreatePorts> = {}): ProjectCreatePorts {
  return {
    create: vi.fn(
      async (): Promise<ProjectCreateOutcome> => ({ ok: true, id: "new-1" }),
    ),
    ...over,
  };
}

function open(
  over: { ports?: Partial<ProjectCreatePorts>; parentName?: string } = {},
) {
  const p = ports(over.ports);
  const onCreated = vi.fn();
  const view = render(
    <ProjectCreateDialog
      open
      onOpenChange={() => {}}
      parentOptions={[
        { id: "p1", name: "Atlas" },
        { id: "p2", name: "Platform", depth: 1 },
      ]}
      parentName={over.parentName}
      ports={p}
      onCreated={onCreated}
    />,
  );
  return { ...view, ports: p, onCreated };
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.useRealTimers();
});

describe("父项目", () => {
  it("挑一个父项目，建的时候把它带上", async () => {
    const { ports: p } = open();
    fireEvent.change(screen.getByTestId("project-create-name"), {
      target: { value: "Ledger" },
    });

    // 原生 <select> 在这一步就露馅：option 一直摆在 DOM 里，点了也不改值。
    fireEvent.click(screen.getByTestId("project-create-parent"));
    fireEvent.click(await screen.findByRole("option", { name: "Platform" }));
    fireEvent.click(screen.getByTestId("project-create-submit"));

    await waitFor(() =>
      expect(p.create).toHaveBeenCalledWith({
        name: "Ledger",
        parentId: "p2",
      }),
    );
  });

  it("层级缩进画得出来 —— <option> 里的空格会被折叠掉，什么都看不见", async () => {
    open();
    fireEvent.click(screen.getByTestId("project-create-parent"));
    const nested = await screen.findByRole("option", { name: "Platform" });
    // depth=1 那一条要有真的左缩进，而不是靠字符串里的两个空格。
    expect(
      nested.querySelector("[data-depth='1']") ??
        (nested.getAttribute("data-depth") === "1" ? nested : null),
    ).not.toBeNull();
  });
});

describe("路径不必填", () => {
  it("只填名字就建得出来，递下去的 draft 里没有 localPath", async () => {
    const { ports: p, onCreated } = open();
    fireEvent.change(screen.getByTestId("project-create-name"), {
      target: { value: "Atlas" },
    });
    fireEvent.click(screen.getByTestId("project-create-submit"));
    await waitFor(() =>
      expect(p.create).toHaveBeenCalledWith({ name: "Atlas" }),
    );
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith("new-1"));
  });

  it("代价当场说出来 —— 不是等他开不出对话时才发现", () => {
    open();
    expect(screen.getByTestId("project-create-path-note")).toBeTruthy();
  });

  it("没填的键不翻成空串送下去（指针语义）", async () => {
    const { ports: p } = open();
    fireEvent.change(screen.getByTestId("project-create-name"), {
      target: { value: "  Atlas  " },
    });
    fireEvent.click(screen.getByTestId("project-create-submit"));
    await waitFor(() => expect(p.create).toHaveBeenCalled());
    const draft = (p.create as ReturnType<typeof vi.fn>).mock.calls[0][0];
    expect(draft).toEqual({ name: "Atlas" });
    expect("description" in draft).toBe(false);
    expect("color" in draft).toBe(false);
  });

  it("名字为空时主按钮不放行 —— 它是唯一必填的一格", () => {
    open();
    expect(
      (screen.getByTestId("project-create-submit") as HTMLButtonElement)
        .disabled,
    ).toBe(true);
  });
});

describe("宿主能力：本机路径与 git 探测", () => {
  it("宿主没挂挑目录的 port 时，本机路径那一格根本不出现", () => {
    open();
    expect(screen.queryByTestId("project-create-path")).toBeNull();
  });

  it("挂了就多一格 + 「浏览…」，挑完填进去并当默认名", async () => {
    const { ports: p } = open({
      ports: { pickLocalDirectory: vi.fn(async () => "/Users/w/code/atlas") },
    });
    fireEvent.click(screen.getByTestId("project-create-browse"));
    await waitFor(() =>
      expect(
        (screen.getByTestId("project-create-path") as HTMLInputElement).value,
      ).toBe("/Users/w/code/atlas"),
    );
    // 没填名字时把目录名当默认名 —— 十有八九就是它。
    expect(
      (screen.getByTestId("project-create-name") as HTMLInputElement).value,
    ).toBe("atlas");
    fireEvent.click(screen.getByTestId("project-create-submit"));
    await waitFor(() =>
      expect(p.create).toHaveBeenCalledWith({
        name: "atlas",
        localPath: "/Users/w/code/atlas",
      }),
    );
  });

  it("挑目录被取消时表单纹丝不动", async () => {
    open({ ports: { pickLocalDirectory: vi.fn(async () => null) } });
    fireEvent.click(screen.getByTestId("project-create-browse"));
    await waitFor(() =>
      expect(
        (screen.getByTestId("project-create-path") as HTMLInputElement).value,
      ).toBe(""),
    );
    expect(
      (screen.getByTestId("project-create-name") as HTMLInputElement).value,
    ).toBe("");
  });

  it("宿主没挂 git 探测时，那枚标记与那次探测都不存在", async () => {
    open({
      ports: { pickLocalDirectory: vi.fn(async () => "/Users/w/code/atlas") },
    });
    fireEvent.click(screen.getByTestId("project-create-browse"));
    await waitFor(() =>
      expect(
        (screen.getByTestId("project-create-path") as HTMLInputElement).value,
      ).toBe("/Users/w/code/atlas"),
    );
    expect(screen.queryByTestId("project-create-git")).toBeNull();
  });

  it("挂了就在挑完之后就地标出这个目录是不是 git 仓库", async () => {
    const probeGitRepo = vi.fn(async () => ({
      isGitRepo: true,
      branch: "main",
      origin: "git@github.com:a/b.git",
    }));
    open({
      ports: {
        pickLocalDirectory: vi.fn(async () => "/Users/w/code/atlas"),
        probeGitRepo,
      },
    });
    fireEvent.click(screen.getByTestId("project-create-browse"));
    await waitFor(() =>
      expect(probeGitRepo).toHaveBeenCalledWith("/Users/w/code/atlas"),
    );
    const badge = await screen.findByTestId("project-create-git");
    expect(badge.textContent).toContain("main");
  });

  it("探测说不是仓库时也说出来 —— 留白会让人以为还在探", async () => {
    open({
      ports: {
        pickLocalDirectory: vi.fn(async () => "/tmp/plain"),
        probeGitRepo: vi.fn(async () => ({ isGitRepo: false })),
      },
    });
    fireEvent.click(screen.getByTestId("project-create-browse"));
    const note = await screen.findByTestId("project-create-git");
    expect(note.textContent).toBeTruthy();
  });
});

describe("失败", () => {
  it("建不成时窗不关、内容不清，错误落在脚部左侧", async () => {
    const { ports: p, onCreated } = open({
      ports: {
        create: vi.fn(
          async (): Promise<ProjectCreateOutcome> => ({
            ok: false,
            failure: {
              kind: "unknown",
              message: "同级下已经有一个叫 Atlas 的项目",
            },
          }),
        ),
      },
    });
    fireEvent.change(screen.getByTestId("project-create-name"), {
      target: { value: "Atlas" },
    });
    fireEvent.click(screen.getByTestId("project-create-submit"));
    await waitFor(() => expect(p.create).toHaveBeenCalled());
    const footer = await screen.findByTestId("project-create-footer");
    expect(footer.textContent).toContain("同级下已经有一个叫 Atlas 的项目");
    expect(
      (screen.getByTestId("project-create-name") as HTMLInputElement).value,
    ).toBe("Atlas");
    expect(onCreated).not.toHaveBeenCalled();
  });
});

describe("父项目", () => {
  it("「在 X 下新建」时头部就说清挂在哪儿", () => {
    open({ parentName: "Platform" });
    expect(document.body.textContent).toContain("Platform");
  });
});
