import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const runtimeMocks = vi.hoisted(() => ({
  EventsOn: vi.fn(() => vi.fn()),
}));
vi.mock("../../../../wailsjs/runtime/runtime", () => runtimeMocks);

const appMocks = vi.hoisted(() => ({
  AnswerCtlApproval: vi.fn(() => Promise.resolve()),
  PendingCtlApprovals: vi.fn((): Promise<Item[]> => Promise.resolve([])),
  ShowNotification: vi.fn(() => Promise.resolve()),
}));
vi.mock("../../../../wailsjs/go/app/App", () => appMocks);

const focusMocks = vi.hoisted(() => ({ isWindowFocused: vi.fn(() => true) }));
vi.mock("@/lib/window-focus", () => focusMocks);

import { ExternalApprovalDialog } from "../external-approval-dialog";

type Item = {
  requestId: string;
  command: string;
  changes: Array<{
    op: "create" | "update" | "delete";
    kind: string;
    id?: number;
    name: string;
    fields?: Array<{
      field: string;
      before?: string;
      after?: string;
      secret?: boolean;
    }>;
  }>;
  caller?: { parentProcess: string; pid: number; workingDir: string };
};

function updateItem(): Item {
  return {
    requestId: "r1",
    command: "agrctl update provider openrouter --base-url https://x",
    changes: [
      {
        op: "update",
        kind: "provider",
        id: 1,
        name: "openrouter",
        fields: [{ field: "baseUrl", before: "a", after: "b" }],
      },
    ],
    caller: {
      parentProcess: "codex",
      pid: 48213,
      workingDir: "~/Code/agentre",
    },
  };
}

function deleteItem(): Item {
  return {
    requestId: "r2",
    command: "agrctl delete department temp --cascade",
    changes: [{ op: "delete", kind: "department", id: 2, name: "temp" }],
  };
}

function emitQueue(items: Item[]) {
  const calls = runtimeMocks.EventsOn.mock.calls as unknown as Array<
    [string, (p?: Item[]) => void]
  >;
  const entry = calls.find((c) => c[0] === "ctl:external-approval");
  if (!entry)
    throw new Error("ctl:external-approval handler was never registered");
  act(() => entry[1](items));
}

beforeEach(() => {
  runtimeMocks.EventsOn.mockReset();
  runtimeMocks.EventsOn.mockImplementation(() => vi.fn());
  appMocks.AnswerCtlApproval.mockReset();
  appMocks.AnswerCtlApproval.mockResolvedValue(undefined);
  appMocks.PendingCtlApprovals.mockReset();
  appMocks.PendingCtlApprovals.mockResolvedValue([]);
  appMocks.ShowNotification.mockReset();
  appMocks.ShowNotification.mockResolvedValue(undefined);
  focusMocks.isWindowFocused.mockReset();
  focusMocks.isWindowFocused.mockReturnValue(true);
});

