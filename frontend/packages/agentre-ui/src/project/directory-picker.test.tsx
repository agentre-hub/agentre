import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { DirectoryPicker } from "./directory-picker";
import type {
  DirectoryEntry,
  ListDirOutcome,
  PickerMachine,
  ProjectFsPort,
} from "./ports";

/**
 * 目录选择器，两端共用那一份（规格 2026-08-22 D 段，决策 11）。
 *
 * 合之前两端**不是同一个形状**：桌面端 `remote-fs-picker.tsx` 是单机文件浏览器
 * （deviceID 传入、列文件、filter / 隐藏项 / symlink），本仓 `DirectoryPickerDialog.tsx`
 * 是多机目录选择器（自己列机器、只列目录、git 标记）。两者问的是同一件事——
 * 「给这个项目在某台机器上挑一个目录」——所以合，合的时候**取并集**，两端各自
 * 获得对方已经做对的那半边。
 *
 * 传输是真正的宿主差异，由 port 吃掉。port **交出判别式结果而不是抛错**：错误分类
 * 是 wire 相关的（本仓是 JSON-RPC 码，桌面端是 Go 错误串），让宿主分好类再交进来，
 * 包不去猜一个它读不懂的 error 是哪一类。
 */

const MACHINES: PickerMachine[] = [
  { id: "m0", name: "wangyz-mbp", kind: "desktop", online: true },
  { id: "m1", name: "build-01", kind: "agentred", online: true },
  { id: "m2", name: "build-03", kind: "agentred", online: false },
];

const ENTRIES: DirectoryEntry[] = [
  { name: ".cache", isDir: true },
  { name: "atlas", isDir: true },
  { name: "ledger", isDir: true },
  { name: "shared", isDir: true, symlink: true },
  { name: "notes.md", isDir: false },
];

function ok(
  path: string,
  entries = ENTRIES,
  truncated = false,
  isGitRepo = false,
): ListDirOutcome {
  return { ok: true, result: { path, entries, truncated, isGitRepo } };
}

function port(over: Partial<ProjectFsPort> = {}): ProjectFsPort {
  return {
    listDir: vi.fn(async (_machineId: string, path: string) =>
      ok(path || "/srv/work"),
    ),
    mkdir: vi.fn(async () => ({ ok: true as const, result: undefined })),
    ...over,
  };
}

function renderPicker(
  over: {
    fs?: ProjectFsPort;
    onPick?: (machineId: string, path: string) => void;
    machines?: PickerMachine[];
    initialMachineId?: string;
  } = {},
) {
  const fs = over.fs ?? port();
  const onPick = over.onPick ?? vi.fn();
  render(
    <DirectoryPicker
      open
      onOpenChange={vi.fn()}
      fs={fs}
      machines={over.machines ?? MACHINES}
      initialMachineId={over.initialMachineId ?? "m1"}
      onPick={onPick}
    />,
  );
  return { fs, onPick };
}

const rail = () => screen.getByTestId("directory-picker-machines");
const listing = () => screen.getByTestId("directory-picker-listing");

describe("目录选择器的机器那一栏", () => {
  it("列出账号里的机器，离线的留在列表里但按不动", async () => {
    renderPicker();
    await screen.findByText("atlas");
    const buttons = within(rail()).getAllByRole("button");
    expect(buttons.map((b) => b.textContent?.trim())).toEqual([
      "wangyz-mbp",
      "build-01",
      "build-03",
    ]);
    // 隐藏会让人以为那台机器没配对。
    expect(buttons[2]).toBeDisabled();
  });

  it("只有一台机器时整个机器栏不画", async () => {
    // 一个只有一个选项的选择器不是选择器，只是占掉 172px。
    // 与「只有一个成员就直接开对话」同一条。
    renderPicker({
      machines: [MACHINES[1]],
      initialMachineId: "m1",
    });
    await screen.findByText("atlas");
    expect(screen.queryByTestId("directory-picker-machines")).toBeNull();
    // 但机器是哪一台仍然说得出来——标题上点名。
    expect(screen.getByRole("heading")).toHaveTextContent("build-01");
  });

  it("打开时那台机器已选中，标题上点名", async () => {
    renderPicker({ initialMachineId: "m0" });
    await screen.findByText("atlas");
    expect(screen.getByRole("heading")).toHaveTextContent("wangyz-mbp");
  });

  it("换一台在线机器：不关窗，用新机器重读", async () => {
    const { fs } = renderPicker();
    await screen.findByText("atlas");
    fireEvent.click(within(rail()).getByRole("button", { name: /wangyz-mbp/ }));
    // 同一个项目常要在两三台机器上各配一次，关窗再开一次是三倍操作。
    await waitFor(() =>
      expect(fs.listDir).toHaveBeenCalledWith("m0", expect.any(String)),
    );
    expect(screen.getByRole("heading")).toHaveTextContent("wangyz-mbp");
  });
});

