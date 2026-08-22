import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ProjectSettingsDialog } from "./project-settings-dialog";
import type {
  ProjectMachineView,
  ProjectSettingsPorts,
  ProjectSettingsView,
  ProjectWriteOutcome,
} from "./ports";

/**
 * 项目设置，两端共用那一份（规格 2026-08-22 B 段，决策 2/3/4/7/14）。
 *
 * 合之前两端是两个产品：桌面端是 `max-w-[460px]` 的四标签页弹窗（基本 / 成员 /
 * 位置 / 危险），本仓是 `DialogShell size="md"` 的一屏三节。合后取一屏三节 +
 * 即时保存 —— 桌面端那一个弹窗里此前同时存在两种保存语义（基本页显式「保存」+
 * dirty 判定，成员与位置的增删立即写），用户按下一个按钮之前得先判断这一节属于
 * 哪一种。
 *
 * 后端形状是真正的宿主差异，由 ports 吃掉：桌面端是数字 id 经 wailsjs，本仓是
 * 字符串 syncId 经 REST。
 */

const MACHINES: ProjectMachineView[] = [
  // 宿主自己那一台：它写自己、不经中继，所以离线也改得动（决策 4）。
  {
    id: "self",
    name: "wangyz-mbp",
    kind: "desktop",
    online: false,
    isSelf: true,
    path: "/Users/w/code/atlas",
    removable: true,
  },
  // 别人的桌面端：路径只住在它的上报组，要经中继喊它自己写 —— 离线就改不动。
  {
    id: "peer",
    name: "office-imac",
    kind: "desktop",
    online: false,
    writeNeedsOnline: true,
    path: "",
    removable: false,
  },
  // agentred：路径是账号级同步对象，服务端直写，离线也配得了。
  {
    id: "farm",
    name: "build-01",
    kind: "agentred",
    online: true,
    path: "/srv/work/atlas",
    removable: true,
  },
];

const PROJECT: ProjectSettingsView = {
  id: "p1",
  name: "Atlas",
  description: "地图那条线",
  color: "agent-3",
  parentId: "",
  members: [
    { id: "m1", name: "Reviewer" },
    { id: "m2", name: "Scout", inherited: true, inheritedFrom: "Platform" },
  ],
  candidates: [{ id: "a9", name: "Builder" }],
};

const OK: ProjectWriteOutcome = { ok: true };

function ports(over: Partial<ProjectSettingsPorts> = {}): ProjectSettingsPorts {
  return {
    updateFields: vi.fn(async () => OK),
    addMember: vi.fn(async () => OK),
    removeMember: vi.fn(async () => OK),
    listMachines: vi.fn(async () => MACHINES),
    setMachinePath: vi.fn(async () => OK),
    clearMachinePath: vi.fn(async () => OK),
    fs: {
      listDir: vi.fn(async (_m: string, path: string) => ({
        ok: true as const,
        result: { path: path || "/srv", entries: [], truncated: false },
      })),
      mkdir: vi.fn(async () => ({ ok: true as const, result: undefined })),
    },
    ...over,
  };
}

type Over = {
  ports?: Partial<ProjectSettingsPorts>;
  project?: ProjectSettingsView;
  focus?: "members" | "paths";
};

function open(over: Over = {}) {
  const p = ports(over.ports);
  const onChanged = vi.fn();
  const view = render(
    <ProjectSettingsDialog
      open
      onOpenChange={() => {}}
      project={over.project ?? PROJECT}
      parentOptions={[
        { id: "p1", name: "Atlas" },
        { id: "p2", name: "Platform" },
      ]}
      ports={p}
      focus={over.focus}
      onChanged={onChanged}
    />,
  );
  return { ...view, ports: p, onChanged };
}

beforeEach(() => {
  vi.clearAllMocks();
  // jsdom 不实现 scrollIntoView —— 直落那一节靠它，得先有这个桩。
  Element.prototype.scrollIntoView = vi.fn();
});

