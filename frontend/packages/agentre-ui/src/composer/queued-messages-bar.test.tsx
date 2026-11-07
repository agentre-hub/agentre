import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { QueuedMessagesBar } from "./queued-messages-bar";
import type { QueuedItem } from "./steer-queue";
import { edgeClasses } from "../lib/__testing__/edge-classes";

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

describe("QueuedMessagesBar · 无边框", () => {
  it("Given queued messages, When rendered, Then the bar, its button and its items are fills without edges", () => {
    render(
      <QueuedMessagesBar
        queued={[{ id: "q1", text: "hello", cancellable: true }]}
        onCancel={vi.fn()}
        onClearAll={vi.fn()}
      />,
    );

    expect(edgeClasses(screen.getByRole("region"))).toEqual([]);
  });

  it("Given dropped messages, When rendered, Then the banner, its buttons and its items are fills without edges", () => {
    render(
      <QueuedMessagesBar
        queued={[]}
        onCancel={vi.fn()}
        onClearAll={vi.fn()}
        dropped={[{ id: "d1", text: "lost", cancellable: true }]}
        onRestoreDropped={vi.fn()}
        onDiscardDropped={vi.fn()}
      />,
    );

    expect(edgeClasses(screen.getByRole("alert"))).toEqual([]);
  });
});

describe("QueuedMessagesBar · 撤回到输入框", () => {
  function renderWithdraw(
    queued: QueuedItem[],
    extra: { withdrawDisabled?: boolean } = {},
  ) {
    const onWithdraw = vi.fn();
    const onCancel = vi.fn();
    render(
      <QueuedMessagesBar
        queued={queued}
        onCancel={onCancel}
        onClearAll={vi.fn()}
        onWithdraw={onWithdraw}
        withdrawDisabled={extra.withdrawDisabled}
      />,
    );
    return { onWithdraw, onCancel };
  }

  it("Given the host wired withdraw, When a cancellable item is shown, Then it offers both move-back and delete, each reporting its own id", () => {
    const { onWithdraw, onCancel } = renderWithdraw([
      { id: "a", text: "first", cancellable: true },
      { id: "b", text: "second", cancellable: true },
    ]);

    fireEvent.click(
      screen.getAllByRole("button", { name: "Move back to input" })[1],
    );
    expect(onWithdraw).toHaveBeenCalledWith("b");
    expect(onCancel).not.toHaveBeenCalled();

    fireEvent.click(
      screen.getAllByRole("button", { name: "Delete this queued message" })[0],
    );
    expect(onCancel).toHaveBeenCalledWith("a");
    expect(onWithdraw).toHaveBeenCalledTimes(1);
  });

  it("Given the host did not wire withdraw, When rendered, Then only delete is offered", () => {
    renderBar([{ id: "a", text: "first", cancellable: true }]);

    expect(
      screen.queryByRole("button", { name: "Move back to input" }),
    ).toBeNull();
    expect(
      screen.getByRole("button", { name: "Delete this queued message" }),
    ).toBeInTheDocument();
  });

  it("Given an item is being withdrawn, When rendered, Then both of its buttons are disabled and the other items stay usable", () => {
    renderWithdraw([
      { id: "a", text: "first", cancellable: true, withdrawing: true },
      { id: "b", text: "second", cancellable: true },
    ]);

    const moveBack = screen.getAllByRole("button", {
      name: "Move back to input",
    });
    const del = screen.getAllByRole("button", {
      name: "Delete this queued message",
    });
    expect(moveBack[0]).toBeDisabled();
    expect(del[0]).toBeDisabled();
    expect(moveBack[1]).toBeEnabled();
    expect(del[1]).toBeEnabled();
  });

  it("Given the composer is editing a message, When rendered, Then move-back is disabled with a reason while delete still works", () => {
    const { onCancel } = renderWithdraw(
      [{ id: "a", text: "first", cancellable: true }],
      { withdrawDisabled: true },
    );

    const moveBack = screen.getByRole("button", { name: "Move back to input" });
    expect(moveBack).toBeDisabled();
    expect(moveBack).toHaveAttribute("title", "Finish or cancel editing first");

    fireEvent.click(
      screen.getByRole("button", { name: "Delete this queued message" }),
    );
    expect(onCancel).toHaveBeenCalledWith("a");
  });

  it("Given the backend cannot cancel this item, When rendered, Then neither move-back nor delete is offered", () => {
    renderWithdraw([{ id: "a", text: "first", cancellable: false }]);

    expect(
      screen.queryAllByRole("button", { name: /Move back|Delete/ }),
    ).toEqual([]);
  });
});