describe("目录选择器的清单", () => {
  it("目录可选，文件可见但不可选", async () => {
    renderPicker();
    await screen.findByText("atlas");
    const dir = within(listing()).getByRole("button", { name: /atlas/ });
    expect(dir).not.toBeDisabled();
    // 文件行给的是「这个目录是不是我要的那个」的上下文，灰掉已经说明不可选。
    const file = within(listing()).getByRole("button", { name: /notes\.md/ });
    expect(file).toBeDisabled();
  });

  it("点进一个子目录，面包屑增一节", async () => {
    const { fs } = renderPicker();
    await screen.findByText("atlas");
    fireEvent.click(within(listing()).getByRole("button", { name: /atlas/ }));
    await waitFor(() =>
      expect(fs.listDir).toHaveBeenCalledWith("m1", "/srv/work/atlas"),
    );
    expect(
      within(screen.getByTestId("directory-picker-breadcrumb")).getByText(
        "atlas",
      ),
    ).toBeTruthy();
  });

  it("符号链接挂角标", async () => {
    renderPicker();
    await screen.findByText("atlas");
    expect(
      within(listing()).getByTestId("directory-picker-symlink-shared"),
    ).toBeTruthy();
  });

  it("git 标记标的是**当前目录**，不是逐个子目录", async () => {
    // 子目录里有没有 .git，这一次 listDir 根本没读——画成逐行角标就是画了一个
    // 后端答不出来的东西。判据是这次列出来的条目里有没有 .git。
    renderPicker({
      fs: port({
        listDir: vi.fn(async (_m, p) =>
          ok(p || "/srv/work", ENTRIES, false, true),
        ),
      }),
    });
    await screen.findByText("atlas");
    expect(screen.getByTestId("directory-picker-git")).toBeTruthy();
    expect(within(listing()).queryByTestId("directory-picker-git")).toBeNull();
  });

  it("当前目录不是仓库就不画那枚标记", async () => {
    renderPicker();
    await screen.findByText("atlas");
    expect(screen.queryByTestId("directory-picker-git")).toBeNull();
  });

  it("目录排在文件前面，同类按名字排（桌面端本来就有，合并时不能丢）", async () => {
    renderPicker({
      fs: port({
        listDir: vi.fn(async (_m, p) =>
          ok(p || "/srv/work", [
            { name: "zeta.md", isDir: false },
            { name: "item10", isDir: true },
            { name: "alpha.md", isDir: false },
            { name: "item2", isDir: true },
          ]),
        ),
      }),
    });
    await screen.findByText("item2");
    const names = within(listing())
      .getAllByRole("button")
      .map((b) => b.textContent?.trim());
    // item2 在 item10 前面：按人读的顺序排，不是字典序。
    expect(names).toEqual(["item2", "item10", "alpha.md", "zeta.md"]);
  });

  it("默认不列隐藏项，勾上才列", async () => {
    renderPicker();
    await screen.findByText("atlas");
    expect(within(listing()).queryByText(".cache")).toBeNull();
    fireEvent.click(screen.getByLabelText("Hidden items"));
    expect(within(listing()).getByText(".cache")).toBeTruthy();
  });
});

describe("目录选择器的筛选", () => {
  it("按名字收窄", async () => {
    renderPicker();
    await screen.findByText("atlas");
    fireEvent.change(screen.getByPlaceholderText("Filter…"), {
      target: { value: "led" },
    });
    expect(within(listing()).getByText("ledger")).toBeTruthy();
    expect(within(listing()).queryByText("atlas")).toBeNull();
  });

  it("一条都不匹配时说出来，而不是留一个空白面板", async () => {
    renderPicker();
    await screen.findByText("atlas");
    fireEvent.change(screen.getByPlaceholderText("Filter…"), {
      target: { value: "zzz" },
    });
    expect(screen.getByTestId("directory-picker-no-match")).toHaveTextContent(
      "zzz",
    );
  });
});