describe("一屏三节", () => {
  it("依次是基本 / 成员 / 机器与路径，没有标签页", async () => {
    open();
    await screen.findByTestId("project-section-basic");
    const body = document.querySelector("[data-slot='dialog-shell-body']")!;
    const sections = Array.from(
      body.querySelectorAll("[data-testid^='project-section-']"),
    ).map((n) => n.getAttribute("data-testid"));
    expect(sections).toEqual([
      "project-section-basic",
      "project-section-members",
      "project-section-paths",
    ]);
    expect(screen.queryByRole("tab")).toBeNull();
  });

  it("脚部只有「完成」，没有「保存」", async () => {
    open();
    const footer = await screen.findByTestId("project-settings-footer");
    expect(
      footer.querySelector("[data-testid='project-settings-done']"),
    ).not.toBeNull();
    expect(footer.textContent).not.toMatch(/save|保存/i);
  });

  it("焦点直落到指定的那一节，且只有那一节描边", async () => {
    open({ focus: "paths" });
    const paths = await screen.findByTestId("project-section-paths");
    expect(paths.dataset.focused).toBe("true");
    expect(
      screen.getByTestId("project-section-members").dataset.focused,
    ).toBeUndefined();
    // 用 ref 回调而不是挂载后的一次性查找：这一节住在 Portal 里，父组件的 effect
    // 跑到时节点还没挂上，`target?.scrollIntoView()` 会静默地什么都不做。
    expect(Element.prototype.scrollIntoView).toHaveBeenCalled();
  });
});

describe("即时保存", () => {
  it("值变了才发一次写，头部走一遍保存态", async () => {
    const { ports: p } = open();
    const name = await screen.findByTestId("project-settings-name");
    fireEvent.change(name, { target: { value: "Atlas II" } });
    fireEvent.blur(name);
    await waitFor(() =>
      expect(p.updateFields).toHaveBeenCalledWith("p1", { name: "Atlas II" }),
    );
    expect(await screen.findByText("Saved")).toBeTruthy();
  });

  it("值没变就不发 —— 一次 blur 发一次会把即时保存变成噪音", async () => {
    const { ports: p } = open();
    const name = await screen.findByTestId("project-settings-name");
    fireEvent.blur(name);
    await waitFor(() => expect(screen.queryByText("Saving...")).toBeNull());
    expect(p.updateFields).not.toHaveBeenCalled();
  });

  it("写失败时用户填的内容留在原地，业务码原样透出", async () => {
    const { ports: p } = open({
      ports: {
        addMember: vi.fn(
          async (): Promise<ProjectWriteOutcome> => ({
            ok: false,
            failure: {
              kind: "unknown",
              message: "该 Agent 已经是这个项目的成员",
            },
          }),
        ),
      },
    });
    const name = await screen.findByTestId("project-settings-name");
    fireEvent.change(name, { target: { value: "改了一半" } });
    fireEvent.click(screen.getByTestId("project-member-add-a9"));
    await waitFor(() => expect(p.addMember).toHaveBeenCalled());
    // 不被折成一句「保存失败」。
    expect(
      await screen.findByText("该 Agent 已经是这个项目的成员"),
    ).toBeTruthy();
    expect((name as HTMLInputElement).value).toBe("改了一半");
  });
});

