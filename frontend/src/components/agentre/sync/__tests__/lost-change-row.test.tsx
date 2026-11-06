import "@testing-library/jest-dom/vitest";

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));
vi.mock("sonner", () => sonnerMocks);

import { LostChangeRow } from "../lost-change-row";
import type { LostChangeView } from "../use-sync-status";

const NOW = 1_700_000_000_000;

function overwrittenRow(originDevice: string): LostChangeView {
  return {
    id: 1,
    entityType: "department",
    entitySyncID: "sync-dept-1",
    reason: "overwritten",
    payloadJSON: JSON.stringify({ name: "工程" }),
    originDevice: originDevice,
    baseVersion: 3,
    occurredAt: NOW - 60_000,
  };
}

function renderRow(row: LostChangeView) {
  return render(
    <LostChangeRow
      row={row}
      expanded={false}
      onToggleExpand={() => {}}
      onRestore={() =>
        Promise.resolve({ Restored: false, TargetDeleted: true })
      }
      onRecreate={() => Promise.resolve()}
      onDiscard={() => Promise.resolve()}
      now={NOW}
    />,
  );
}

/**
 * 覆盖方是**服务端**时的呈现(规格 2026-08-18「server 端的组织管理面」)。
 *
 * server 的组织管理面让浏览器直接建 / 改 / 删组织架构,那些行的 SourceDeviceID
 * 记 0,冲突应答因此回传 0。这一格在 JavaScript 里是**字符串** `"0"` —— 它是真值,
 * 会照常走进「来自「{{device}}」」那一支,把覆盖方说成不存在的「设备 0」。
 * 落库过的旧行同样留着 `"0"`(source_device_id 建列时 DEFAULT 0 且没有回填),
 * 所以这两种写法都要译成同一句话。
 */
describe("LostChangeRow 的覆盖来源", () => {
  it.each(["0", "server"])(
    "把来源 %s 说成「服务端(浏览器)」,不是设备 0 也不是空白",
    (origin) => {
      renderRow(overwrittenRow(origin));

      expect(
        screen.getByText(/from "the server \(browser\)"/),
      ).toBeInTheDocument();
      expect(screen.queryByText(/from "0"/)).not.toBeInTheDocument();
      expect(screen.queryByText(/from "server"/)).not.toBeInTheDocument();
    },
  );

  it("真的来自某台设备时照旧显示那台设备", () => {
    renderRow(overwrittenRow("Work Mac mini"));

    expect(screen.getByText(/from "Work Mac mini"/)).toBeInTheDocument();
  });

  // 「已被删除」那一支复用同一个来源格:恢复被拒之后的说明里同样不能出现设备 0。
  it("恢复被拒后的说明里同样译成「服务端(浏览器)」", async () => {
    const user = userEvent.setup();
    render(
      <LostChangeRow
        row={overwrittenRow("0")}
        expanded
        onToggleExpand={() => {}}
        onRestore={() =>
          Promise.resolve({ Restored: false, TargetDeleted: true })
        }
        onRecreate={() => Promise.resolve()}
        onDiscard={() => Promise.resolve()}
        now={NOW}
      />,
    );

    await user.click(screen.getByRole("button", { name: /restore/i }));

    expect(
      await screen.findByText(/deleted on "the server \(browser\)"/),
    ).toBeInTheDocument();
  });
});

// S5：同步写失败说中文事实 + 对应出口，不把 String(e) 当正文。
describe("LostChangeRow 失败的呈现", () => {
  function renderFailing(onRestore: () => Promise<never>) {
    sonnerMocks.toast.error.mockClear();
    render(
      <LostChangeRow
        row={overwrittenRow("Work Mac mini")}
        expanded
        onToggleExpand={() => {}}
        onRestore={onRestore}
        onRecreate={() => Promise.resolve()}
        onDiscard={() => Promise.resolve()}
        now={NOW}
      />,
    );
    return userEvent.setup();
  }

  it("连不上同步服务时说连不上，并给「重试」把同一次恢复再跑一遍", async () => {
    const onRestore = vi
      .fn()
      .mockRejectedValue("agentre-code:20301 Server unreachable");
    const user = renderFailing(onRestore);

    await user.click(screen.getByRole("button", { name: /restore/i }));

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    const [title, options] = sonnerMocks.toast.error.mock.calls[0];
    expect(title).toBe("Restore failed");
    const { container } = render(<>{options.description}</>);
    expect(container).toHaveTextContent(
      "Can't reach the sync server. Try again once the network is back.",
    );
    expect(options.action.label).toBe("Retry");
    options.action.onClick();
    await waitFor(() => expect(onRestore).toHaveBeenCalledTimes(2));
  });

  // 规格 S5「unknown：中文标题 + 折叠详情」：原话不当正文，但也不丢 —— 进默认折叠的详情。
  it("没有码的失败不把原话当正文，原话进默认折叠的详情", async () => {
    const user = renderFailing(
      vi.fn().mockRejectedValue(new Error("rpc error: code = Unavailable")),
    );

    await user.click(screen.getByRole("button", { name: /restore/i }));

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    const [, options] = sonnerMocks.toast.error.mock.calls[0];
    const { container } = render(<>{options.description}</>);
    const body = container.querySelector("p");
    expect(body).toHaveTextContent(
      "That didn't go through. You can try again.",
    );
    expect(body).not.toHaveTextContent("rpc error");
    const details = container.querySelector("details");
    expect(details).not.toBeNull();
    expect(details).not.toHaveAttribute("open");
    expect(details).toHaveTextContent("rpc error: code = Unavailable");
  });
});