describe("目录选择器的四类失败各自可分辨", () => {
  it.each([
    ["denied", "not allowed"],
    ["notFound", "no longer exists"],
    ["disconnected", "Lost the connection"],
    ["unknown", "Could not read"],
  ] as const)("%s 给出自己那一句", async (kind, phrase) => {
    renderPicker({
      fs: port({
        listDir: vi.fn(async () => ({
          ok: false as const,
          failure: { kind, message: "" },
        })),
      }),
    });
    // 把权限拒绝画成空目录，用户会以为那台机器上什么都没有。
    const box = await screen.findByTestId("directory-picker-failure");
    expect(box.textContent).toContain(phrase);
  });

  it("空目录不是失败：它是一条能走的路", async () => {
    renderPicker({
      fs: port({ listDir: vi.fn(async (_m, p) => ok(p || "/srv/work", [])) }),
    });
    const empty = await screen.findByTestId("directory-picker-empty");
    expect(empty).toBeTruthy();
    expect(screen.queryByTestId("directory-picker-failure")).toBeNull();
    // 空目录照样选得了。
    expect(
      screen.getByRole("button", { name: "Choose this folder" }),
    ).not.toBeDisabled();
  });

  it("条目截断时如实说，不假装这就是全部", async () => {
    renderPicker({
      fs: port({
        listDir: vi.fn(async (_m, p) => ok(p || "/srv/work", ENTRIES, true)),
      }),
    });
    expect(
      await screen.findByTestId("directory-picker-truncated"),
    ).toBeTruthy();
  });
});

describe("目录选择器的新建目录", () => {
  it("建完重读当前目录", async () => {
    const { fs } = renderPicker();
    await screen.findByText("atlas");
    fireEvent.click(screen.getByRole("button", { name: "New folder" }));
    fireEvent.change(screen.getByPlaceholderText("New folder name"), {
      target: { value: "edge" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    await waitFor(() =>
      expect(fs.mkdir).toHaveBeenCalledWith("m1", "/srv/work", "edge"),
    );
    await waitFor(() =>
      expect(
        (fs.listDir as ReturnType<typeof vi.fn>).mock.calls.length,
      ).toBeGreaterThan(1),
    );
  });

  it("名字前后多打的空格修掉就好，不当成错误报出来", async () => {
    const { fs } = renderPicker();
    await screen.findByText("atlas");
    fireEvent.click(screen.getByRole("button", { name: "New folder" }));
    fireEvent.change(screen.getByPlaceholderText("New folder name"), {
      target: { value: "  edge  " },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    await waitFor(() =>
      expect(fs.mkdir).toHaveBeenCalledWith("m1", "/srv/work", "edge"),
    );
  });

  it.each([["a/b"], [".."], ["."], [""]])(
    "名字 %s 当场拦下，不白跑一趟往那台机器发",
    async (name) => {
      const { fs } = renderPicker();
      await screen.findByText("atlas");
      fireEvent.click(screen.getByRole("button", { name: "New folder" }));
      fireEvent.change(screen.getByPlaceholderText("New folder name"), {
        target: { value: name },
      });
      fireEvent.click(screen.getByRole("button", { name: "Create" }));
      // 斜杠、首尾空白、`.`/`..` 在任何一端都建不出来，就地说比等一趟往返说要好。
      expect(
        await screen.findByTestId("directory-picker-create-error"),
      ).toBeTruthy();
      expect(fs.mkdir).not.toHaveBeenCalled();
    },
  );

  it("建失败就地说明，不静默吞掉", async () => {
    renderPicker({
      fs: port({
        mkdir: vi.fn(async () => ({
          ok: false as const,
          failure: { kind: "invalidName" as const, message: "bad name" },
        })),
      }),
    });
    await screen.findByText("atlas");
    fireEvent.click(screen.getByRole("button", { name: "New folder" }));
    fireEvent.change(screen.getByPlaceholderText("New folder name"), {
      target: { value: "a/b" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    expect(
      await screen.findByTestId("directory-picker-create-error"),
    ).toBeTruthy();
  });
});

describe("目录选择器交出去的东西", () => {
  it("只交出（哪台机器, 哪个路径），写在哪归调用方", async () => {
    const { onPick } = renderPicker();
    await screen.findByText("atlas");
    fireEvent.click(within(listing()).getByRole("button", { name: /atlas/ }));
    await screen.findByText("atlas", { selector: '[data-slot="crumb"]' });
    fireEvent.click(screen.getByRole("button", { name: "Choose this folder" }));
    expect(onPick).toHaveBeenCalledWith("m1", "/srv/work/atlas");
  });
});