describe("机器与路径", () => {
  it("清单在读、读失败、读到空是三个样子，互不折叠", async () => {
    const { unmount } = open({
      ports: {
        listMachines: vi.fn(() => new Promise<ProjectMachineView[]>(() => {})),
      },
    });
    expect(await screen.findByTestId("project-paths-loading")).toBeTruthy();
    unmount();

    const failed = open({
      ports: {
        listMachines: vi.fn(async () => Promise.reject(new Error("boom"))),
      },
    });
    expect(await screen.findByTestId("project-paths-failed")).toBeTruthy();
    // 读失败不是「这个账号没有机器」：说成空态等于让人去添加一台他其实已经有的机器。
    expect(screen.queryByTestId("project-paths-empty")).toBeNull();
    failed.unmount();

    open({ ports: { listMachines: vi.fn(async () => []) } });
    expect(await screen.findByTestId("project-paths-empty")).toBeTruthy();
    expect(screen.queryByTestId("project-paths-failed")).toBeNull();
  });

  it("离线的机器留在表里 —— 隐藏会让人以为那台机器没配对", async () => {
    open();
    expect(await screen.findByTestId("project-path-row-peer")).toBeTruthy();
    expect(screen.getByTestId("project-path-row-farm")).toBeTruthy();
  });

  it("本机那一行离线照样改得动，别人的桌面端离线就改不动", async () => {
    open();
    await screen.findByTestId("project-path-row-self");
    // 本机是宿主写自己，不经中继，在线与否与它无关。
    expect(
      (screen.getByTestId("project-path-input-self") as HTMLInputElement)
        .disabled,
    ).toBe(false);
    // 别人的桌面端要经中继喊它自己写，离线就没人接。
    expect(
      (screen.getByTestId("project-path-input-peer") as HTMLInputElement)
        .disabled,
    ).toBe(true);
    // agentred 的路径由服务端直写，离线也配得了。
    expect(
      (screen.getByTestId("project-path-input-farm") as HTMLInputElement)
        .disabled,
    ).toBe(false);
  });

  it("本机那一行带「本机」角标", async () => {
    open();
    const row = await screen.findByTestId("project-path-row-self");
    expect(row.textContent).toContain("This machine");
    expect(
      screen.getByTestId("project-path-row-farm").textContent,
    ).not.toContain("This machine");
  });

  it("宿主说不出本机叫什么时，「本机」就是它的名字，不再另挂一枚同字角标", async () => {
    open({
      ports: {
        listMachines: vi.fn(async () => [
          { ...MACHINES[0], name: "" },
          MACHINES[2],
        ]),
      },
    });
    const row = await screen.findByTestId("project-path-row-self");
    // 同一句话说两遍不是更清楚，是更吵。
    expect(row.textContent?.match(/This machine/g)?.length).toBe(1);
  });

  it("离线的机器答不出目录里有什么，所以「选择…」停掉", async () => {
    open();
    await screen.findByTestId("project-path-row-farm");
    expect(
      (screen.getByTestId("project-path-choose-farm") as HTMLButtonElement)
        .disabled,
    ).toBe(false);
    expect(
      (screen.getByTestId("project-path-choose-peer") as HTMLButtonElement)
        .disabled,
    ).toBe(true);
  });

  it("本机的「选择…」走宿主的原生对话框，不开自绘面板", async () => {
    const pickLocalDirectory = vi.fn(async () => "/Users/w/code/next");
    const { ports: p } = open({ ports: { pickLocalDirectory } });
    await screen.findByTestId("project-path-row-self");
    fireEvent.click(screen.getByTestId("project-path-choose-self"));
    await waitFor(() => expect(pickLocalDirectory).toHaveBeenCalled());
    expect(screen.queryByTestId("directory-picker")).toBeNull();
    await waitFor(() =>
      expect(p.setMachinePath).toHaveBeenCalledWith(
        "p1",
        expect.objectContaining({ id: "self" }),
        "/Users/w/code/next",
      ),
    );
  });

  it("改一行的路径：blur 提交，值没变不发", async () => {
    const { ports: p } = open();
    const input = (await screen.findByTestId(
      "project-path-input-farm",
    )) as HTMLInputElement;
    fireEvent.blur(input);
    expect(p.setMachinePath).not.toHaveBeenCalled();
    fireEvent.change(input, { target: { value: "/srv/work/atlas-2" } });
    fireEvent.blur(input);
    await waitFor(() =>
      expect(p.setMachinePath).toHaveBeenCalledWith(
        "p1",
        expect.objectContaining({ id: "farm" }),
        "/srv/work/atlas-2",
      ),
    );
  });

  it("没配路径的行在路径处写「未配置」", async () => {
    open();
    const input = (await screen.findByTestId(
      "project-path-input-peer",
    )) as HTMLInputElement;
    expect(input.value).toBe("");
    expect(input.placeholder).toBe("No path yet");
  });

  it("能移除的行才画移除", async () => {
    open();
    await screen.findByTestId("project-path-row-self");
    expect(screen.queryByTestId("project-path-remove-peer")).toBeNull();
    expect(screen.getByTestId("project-path-remove-farm")).toBeTruthy();
  });
});

