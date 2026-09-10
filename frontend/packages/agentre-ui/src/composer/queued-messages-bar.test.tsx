import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { QueuedMessagesBar } from "./queued-messages-bar";
import type { QueuedItem } from "./steer-queue";

function renderBar(queued: QueuedItem[]) {
  return render(
    <QueuedMessagesBar
      queued={queued}
      onCancel={vi.fn()}
      onClearAll={vi.fn()}
    />,
  );
}

describe("QueuedMessagesBar", () => {
  it("renders nothing when there are no queued messages", () => {
    const { container } = renderBar([]);
    expect(container.firstChild).toBeNull();
  });

  it("renders each queued message text", () => {
    renderBar([
      { id: "a", text: "第一条排队消息", cancellable: true },
      { id: "b", text: "second queued message", cancellable: false },
    ]);
    expect(screen.getByText("第一条排队消息")).toBeInTheDocument();
    expect(screen.getByText("second queued message")).toBeInTheDocument();
  });

  // 撤回被对端拒了之后,这条 chip 留在原位并转成锁住 —— 悬停要讲**这一条**为什么
  // 撤不动(对端那句已本地化的原话),而不是那句通用的「这个后端不支持撤回」。
  it("shows the item's own note on the lock when cancelling was refused", () => {
    renderBar([
      {
        id: "a",
        text: "撤不掉的这条",
        cancellable: false,
        note: "这条已经被取走了",
      },
    ]);
    expect(screen.getByTitle("这条已经被取走了")).toBeInTheDocument();
  });

  // 真实诉求:排队中的消息是用户刚写的原文,得能选中复制(比如复制到别处重发)。
  // body 全局 user-select:none,只有挂 data-selectable-text='true' 的子树才放开。
  it("marks queued message text as selectable for copying", () => {
    renderBar([{ id: "a", text: "可被选中复制的排队文字", cancellable: true }]);
    const textEl = screen.getByText("可被选中复制的排队文字");
    expect(textEl.closest("[data-selectable-text='true']")).not.toBeNull();
  });
});

describe("QueuedMessagesBar dropped banner", () => {
  const dropped: QueuedItem[] = [
    { id: "a", text: "第一条未发送消息", cancellable: true },
    { id: "b", text: "second unsent message", cancellable: false },
  ];

  function renderDropped(
    overrides: {
      dropped?: QueuedItem[] | null;
      onRestoreDropped?: () => void;
      onDiscardDropped?: () => void;
    } = {},
  ) {
    const onRestoreDropped = vi.fn();
    const onDiscardDropped = vi.fn();
    return render(
      <QueuedMessagesBar
        queued={[]}
        onCancel={vi.fn()}
        onClearAll={vi.fn()}
        dropped={overrides.dropped === undefined ? dropped : overrides.dropped}
        onRestoreDropped={overrides.onRestoreDropped ?? onRestoreDropped}
        onDiscardDropped={overrides.onDiscardDropped ?? onDiscardDropped}
      />,
    );
  }

  it("renders the dropped banner with count, item texts and restore/discard actions", () => {
    renderDropped();
    expect(
      screen.getByText("2 message(s) were not sent when the turn ended"),
    ).toBeInTheDocument();
    expect(screen.getByText("第一条未发送消息")).toBeInTheDocument();
    expect(screen.getByText("second unsent message")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Restore as draft" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Discard" })).toBeInTheDocument();
  });

  it("restore calls onRestoreDropped and discard calls onDiscardDropped", () => {
    const onRestoreDropped = vi.fn();
    const onDiscardDropped = vi.fn();
    renderDropped({ onRestoreDropped, onDiscardDropped });

    fireEvent.click(screen.getByRole("button", { name: "Restore as draft" }));
    expect(onRestoreDropped).toHaveBeenCalledOnce();
    expect(onDiscardDropped).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Discard" }));
    expect(onDiscardDropped).toHaveBeenCalledOnce();
  });

  it("renders nothing when dropped is null even if callbacks are wired", () => {
    const { container } = renderDropped({ dropped: null });
    expect(container.firstChild).toBeNull();
  });

  it("dropped banner wins over an empty queue (markDropped cleared it)", () => {
    // queued 为 [] 但 dropped 有内容:必须渲染丢弃横幅,而不是返回 null。
    renderDropped();
    expect(screen.getByRole("alert")).toBeInTheDocument();
  });

  it("keeps item text selectable for copying", () => {
    renderDropped();
    const textEl = screen.getByText("第一条未发送消息");
    expect(textEl.closest("[data-selectable-text='true']")).not.toBeNull();
  });
});
