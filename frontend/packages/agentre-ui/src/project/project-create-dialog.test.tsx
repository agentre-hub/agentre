import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ProjectCreateDialog } from "./project-create-dialog";
import type {
  PickerMachine,
  ProjectCreateMachinesPort,
  ProjectCreateOutcome,
  ProjectCreatePorts,
} from "./ports";

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

  /**
   * 这句提示按**宿主有没有路径这一格**分两版。
   *
   * 从前只有一句「现在不用填路径」，而 web 宿主根本不挂 `pickLocalDirectory`：
   * 那句话是在劝他别填一个不存在的输入框，且每次新建都出现。两版说的是同一件代价，
   * 差别只在下一步该往哪走。
   */
  it("挂了本机路径那一格时，说的是「不填也能建」", () => {
    open({ ports: { pickLocalDirectory: vi.fn(async () => null) } });
    expect(
      screen.getByTestId("project-create-path-note").textContent,
    ).toContain("You can create it without a path");
  });

  it("没挂那一格时，说的是建好后去哪儿配 —— 不提一个不存在的输入框", () => {
    open();
    const note = screen.getByTestId("project-create-path-note").textContent;
    expect(note).toContain("project settings");
    expect(note).not.toContain("You can create it without a path");
  });

  /**
   * 提示**不预告角标**。角标自己可点、带 aria，教在这里等于让他记一个还没见过的
   * 东西；而 zh-CN 那版还把它的名字记错了（写「未配路径」，界面上是「未配置」）。
   */
  it("不在这里预告组头上的角标", () => {
    open();
    expect(
      screen.getByTestId("project-create-path-note").textContent,
    ).not.toContain("badge");
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

/**
 * 在别的机器上配路径，是 web 宿主那一端的能力（桌面端不挂，它有自己的本机那一格）。
 *
 * 路径**进不了建项目那次请求** —— 它不是项目的字段，按「项目 × 机器」另存一处，
 * 服务端那一族的注释写死了「这里没有 path，也不会有」。所以必然是 create → setPath
 * 两次写，而两次之间那个「项目已经建出来了」的中间态必须当场交代清楚。
 */
describe("宿主能力：在别的机器上配路径", () => {
  const boxes: PickerMachine[] = [
    { id: "fp-1", name: "build-box", kind: "agentred", online: true },
    { id: "fp-2", name: "laptop", kind: "desktop", online: false },
  ];

  function machinesPort(
    over: Partial<ProjectCreateMachinesPort> = {},
  ): ProjectCreateMachinesPort {
    return {
      list: vi.fn(async () => boxes),
      fs: {
        listDir: vi.fn(async () => ({
          ok: true as const,
          result: {
            path: "/srv",
            entries: [{ name: "atlas", isDir: true }],
            truncated: false,
          },
        })),
        mkdir: vi.fn(async () => ({ ok: true as const, result: undefined })),
      },
      setPath: vi.fn(async () => ({ ok: true as const })),
      ...over,
    };
  }

  /** 挑一台机器与目录，走完那个嵌套的目录选择器。 */
  async function pick() {
    fireEvent.click(await screen.findByTestId("project-create-machine-pick"));
    fireEvent.click(
      await screen.findByRole("button", { name: "Choose this folder" }),
    );
  }

  it("宿主没挂这个 port 时，那一格根本不出现", () => {
    open();
    expect(screen.queryByTestId("project-create-machine-pick")).toBeNull();
  });

  it("账号里一台机器都没有时也不出现 —— 一个点开是空的入口比没有更糟", async () => {
    open({
      ports: { machines: machinesPort({ list: vi.fn(async () => []) }) },
    });
    await waitFor(() =>
      expect(screen.queryByTestId("project-create-machine-pick")).toBeNull(),
    );
  });

  it("挑完在弹窗里留一行只读摘要，说清是哪台机器上的哪条路径", async () => {
    open({ ports: { machines: machinesPort() } });
    await pick();
    const row = await screen.findByTestId("project-create-machine");
    expect(row.textContent).toContain("build-box");
    expect(row.textContent).toContain("/srv");
  });

  it("挑了机器与路径之后，那句「配好之前开不出对话」就不再出现", async () => {
    open({ ports: { machines: machinesPort() } });
    await pick();
    await waitFor(() =>
      expect(screen.queryByTestId("project-create-path-note")).toBeNull(),
    );
  });

  it("建成之后紧跟一次写路径，写的是刚建出来的那个项目", async () => {
    const m = machinesPort();
    const { onCreated } = open({ ports: { machines: m } });
    fireEvent.change(screen.getByTestId("project-create-name"), {
      target: { value: "Atlas" },
    });
    await pick();
    fireEvent.click(screen.getByTestId("project-create-submit"));

    await waitFor(() => expect(m.setPath).toHaveBeenCalledTimes(1));
    expect(m.setPath).toHaveBeenCalledWith(
      "new-1",
      expect.objectContaining({ id: "fp-1" }),
      "/srv",
    );
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith("new-1"));
  });

  it("路径进不了建项目那次请求 —— draft 里没有它", async () => {
    const { ports: p } = open({ ports: { machines: machinesPort() } });
    fireEvent.change(screen.getByTestId("project-create-name"), {
      target: { value: "Atlas" },
    });
    await pick();
    fireEvent.click(screen.getByTestId("project-create-submit"));
    await waitFor(() =>
      expect(p.create).toHaveBeenCalledWith({ name: "Atlas" }),
    );
  });

  describe("建成了、路径没写成", () => {
    function openFailing() {
      const m = machinesPort({
        setPath: vi
          .fn()
          .mockResolvedValueOnce({
            ok: false,
            failure: { kind: "disconnected", message: "" },
          })
          .mockResolvedValue({ ok: true }),
      });
      const view = open({ ports: { machines: m } });
      return { ...view, machines: m };
    }

    async function createAndFail(view: ReturnType<typeof openFailing>) {
      fireEvent.change(screen.getByTestId("project-create-name"), {
        target: { value: "Atlas" },
      });
      await pick();
      fireEvent.click(screen.getByTestId("project-create-submit"));
      await waitFor(() => expect(view.machines.setPath).toHaveBeenCalled());
    }

    it("不关窗，就地说清哪一半成了、哪一半没成", async () => {
      const view = openFailing();
      await createAndFail(view);
      const half = await screen.findByTestId("project-create-half-done");
      expect(half.textContent).toContain("Atlas");
      expect(half.textContent).toContain("build-box");
      // 分得出类的失败用包写好的那一句，不是一句泛泛的「保存失败」。
      expect(half.textContent).toContain("Lost the connection");
    });

    it("重试只重发写路径那一步，不会建出第二个项目", async () => {
      const view = openFailing();
      await createAndFail(view);
      fireEvent.click(await screen.findByTestId("project-create-retry-path"));
      await waitFor(() =>
        expect(view.machines.setPath).toHaveBeenCalledTimes(2),
      );
      expect(view.ports.create).toHaveBeenCalledTimes(1);
      expect(view.machines.setPath).toHaveBeenLastCalledWith(
        "new-1",
        expect.objectContaining({ id: "fp-1" }),
        "/srv",
      );
      await waitFor(() => expect(view.onCreated).toHaveBeenCalledWith("new-1"));
    });

    it("选「先这样」也要把「项目已经建出来了」告诉宿主 —— 否则索引里凭空少一个", async () => {
      const view = openFailing();
      await createAndFail(view);
      fireEvent.click(await screen.findByTestId("project-create-keep-anyway"));
      await waitFor(() => expect(view.onCreated).toHaveBeenCalledWith("new-1"));
    });

    it("直接关窗同样要告诉宿主，且只告诉一次", async () => {
      const view = openFailing();
      await createAndFail(view);
      fireEvent.click(screen.getByRole("button", { name: "Close" }));
      await waitFor(() => expect(view.onCreated).toHaveBeenCalledWith("new-1"));
      expect(view.onCreated).toHaveBeenCalledTimes(1);
    });
  });
});