describe("中继写失败按错误码分四类", () => {
  const cases: [string, string][] = [
    ["notSynced", "That machine has not synced this project yet"],
    ["pathNotFound", "no such directory"],
    ["invalidPath", "cannot be used"],
    ["disconnected", "Lost the connection"],
  ];

  for (const [kind, fragment] of cases) {
    it(`${kind} 有它自己的一句 —— 折成同一句「保存失败」用户就得自己猜`, async () => {
      open({
        ports: {
          setMachinePath: vi.fn(
            async (): Promise<ProjectWriteOutcome> => ({
              ok: false,
              // 宿主分好类交进来；那一侧的 message 是 Go 文本，对用户没用。
              failure: { kind: kind as never, message: "relay: rpc error" },
            }),
          ),
        },
      });
      const input = (await screen.findByTestId(
        "project-path-input-farm",
      )) as HTMLInputElement;
      fireEvent.change(input, { target: { value: "/nope" } });
      fireEvent.blur(input);
      const footer = await screen.findByTestId("project-settings-footer");
      await waitFor(() => expect(footer.textContent).toContain(fragment));
      expect(footer.textContent).not.toContain("relay: rpc error");
    });
  }
});

describe("宿主能力", () => {
  it("图标那一格由宿主画，写仍然走包里同一条即时保存", async () => {
    const p = ports();
    render(
      <ProjectSettingsDialog
        open
        onOpenChange={() => {}}
        project={{ ...PROJECT, icon: "folder" }}
        parentOptions={[]}
        ports={p}
        onChanged={vi.fn()}
        // 宿主只管长相与可选项（那张 icon key → 图标的注册表是它的）；写归包。
        iconField={({ value, onPick }) => (
          <button
            type="button"
            data-testid="host-icon-field"
            onClick={() => onPick("rocket")}
          >
            {value}
          </button>
        )}
      />,
    );
    const field = await screen.findByTestId("host-icon-field");
    expect(field.textContent).toBe("folder");
    fireEvent.click(field);
    await waitFor(() =>
      expect(p.updateFields).toHaveBeenCalledWith("p1", { icon: "rocket" }),
    );
  });

  it("宿主给不出父项目候选时整格不画 —— 画一个按了没反应的下拉更糟", async () => {
    render(
      <ProjectSettingsDialog
        open
        onOpenChange={() => {}}
        project={PROJECT}
        parentOptions={[{ id: "p1", name: "Atlas" }]}
        ports={ports()}
        onChanged={vi.fn()}
      />,
    );
    await screen.findByTestId("project-section-basic");
    // 候选里只剩它自己 = 没有可选的父项目。
    expect(screen.queryByTestId("project-settings-parent")).toBeNull();
  });
});

describe("成员", () => {
  it("继承来的成员带角标且不给移除 —— 它的出处不在这个项目", async () => {
    open();
    await screen.findByTestId("project-section-members");
    expect(screen.getByTestId("project-member-row-m2").textContent).toContain(
      "Inherited",
    );
    expect(screen.queryByTestId("project-member-remove-m2")).toBeNull();
    expect(screen.getByTestId("project-member-remove-m1")).toBeTruthy();
  });

  it("移除成员是即时写，与字段同一种语义", async () => {
    const { ports: p } = open();
    await screen.findByTestId("project-section-members");
    fireEvent.click(screen.getByTestId("project-member-remove-m1"));
    await waitFor(() =>
      expect(p.removeMember).toHaveBeenCalledWith(
        "p1",
        expect.objectContaining({ id: "m1" }),
      ),
    );
  });

  it("不能加的候选留在列表里并说明原因，不是静默消失", async () => {
    open({
      project: {
        ...PROJECT,
        candidates: [
          { id: "a9", name: "Builder" },
          {
            id: "a8",
            name: "Remote",
            disabled: true,
            disabledReason: "先给这台机器配路径",
          },
        ],
      },
    });
    await screen.findByTestId("project-section-members");
    expect(
      (screen.getByTestId("project-member-add-a8") as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    expect(screen.getByTestId("project-member-add-a8").textContent).toContain(
      "先给这台机器配路径",
    );
  });
});
