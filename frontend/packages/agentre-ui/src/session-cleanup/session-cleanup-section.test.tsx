import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { SessionCleanupSection } from "./session-cleanup-section";
import type {
  CleanupExecuteView,
  CleanupPreviewView,
  SessionCleanupPorts,
} from "./ports";

const CUTOFF = new Date(2026, 7, 15, 12).getTime();

function preview(over: Partial<CleanupPreviewView> = {}): CleanupPreviewView {
  return {
    count: 3,
    retained: 2,
    cutoffDate: CUTOFF,
    ids: ["a", "b", "c"],
    copies: [
      { kind: "local", label: "", count: 3 },
      { kind: "account", label: "", count: 2 },
      { kind: "machine", label: "build-box", online: true, count: 2 },
      { kind: "machine", label: "old-laptop", online: false, count: 1 },
      {
        kind: "machine",
        label: "pair-box",
        lanDirect: true,
        online: true,
        count: 1,
      },
    ],
    ...over,
  };
}

function exec(over: Partial<CleanupExecuteView> = {}): CleanupExecuteView {
  return { deleted: 3, failed: [], pending: [], ...over };
}

function makePorts(
  over: Partial<SessionCleanupPorts> = {},
): SessionCleanupPorts {
  return {
    preview: vi.fn().mockResolvedValue(preview()),
    execute: vi.fn().mockResolvedValue(exec()),
    agents: [{ id: "7", label: "Claude Code" }],
    projects: [{ id: "9", label: "agentre" }],
    ...over,
  };
}

const deferred = <T,>() => {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
};

async function openConfirm() {
  const button = await screen.findByRole("button", {
    name: /Clean up 3 conversations/,
  });
  await waitFor(() => expect(button).toBeEnabled());
  fireEvent.click(button);
  return screen.findByRole("dialog");
}

