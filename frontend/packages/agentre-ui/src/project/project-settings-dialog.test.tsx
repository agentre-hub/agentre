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
  // 它没有路径，之所以还直接列在表里，是因为这个项目有成员在它上面（`hasMember`）
  // —— 「机器与路径」那一节回答的是「这个项目在哪」。
  {
    id: "peer",
    name: "office-imac",
    kind: "desktop",
    online: false,
    writeNeedsOnline: true,
    path: "",
    removable: false,
    hasMember: true,
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
  it("依次是身份 / 成员 / 机器与路径，没有标签页", async () => {
    open();
    await screen.findByTestId("project-section-identity");
    const body = document.querySelector("[data-slot='dialog-shell-body']")!;
    const sections = Array.from(
      body.querySelectorAll("[data-testid^='project-section-']"),
    ).map((n) => n.getAttribute("data-testid"));
    expect(sections).toEqual([
      "project-section-identity",
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
    fireEvent.click(screen.getByTestId("project-member-add-open"));
    fireEvent.click(await screen.findByTestId("project-member-add-a9"));
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
  /**
   * 图标与颜色在同一个字形弹层里，写走的是与字段同一条即时保存。宿主什么都不用给
   * ——词表住在包里，两端同一份。
   */
  it("图标与颜色都在字形弹层里，写走同一条即时保存", async () => {
    const { ports: p } = open();
    fireEvent.click(await screen.findByTestId("project-glyph-trigger"));
    fireEvent.click(await screen.findByTestId("project-glyph-color-agent-7"));
    await waitFor(() =>
      expect(p.updateFields).toHaveBeenCalledWith("p1", { color: "agent-7" }),
    );

    // 挑颜色不关弹层：一次打开里可能既要换色也要换图标。
    fireEvent.click(await screen.findByTestId("project-glyph-icon-rocket"));
    await waitFor(() =>
      expect(p.updateFields).toHaveBeenCalledWith("p1", { icon: "rocket" }),
    );
  });

  it("父项目走的是共享包那颗 Select：点开挑一个，即时保存", async () => {
    const { ports: p } = open();
    await screen.findByTestId("project-section-identity");

    // 原生 <select> 在这一步就露馅：它的 option 一直摆在 DOM 里，点了也不改值。
    fireEvent.click(screen.getByTestId("project-settings-parent"));
    fireEvent.click(await screen.findByRole("option", { name: "Platform" }));

    await waitFor(() =>
      expect(p.updateFields).toHaveBeenCalledWith("p1", { parentId: "p2" }),
    );
  });

  it("父项目的候选里没有它自己 —— 指向自己会造出一个走不完的环", async () => {
    open();
    await screen.findByTestId("project-section-identity");

    fireEvent.click(screen.getByTestId("project-settings-parent"));
    const options = (await screen.findAllByRole("option")).map((o) =>
      o.textContent?.trim(),
    );
    expect(options).not.toContain("Atlas");
    expect(options).toContain("Platform");
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
    await screen.findByTestId("project-section-identity");
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
    fireEvent.click(screen.getByTestId("project-member-add-open"));
    const blocked = await screen.findByTestId("project-member-add-a8");
    expect((blocked as HTMLButtonElement).disabled).toBe(true);
    // 原因单独一行，不塞进那一行里 —— 塞进去会把每一条候选撑成不同的宽度。
    expect(blocked.closest("li")!.textContent).toContain("先给这台机器配路径");
  });
});

/**
 * 2026-08-27 改版：这一节回答的是**「这个项目在哪」**，不是「账号里有哪些机器」。
 *
 * 合之前它把 `listMachines` 返回的每一台都摊开 —— 桌面端那一侧返回的正是账号里配对
 * 过的每一台 agentred，跟这个项目、跟成员都无关。配了八台，一个刚建的项目就画九行、
 * 其中八行是空的。
 */
describe("机器口径：只列相关的", () => {
  const SPARE: ProjectMachineView = {
    id: "spare",
    name: "ci-runner",
    kind: "agentred",
    online: true,
    path: "",
    removable: false,
  };

  it("没路径、没成员、又不是本机的机器不直接列出来", async () => {
    open({ ports: { listMachines: vi.fn(async () => [...MACHINES, SPARE]) } });
    await screen.findByTestId("project-path-row-self");
    expect(screen.queryByTestId("project-path-row-spare")).toBeNull();
    // 但它没有消失 —— 收在那颗「＋」后面。
    expect(screen.getByTestId("project-paths-add-open")).toBeTruthy();
  });

  it("从「＋」里挑出来之后就地出现，等着填路径", async () => {
    open({ ports: { listMachines: vi.fn(async () => [...MACHINES, SPARE]) } });
    fireEvent.click(await screen.findByTestId("project-paths-add-open"));
    fireEvent.click(await screen.findByTestId("project-paths-add-spare"));
    // 挑完那台还没有路径；不留住它，用户挑完一台机器版面上什么都没发生。
    expect(await screen.findByTestId("project-path-row-spare")).toBeTruthy();
  });

  /**
   * agentre-server 上没有「本机」那一行：一个还没配过路径的项目一台都不相关。照收不误
   * 的话，用户点进来看到的是一节空白加一颗「＋」—— 而他正是来配路径的。
   */
  it("一台都不相关时把全集摊开 —— 那颗「＋」是收噪音的，不是清空这一节的", async () => {
    open({
      ports: {
        listMachines: vi.fn(async () => [
          { ...SPARE },
          { ...SPARE, id: "spare2", name: "ci-2" },
        ]),
      },
    });
    expect(await screen.findByTestId("project-path-row-spare")).toBeTruthy();
    expect(screen.getByTestId("project-path-row-spare2")).toBeTruthy();
    expect(screen.queryByTestId("project-paths-add-open")).toBeNull();
  });

  it("全都相关时那颗「＋」不画 —— 点开只会说「都在表里了」", async () => {
    open();
    await screen.findByTestId("project-path-row-self");
    expect(screen.queryByTestId("project-paths-add-open")).toBeNull();
  });

  /**
   * `hasMember` 是**可选能力**：宿主答不出「某个成员的 Agent 绑在哪台机器上」时，
   * 这一节退成「本机 + 已配路径的」，功能不减，只是少一档自动带出来的行。
   */
  it("宿主答不出成员在哪台机器上时，退成本机 + 已配路径的", async () => {
    open({
      ports: {
        listMachines: vi.fn(async () =>
          MACHINES.map(({ hasMember: _drop, ...m }) => m),
        ),
      },
    });
    await screen.findByTestId("project-path-row-self");
    expect(screen.getByTestId("project-path-row-farm")).toBeTruthy();
    expect(screen.queryByTestId("project-path-row-peer")).toBeNull();
  });

  it("节头说得出列了几台、其中几台还没配路径", async () => {
    open();
    expect(await screen.findByTestId("project-paths-count")).toHaveTextContent(
      "3",
    );
    // MACHINES 里 peer 没有路径。
    expect(screen.getByTestId("project-paths-unconfigured")).toHaveTextContent(
      "1",
    );
  });
});

/**
 * 失败要说在出事的地方。
 *
 * 此前每一种写失败都落到脚部那一行 —— 它在滚动正文的下面，而点了那一格的人视线就在
 * 那一格上。重名是唯一一种「针对某一格」的业务失败，所以只有名字有这一档；其余的写
 * 说的都不是某一格的事，仍然落脚部。
 */
describe("字段级失败", () => {
  const taken: ProjectWriteOutcome = {
    ok: false,
    failure: { kind: "unknown", message: "已经有一个叫这个名字的项目了" },
  };

  it("名字写失败时那一句紧贴名字，不去脚部", async () => {
    open({ ports: { updateFields: vi.fn(async () => taken) } });
    const name = await screen.findByTestId("project-settings-name");
    fireEvent.change(name, { target: { value: "Platform" } });
    fireEvent.blur(name);
    expect(
      await screen.findByTestId("project-settings-name-error"),
    ).toHaveTextContent("已经有一个叫这个名字的项目了");
    const footer = screen.getByTestId("project-settings-footer");
    expect(footer.textContent).not.toContain("已经有一个叫这个名字的项目了");
  });

  it("不是某一格的失败照旧落脚部", async () => {
    open({ ports: { removeMember: vi.fn(async () => taken) } });
    await screen.findByTestId("project-section-members");
    fireEvent.click(screen.getByTestId("project-member-remove-m1"));
    const footer = await screen.findByTestId("project-settings-footer");
    await waitFor(() =>
      expect(footer.textContent).toContain("已经有一个叫这个名字的项目了"),
    );
    expect(screen.queryByTestId("project-settings-name-error")).toBeNull();
  });

  it("下一次写成功就把那一句收掉 —— 留着它等于说这一格还是错的", async () => {
    const updateFields = vi
      .fn<ProjectSettingsPorts["updateFields"]>()
      .mockResolvedValueOnce(taken)
      .mockResolvedValue({ ok: true });
    open({ ports: { updateFields } });
    const name = await screen.findByTestId("project-settings-name");
    fireEvent.change(name, { target: { value: "Platform" } });
    fireEvent.blur(name);
    await screen.findByTestId("project-settings-name-error");
    fireEvent.change(name, { target: { value: "Atlas III" } });
    fireEvent.blur(name);
    await waitFor(() =>
      expect(screen.queryByTestId("project-settings-name-error")).toBeNull(),
    );
  });
});

/**
 * 候选不再平铺到底：账号里 Agent 一多，那一排 chip 就把这一节糊成一片，而加不了的
 * 还把原因塞进 chip 里，宽度乱跳。
 */
describe("成员选人层", () => {
  it("搜得动，且搜不到时说的是「没有匹配」而不是「都加过了」", async () => {
    open({
      project: {
        ...PROJECT,
        candidates: [
          { id: "a9", name: "Builder" },
          { id: "a7", name: "Reviewer2" },
        ],
      },
    });
    await screen.findByTestId("project-section-members");
    fireEvent.click(screen.getByTestId("project-member-add-open"));
    const search = await screen.findByTestId("project-member-search");
    fireEvent.change(search, { target: { value: "build" } });
    expect(screen.getByTestId("project-member-add-a9")).toBeTruthy();
    expect(screen.queryByTestId("project-member-add-a7")).toBeNull();

    fireEvent.change(search, { target: { value: "zzz" } });
    const none = await screen.findByTestId("project-member-none");
    expect(none.textContent).toContain("No agent matches");
  });

  it("一个候选都没有时点开说的是「都已经在这个项目里了」", async () => {
    open({ project: { ...PROJECT, candidates: [] } });
    await screen.findByTestId("project-section-members");
    fireEvent.click(screen.getByTestId("project-member-add-open"));
    const none = await screen.findByTestId("project-member-none");
    expect(none.textContent).toContain("already a member");
  });
});