describe("QueuedMessagesBar · touch", () => {
  const items: QueuedItem[] = [{ id: "a", text: "排队的", cancellable: true }];

  it("gives withdraw and delete 44px hit areas with 8px between them", () => {
    render(
      <QueuedMessagesBar
        queued={items}
        onCancel={vi.fn()}
        onWithdraw={vi.fn()}
        onClearAll={vi.fn()}
        inputMode="touch"
      />,
    );
    const withdraw = screen.getByRole("button", {
      name: /Move back|放回|撤回/,
    });
    const del = screen.getByRole("button", { name: /Delete this|删除/ });
    for (const b of [withdraw, del]) {
      expect(b.className).toContain("size-11");
    }
    expect(withdraw.parentElement?.className).toContain("gap-2");
  });

  it("keeps today's 20px buttons without the prop", () => {
    render(
      <QueuedMessagesBar
        queued={items}
        onCancel={vi.fn()}
        onWithdraw={vi.fn()}
        onClearAll={vi.fn()}
      />,
    );
    expect(
      screen.getByRole("button", { name: /Delete this|删除/ }).className,
    ).toContain("size-5");
  });

  it("shows the withdraw-disabled reason as visible text in touch mode only", () => {
    const { rerender } = render(
      <QueuedMessagesBar
        queued={items}
        onCancel={vi.fn()}
        onWithdraw={vi.fn()}
        withdrawDisabled
        onClearAll={vi.fn()}
        inputMode="touch"
      />,
    );
    expect(screen.getByTestId("withdraw-disabled-reason")).toBeVisible();
    rerender(
      <QueuedMessagesBar
        queued={items}
        onCancel={vi.fn()}
        onWithdraw={vi.fn()}
        withdrawDisabled
        onClearAll={vi.fn()}
      />,
    );
    expect(screen.queryByTestId("withdraw-disabled-reason")).toBeNull();
  });

  it("renders the notice line and fires onClear, independent of the queue", () => {
    const onClear = vi.fn();
    render(
      <QueuedMessagesBar
        queued={[]}
        onCancel={vi.fn()}
        onClearAll={vi.fn()}
        undeliveredNotice={{ count: 2, onClear }}
      />,
    );
    const line = screen.getByTestId("undelivered-notice");
    expect(line.textContent).toMatch(/2/);
    fireEvent.click(screen.getByRole("button", { name: /清掉|Dismiss/ }));
    expect(onClear).toHaveBeenCalledTimes(1);
  });

  it("words the notice in the singular for one steer and the plural for more", () => {
    const { rerender } = render(
      <QueuedMessagesBar
        queued={[]}
        onCancel={vi.fn()}
        onClearAll={vi.fn()}
        undeliveredNotice={{ count: 1, onClear: vi.fn() }}
      />,
    );
    expect(screen.getByTestId("undelivered-notice").textContent).toContain(
      "1 steer not delivered, put back in the input",
    );
    rerender(
      <QueuedMessagesBar
        queued={[]}
        onCancel={vi.fn()}
        onClearAll={vi.fn()}
        undeliveredNotice={{ count: 2, onClear: vi.fn() }}
      />,
    );
    expect(screen.getByTestId("undelivered-notice").textContent).toContain(
      "2 steers not delivered, put back in the input",
    );
  });

  it("renders nothing for a zero-count notice with an empty queue", () => {
    const { container } = render(
      <QueuedMessagesBar
        queued={[]}
        onCancel={vi.fn()}
        onClearAll={vi.fn()}
        undeliveredNotice={{ count: 0, onClear: vi.fn() }}
      />,
    );
    expect(container.firstChild).toBeNull();
  });
});