describe("ExternalApprovalDialog", () => {
  it("renders nothing until a request is queued", () => {
    render(<ExternalApprovalDialog />);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("shows the command, change list and caller row for a queued request", async () => {
    render(<ExternalApprovalDialog />);
    await waitFor(() => expect(runtimeMocks.EventsOn).toHaveBeenCalled());
    emitQueue([updateItem()]);

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(
      screen.getByText(
        "agrctl update provider openrouter --base-url https://x",
      ),
    ).toBeInTheDocument();
    expect(screen.getByText("openrouter")).toBeInTheDocument();
    expect(screen.getByText("codex")).toBeInTheDocument();
    expect(screen.getByText("pid 48213")).toBeInTheDocument();
    expect(screen.getByText("~/Code/agentre")).toBeInTheDocument();
    // 单条待审批：不显示「第 N 个，共 M 个」。
    expect(screen.queryByText(/pending/i)).toBeNull();
  });

  it('shows "N of M" when several requests are queued', async () => {
    render(<ExternalApprovalDialog />);
    await waitFor(() => expect(runtimeMocks.EventsOn).toHaveBeenCalled());
    emitQueue([updateItem(), deleteItem()]);

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText("1 of 2 pending")).toBeInTheDocument();
  });

  it("uses the danger form (destructive primary button) for a delete request", async () => {
    render(<ExternalApprovalDialog />);
    await waitFor(() => expect(runtimeMocks.EventsOn).toHaveBeenCalled());
    emitQueue([deleteItem()]);

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    const approveButton = screen.getByRole("button", {
      name: /approve delete/i,
    });
    expect(approveButton.className).toMatch(/destructive/);
  });

  it("closing the dialog (Esc) answers reject", async () => {
    render(<ExternalApprovalDialog />);
    await waitFor(() => expect(runtimeMocks.EventsOn).toHaveBeenCalled());
    emitQueue([updateItem()]);
    expect(await screen.findByRole("dialog")).toBeInTheDocument();

    fireEvent.keyDown(document.body, { key: "Escape" });

    await waitFor(() =>
      expect(appMocks.AnswerCtlApproval).toHaveBeenCalledWith("r1", false),
    );
  });

  it("clicking reject answers reject", async () => {
    render(<ExternalApprovalDialog />);
    await waitFor(() => expect(runtimeMocks.EventsOn).toHaveBeenCalled());
    emitQueue([updateItem()]);
    expect(await screen.findByRole("dialog")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /reject/i }));

    await waitFor(() =>
      expect(appMocks.AnswerCtlApproval).toHaveBeenCalledWith("r1", false),
    );
  });

  it("disables both buttons while a submit is in flight", async () => {
    let resolve!: () => void;
    appMocks.AnswerCtlApproval.mockReturnValue(
      new Promise<void>((r) => {
        resolve = r;
      }),
    );
    render(<ExternalApprovalDialog />);
    await waitFor(() => expect(runtimeMocks.EventsOn).toHaveBeenCalled());
    emitQueue([updateItem()]);
    expect(await screen.findByRole("dialog")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /^approve$/i }));

    expect(screen.getByRole("button", { name: /^approve$/i })).toBeDisabled();
    expect(screen.getByRole("button", { name: /reject/i })).toBeDisabled();

    resolve();
    await waitFor(() =>
      expect(appMocks.AnswerCtlApproval).toHaveBeenCalledTimes(1),
    );
  });

  it("shows the error in the footer and stays retryable when submit fails", async () => {
    // Wails 的绑定调用以字符串拒绝；机器原话不上界面，底栏只给本地化的失败提示。
    appMocks.AnswerCtlApproval.mockRejectedValueOnce(
      'ctl_svc: no pending desktop approval "r1"',
    );
    render(<ExternalApprovalDialog />);
    await waitFor(() => expect(runtimeMocks.EventsOn).toHaveBeenCalled());
    emitQueue([updateItem()]);
    expect(await screen.findByRole("dialog")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /^approve$/i }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Failed to submit approval");
    expect(alert).not.toHaveTextContent("ctl_svc");
    // 保留可重试：按钮重新可点。
    expect(
      screen.getByRole("button", { name: /^approve$/i }),
    ).not.toBeDisabled();

    appMocks.AnswerCtlApproval.mockResolvedValueOnce(undefined);
    fireEvent.click(screen.getByRole("button", { name: /^approve$/i }));
    await waitFor(() =>
      expect(appMocks.AnswerCtlApproval).toHaveBeenCalledTimes(2),
    );
  });

  it("shows requests that were queued before it mounted", async () => {
    appMocks.PendingCtlApprovals.mockResolvedValueOnce([updateItem()]);
    render(<ExternalApprovalDialog />);
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText(updateItem().command)).toBeInTheDocument();
  });

  it("keeps a newer pushed snapshot over a late initial fetch", async () => {
    let resolveInitial: (items: Item[]) => void = () => {};
    appMocks.PendingCtlApprovals.mockImplementationOnce(
      () => new Promise<Item[]>((r) => (resolveInitial = r)),
    );
    render(<ExternalApprovalDialog />);
    await waitFor(() => expect(runtimeMocks.EventsOn).toHaveBeenCalled());
    emitQueue([deleteItem()]);
    await act(async () => resolveInitial([updateItem()]));
    expect(screen.getByText(deleteItem().command)).toBeInTheDocument();
    expect(screen.queryByText(updateItem().command)).toBeNull();
  });

  it("closes after a successful answer even before the next snapshot arrives", async () => {
    render(<ExternalApprovalDialog />);
    await waitFor(() => expect(runtimeMocks.EventsOn).toHaveBeenCalled());
    emitQueue([updateItem()]);
    expect(await screen.findByRole("dialog")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /^approve$/i }));

    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("removes the dialog when the executor withdraws the request (timeout)", async () => {
    render(<ExternalApprovalDialog />);
    await waitFor(() => expect(runtimeMocks.EventsOn).toHaveBeenCalled());
    emitQueue([updateItem()]);
    expect(await screen.findByRole("dialog")).toBeInTheDocument();

    // 执行者超时 Withdraw 之后推的快照不再含这条请求。
    emitQueue([]);

    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("sends a system notification for a new arrival while the window is unfocused", async () => {
    focusMocks.isWindowFocused.mockReturnValue(false);
    render(<ExternalApprovalDialog />);
    await waitFor(() => expect(runtimeMocks.EventsOn).toHaveBeenCalled());

    emitQueue([updateItem()]);

    await waitFor(() =>
      expect(appMocks.ShowNotification).toHaveBeenCalledTimes(1),
    );
  });

  it("does not notify when the window is focused", async () => {
    focusMocks.isWindowFocused.mockReturnValue(true);
    render(<ExternalApprovalDialog />);
    await waitFor(() => expect(runtimeMocks.EventsOn).toHaveBeenCalled());

    emitQueue([updateItem()]);

    expect(appMocks.ShowNotification).not.toHaveBeenCalled();
  });
});