describe("SessionCleanupSection · 条件与预览", () => {
  it("默认 90 天前 / 全部 Agent / 全部项目，进来就按默认条件计数", async () => {
    const ports = makePorts();
    render(<SessionCleanupSection ports={ports} />);

    expect(ports.preview).toHaveBeenCalledWith({
      before: "90d",
      agentId: "all",
      projectId: "all",
    });
    expect(screen.getByRole("tab", { name: "90 days ago" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(
      await screen.findByText("3 conversations will be deleted"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("2 running or waiting for you will be kept"),
    ).toBeInTheDocument();
  });

  it("换时间档就用新条件重新计数", async () => {
    const ports = makePorts();
    render(<SessionCleanupSection ports={ports} />);
    await screen.findByText("3 conversations will be deleted");

    fireEvent.click(screen.getByRole("tab", { name: "All time" }));

    await waitFor(() =>
      expect(ports.preview).toHaveBeenLastCalledWith({
        before: "all",
        agentId: "all",
        projectId: "all",
      }),
    );
  });

  it("方向键在分段控件里移动并选中（键盘可达）", async () => {
    const ports = makePorts();
    render(<SessionCleanupSection ports={ports} />);
    await screen.findByText("3 conversations will be deleted");

    screen.getByRole("tab", { name: "90 days ago" }).focus();
    await userEvent.keyboard("{ArrowRight}");

    expect(screen.getByRole("tab", { name: "180 days ago" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    await waitFor(() =>
      expect(ports.preview).toHaveBeenLastCalledWith(
        expect.objectContaining({ before: "180d" }),
      ),
    );
  });

  it("Agent 下拉能用键盘打开并选择，选择后按该 Agent 重新计数", async () => {
    const ports = makePorts();
    render(<SessionCleanupSection ports={ports} />);
    await screen.findByText("3 conversations will be deleted");

    const select = screen.getByRole("combobox", { name: "Agent" });
    select.focus();
    expect(select).toHaveFocus();
    await userEvent.keyboard("{Enter}");
    await userEvent.click(
      await screen.findByRole("option", { name: "Claude Code" }),
    );

    await waitFor(() =>
      expect(ports.preview).toHaveBeenLastCalledWith(
        expect.objectContaining({ agentId: "7" }),
      ),
    );
  });

  it("项目下拉列出宿主给的项目", async () => {
    render(<SessionCleanupSection ports={makePorts()} />);
    const select = screen.getByRole("combobox", { name: "Project" });
    expect(select).toHaveTextContent("All projects");
  });

  it("计数中：汇总是骨架，清理按钮不可点", () => {
    const ports = makePorts({
      preview: vi.fn(() => new Promise<CleanupPreviewView>(() => {})),
    });
    render(<SessionCleanupSection ports={ports} />);

    expect(screen.getByRole("button", { name: /Clean up/ })).toBeDisabled();
    expect(screen.getByRole("status")).toHaveAttribute("aria-busy", "true");
    expect(screen.queryByText(/will be deleted/)).not.toBeInTheDocument();
  });

  it("过期的计数结果不会盖掉新条件的结果", async () => {
    const slow = deferred<CleanupPreviewView>();
    const fast = deferred<CleanupPreviewView>();
    const previewFn = vi
      .fn()
      .mockReturnValueOnce(slow.promise)
      .mockReturnValueOnce(fast.promise);
    render(<SessionCleanupSection ports={makePorts({ preview: previewFn })} />);

    fireEvent.click(screen.getByRole("tab", { name: "30 days ago" }));
    fast.resolve(preview({ count: 5, ids: ["1", "2", "3", "4", "5"] }));
    await screen.findByText("5 conversations will be deleted");
    slow.resolve(preview({ count: 9 }));
    await Promise.resolve();

    expect(
      screen.getByText("5 conversations will be deleted"),
    ).toBeInTheDocument();
  });

  it("数量为 0：按钮不可点，写明日期", async () => {
    const ports = makePorts({
      preview: vi
        .fn()
        .mockResolvedValue(
          preview({ count: 0, ids: [], copies: [], retained: 0 }),
        ),
    });
    render(<SessionCleanupSection ports={ports} />);

    expect(
      await screen.findByText(/No conversations last active before .*2026/),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Clean up/ })).toBeDisabled();
  });

  it("数量为 0 且选了「全部」：不写日期", async () => {
    const ports = makePorts({
      preview: vi.fn().mockResolvedValue(
        preview({
          count: 0,
          ids: [],
          copies: [],
          retained: 0,
          cutoffDate: null,
        }),
      ),
    });
    render(<SessionCleanupSection ports={ports} />);

    expect(
      await screen.findByText("No conversations to clean up"),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(/No conversations last active/),
    ).not.toBeInTheDocument();
  });

  it("计数失败：说明 + 重试，不允许删除；重试成功后恢复", async () => {
    const previewFn = vi
      .fn()
      .mockRejectedValueOnce(new Error("boom"))
      .mockResolvedValue(preview());
    render(<SessionCleanupSection ports={makePorts({ preview: previewFn })} />);

    expect(
      await screen.findByText(
        "Couldn't work out how many conversations to clean up",
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Clean up/ })).toBeDisabled();

    fireEvent.click(screen.getByRole("button", { name: "Try again" }));

    expect(
      await screen.findByText("3 conversations will be deleted"),
    ).toBeInTheDocument();
    expect(previewFn).toHaveBeenCalledTimes(2);
  });
});

describe("SessionCleanupSection · 文案变体", () => {
  it("desktop 与 console 各有自己的标题和说明", () => {
    const { unmount } = render(
      <SessionCleanupSection ports={makePorts()} variant="desktop" />,
    );
    expect(
      screen.getByRole("heading", { name: "Clean up conversation history" }),
    ).toBeInTheDocument();
    unmount();
    render(<SessionCleanupSection ports={makePorts()} variant="console" />);
    expect(
      screen.getByRole("heading", { name: "Clean up saved conversations" }),
    ).toBeInTheDocument();
  });

  it("fullWidthAction 让按钮占满整行", async () => {
    render(<SessionCleanupSection ports={makePorts()} fullWidthAction />);
    expect(
      await screen.findByRole("button", { name: /Clean up 3/ }),
    ).toHaveClass("w-full");
  });
});

describe("SessionCleanupSection · 确认", () => {
  it("标题含数量，写明无法撤销，逐份列出副本及在线 / 离线 / 局域网直接删除", async () => {
    render(<SessionCleanupSection ports={makePorts()} />);
    const dialog = await openConfirm();

    expect(
      within(dialog).getByText("Delete 3 conversations?"),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByText("This can't be undone."),
    ).toBeInTheDocument();
    expect(within(dialog).getByText("This computer")).toBeInTheDocument();
    expect(
      within(dialog).getByText("Copies saved to your account"),
    ).toBeInTheDocument();
    const offline = within(dialog).getByText("old-laptop").closest("li")!;
    expect(offline).toHaveTextContent(
      "Offline · deleted next time it's online",
    );
    const online = within(dialog).getByText("build-box").closest("li")!;
    expect(online).toHaveTextContent("Online");
    expect(online).not.toHaveTextContent("Offline");
    const lan = within(dialog).getByText("pair-box").closest("li")!;
    expect(lan).toHaveTextContent("LAN pairing · deleted directly");
  });

  it("取消只关框，不执行", async () => {
    const ports = makePorts();
    render(<SessionCleanupSection ports={ports} />);
    const dialog = await openConfirm();

    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));

    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    expect(ports.execute).not.toHaveBeenCalled();
  });
});

describe("SessionCleanupSection · 执行", () => {
  it("送出的是确认那一刻计数里的 ids；删除期间框不能关、按钮转圈、没有 banner", async () => {
    const run = deferred<CleanupExecuteView>();
    const ports = makePorts({ execute: vi.fn(() => run.promise) });
    render(<SessionCleanupSection ports={ports} />);
    const dialog = await openConfirm();

    fireEvent.click(within(dialog).getByRole("button", { name: "Delete 3" }));

    expect(ports.execute).toHaveBeenCalledWith(["a", "b", "c"]);
    const busy = await within(dialog).findByRole("button", {
      name: /Deleting/,
    });
    expect(busy).toBeDisabled();
    expect(
      within(dialog).getByRole("button", { name: "Cancel" }),
    ).toBeDisabled();
    fireEvent.keyDown(dialog, { key: "Escape" });
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();

    run.resolve(exec());
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
  });

  it("全部成功：框关闭、onDone 带结果、按当前条件重新计数", async () => {
    const onDone = vi.fn();
    const result = exec({
      pending: [{ label: "old-laptop", count: 1 }],
    });
    const ports = makePorts({
      execute: vi.fn().mockResolvedValue(result),
      onDone,
    });
    render(<SessionCleanupSection ports={ports} />);
    const dialog = await openConfirm();
    expect(ports.preview).toHaveBeenCalledTimes(1);

    fireEvent.click(within(dialog).getByRole("button", { name: "Delete 3" }));

    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    expect(onDone).toHaveBeenCalledTimes(1);
    expect(onDone).toHaveBeenCalledWith(result);
    await waitFor(() => expect(ports.preview).toHaveBeenCalledTimes(2));
  });

  it("部分失败：框不关，标题改成还剩 K 个，给出原因；重试只送剩下的 ids", async () => {
    const onDone = vi.fn();
    const execute = vi
      .fn()
      .mockResolvedValueOnce(
        exec({
          deleted: 1,
          failed: [
            { id: "b", reason: "Disk is busy" },
            { id: "c", reason: "Disk is busy" },
          ],
          pending: [{ label: "old-laptop", count: 1 }],
        }),
      )
      .mockResolvedValueOnce(exec({ deleted: 2 }));
    render(<SessionCleanupSection ports={makePorts({ execute, onDone })} />);
    const dialog = await openConfirm();

    fireEvent.click(within(dialog).getByRole("button", { name: "Delete 3" }));

    expect(
      await within(dialog).findByText("2 conversations couldn't be deleted"),
    ).toBeInTheDocument();
    expect(within(dialog).getByText(/Disk is busy/)).toBeInTheDocument();
    expect(onDone).not.toHaveBeenCalled();

    fireEvent.click(within(dialog).getByRole("button", { name: "Retry 2" }));

    await waitFor(() => expect(execute).toHaveBeenCalledTimes(2));
    expect(execute).toHaveBeenLastCalledWith(["b", "c"]);
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    expect(onDone).toHaveBeenCalledWith({
      deleted: 3,
      failed: [],
      pending: [{ label: "old-laptop", count: 1 }],
    });
  });

  it("部分失败后点「关闭」：已删掉的照样交给 onDone，并重新计数", async () => {
    const onDone = vi.fn();
    const execute = vi
      .fn()
      .mockResolvedValue(
        exec({ deleted: 2, failed: [{ id: "c", reason: "Disk is busy" }] }),
      );
    const ports = makePorts({ execute, onDone });
    render(<SessionCleanupSection ports={ports} />);
    const dialog = await openConfirm();
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete 3" }));
    await within(dialog).findByText("1 conversation couldn't be deleted");

    fireEvent.click(within(dialog).getByRole("button", { name: "Close" }));

    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    expect(onDone).toHaveBeenCalledWith(
      expect.objectContaining({
        deleted: 2,
        failed: [{ id: "c", reason: "Disk is busy" }],
      }),
    );
    await waitFor(() => expect(ports.preview).toHaveBeenCalledTimes(2));
  });

  it("execute 整个失败：框留在确认态、能再点，不丢 ids", async () => {
    const execute = vi
      .fn()
      .mockRejectedValueOnce(new Error("net"))
      .mockResolvedValueOnce(exec());
    render(<SessionCleanupSection ports={makePorts({ execute })} />);
    const dialog = await openConfirm();

    fireEvent.click(within(dialog).getByRole("button", { name: "Delete 3" }));

    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Deletion didn't finish. Try again.",
    );
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete 3" }));
    await waitFor(() => expect(execute).toHaveBeenCalledTimes(2));
    expect(execute).toHaveBeenLastCalledWith(["a", "b", "c"]);
  });
});
