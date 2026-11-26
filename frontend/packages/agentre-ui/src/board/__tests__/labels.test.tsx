import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { LabelManagerPanel } from "../label-manager";
import { ISSUE_TONES } from "../types";
import type { LabelUsageView } from "../query-types";

const LABELS: LabelUsageView[] = [
  { id: 1, name: "bug", tone: "red", usageCount: 3 },
  { id: 2, name: "docs", tone: "gray", usageCount: 0 },
];

function renderPanel(
  over: Partial<React.ComponentProps<typeof LabelManagerPanel>> = {},
) {
  const onLabelMutate = vi.fn().mockResolvedValue(undefined);
  render(
    <LabelManagerPanel
      labels={LABELS}
      onLabelMutate={onLabelMutate}
      {...over}
    />,
  );
  return { onLabelMutate, user: userEvent.setup() };
}

describe("标签管理", () => {
  it("Given the label list, When it renders, Then every row says how many tasks use it", () => {
    renderPanel();

    expect(screen.getByTestId("label-usage-1")).toHaveTextContent("3");
    expect(screen.getByTestId("label-usage-2")).toHaveTextContent("0");
  });

  it("Given a rename, When it is confirmed, Then the label keeps its tone and travels with the new name", async () => {
    const { onLabelMutate, user } = renderPanel();

    await user.click(screen.getByTestId("label-rename-1"));
    const input = screen.getByTestId("label-name-input-1");
    await user.clear(input);
    await user.type(input, "defect");
    await user.click(screen.getByTestId("label-rename-confirm-1"));

    expect(onLabelMutate).toHaveBeenCalledWith({
      kind: "update",
      id: 1,
      name: "defect",
      tone: "red",
    });
  });

  it("Given an existing label, When its tone is changed, Then the new tone travels with the same name", async () => {
    const { onLabelMutate, user } = renderPanel();

    await user.click(screen.getByTestId("label-rename-1"));
    // 换色是用户故事 4 的一半：改名、换色、新建、删除四件事都要有出口。
    await user.click(screen.getByTestId("label-1-tone-violet"));
    await user.click(screen.getByTestId("label-rename-confirm-1"));

    expect(onLabelMutate).toHaveBeenCalledWith({
      kind: "update",
      id: 1,
      name: "bug",
      tone: "violet",
    });
  });

  it("Given a delete, When it is asked for, Then the blast radius is stated before anything happens", async () => {
    const { onLabelMutate, user } = renderPanel();

    await user.click(screen.getByTestId("label-delete-1"));

    // 软删对使用者不可逆，所以先说清它会从多少个任务上消失。
    expect(screen.getByTestId("label-delete-warning-1")).toHaveTextContent("3");
    expect(onLabelMutate).not.toHaveBeenCalled();

    await user.click(screen.getByTestId("label-delete-confirm-1"));
    expect(onLabelMutate).toHaveBeenCalledWith({ kind: "delete", id: 1 });
  });

  it("Given the delete is called off, When cancel is pressed, Then nothing is mutated", async () => {
    const { onLabelMutate, user } = renderPanel();

    await user.click(screen.getByTestId("label-delete-2"));
    await user.click(screen.getByTestId("label-delete-cancel-2"));

    expect(screen.queryByTestId("label-delete-warning-2")).toBeNull();
    expect(onLabelMutate).not.toHaveBeenCalled();
  });

  it("Given the palette, When a new label is created, Then exactly the eight design tones are on offer", async () => {
    const { onLabelMutate, user } = renderPanel();

    const palette = screen.getByTestId("label-palette");
    expect(within(palette).getAllByRole("radio")).toHaveLength(
      ISSUE_TONES.length,
    );

    await user.type(screen.getByTestId("label-new-name"), "chore");
    await user.click(screen.getByTestId("label-tone-violet"));
    await user.click(screen.getByTestId("label-create"));

    expect(onLabelMutate).toHaveBeenCalledWith({
      kind: "create",
      name: "chore",
      tone: "violet",
    });
  });

  it("Given no name, When create is pressed, Then nothing is sent", async () => {
    const { onLabelMutate, user } = renderPanel();

    await user.click(screen.getByTestId("label-create"));

    expect(onLabelMutate).not.toHaveBeenCalled();
  });
});

// 写没过去时，面板此前会照常收起编辑行、清掉刚敲的名字，一句话都不说——用户看到
// 的是「面板复位、什么都没发生」。
describe("标签写入失败", () => {
  it("Given a create that rejects, When it settles, Then the typed name survives and the reason is shown", async () => {
    const onLabelMutate = vi.fn().mockRejectedValue(new Error("重名了"));
    render(<LabelManagerPanel labels={LABELS} onLabelMutate={onLabelMutate} />);
    const user = userEvent.setup();

    await user.type(screen.getByTestId("label-new-name"), "gateway");
    await user.click(screen.getByTestId("label-create"));

    expect(await screen.findByTestId("label-manager-error")).toHaveTextContent(
      "重名了",
    );
    expect(screen.getByTestId("label-new-name")).toHaveValue("gateway");
  });

  // 宿主接住了报错、只回一个「没过去」时（桌面端的四条写路径刻意都不 reject），
  // 面板同样不能当成功处理。
  it("Given a host that resolves false, When a rename is confirmed, Then the row stays open", async () => {
    const onLabelMutate = vi.fn().mockResolvedValue(false);
    render(<LabelManagerPanel labels={LABELS} onLabelMutate={onLabelMutate} />);
    const user = userEvent.setup();

    await user.click(screen.getByTestId("label-rename-1"));
    await user.click(screen.getByTestId("label-rename-confirm-1"));

    expect(
      await screen.findByTestId("label-manager-error"),
    ).toBeInTheDocument();
    expect(screen.getByTestId("label-name-input-1")).toBeInTheDocument();
  });

  it("Given a create that goes through, When it settles, Then the name box is cleared and no error is shown", async () => {
    const onLabelMutate = vi.fn().mockResolvedValue(undefined);
    render(<LabelManagerPanel labels={LABELS} onLabelMutate={onLabelMutate} />);
    const user = userEvent.setup();

    await user.type(screen.getByTestId("label-new-name"), "gateway");
    await user.click(screen.getByTestId("label-create"));

    await waitFor(() =>
      expect(screen.getByTestId("label-new-name")).toHaveValue(""),
    );
    expect(screen.queryByTestId("label-manager-error")).toBeNull();
  });
});

describe("F5 新建标签超时：刷新看看", () => {
  const timeoutErr = () => {
    const e = new Error("request timed out after 15000ms");
    e.name = "ApiTimeoutError";
    return e;
  };
  async function createWith(
    over: Partial<React.ComponentProps<typeof LabelManagerPanel>>,
  ) {
    const onLabelMutate = vi.fn().mockRejectedValue(timeoutErr());
    const user = userEvent.setup();
    render(
      <LabelManagerPanel
        labels={LABELS}
        onLabelMutate={onLabelMutate}
        {...over}
      />,
    );
    await user.type(screen.getByTestId("label-new-name"), "perf");
    await user.click(screen.getByTestId("label-create"));
    return user;
  }

  it("给了 onCreateTimeoutRecheck：一行中文 + 刷新看看，原文不出现；找到 -> 清空输入", async () => {
    const recheck = vi.fn().mockResolvedValue(true);
    const user = await createWith({ onCreateTimeoutRecheck: recheck });
    const alert = await screen.findByTestId("label-manager-error");
    expect(alert).toHaveTextContent(
      "The network timed out. It may already have been created.",
    );
    expect(alert.textContent).not.toMatch(/timed out after/);
    await user.click(
      within(alert).getByRole("button", { name: "Refresh to check" }),
    );
    expect(recheck).toHaveBeenCalledWith("perf");
    await waitFor(() =>
      expect(screen.queryByTestId("label-manager-error")).toBeNull(),
    );
    expect(screen.getByTestId("label-new-name")).toHaveValue("");
  });

  it("没找到：保留输入、说一句、允许再提交", async () => {
    const user = await createWith({
      onCreateTimeoutRecheck: vi.fn().mockResolvedValue(false),
    });
    await user.click(
      await screen.findByRole("button", { name: "Refresh to check" }),
    );
    expect(await screen.findByTestId("label-manager-error")).toHaveTextContent(
      "Not found yet. You can submit again.",
    );
    expect(screen.getByTestId("label-new-name")).toHaveValue("perf");
    expect(screen.getByTestId("label-create")).toBeEnabled();
  });

  it("刷新本身失败：仍是「不知道建没建」——超时那一行与「刷新看看」留着、说一句没刷新成，不放开再提交", async () => {
    const recheck = vi.fn().mockRejectedValue(new Error("offline"));
    const user = await createWith({ onCreateTimeoutRecheck: recheck });
    await user.click(
      await screen.findByRole("button", { name: "Refresh to check" }),
    );
    const alert = await screen.findByTestId("label-manager-error");
    await waitFor(() =>
      expect(alert).toHaveTextContent(
        "Couldn't refresh. Try again in a moment.",
      ),
    );
    expect(alert.textContent).not.toMatch(/submit again/);
    expect(
      within(alert).getByRole("button", { name: "Refresh to check" }),
    ).toBeEnabled();
  });

  it("超时后没刷新就直接再点新建：先核实，不发第二次新建", async () => {
    const recheck = vi.fn().mockResolvedValue(true);
    const onLabelMutate = vi.fn().mockRejectedValue(timeoutErr());
    const user = userEvent.setup();
    render(
      <LabelManagerPanel
        labels={LABELS}
        onLabelMutate={onLabelMutate}
        onCreateTimeoutRecheck={recheck}
      />,
    );
    await user.type(screen.getByTestId("label-new-name"), "perf");
    await user.click(screen.getByTestId("label-create"));
    await screen.findByRole("button", { name: "Refresh to check" });

    await user.click(screen.getByTestId("label-create"));

    await waitFor(() => expect(recheck).toHaveBeenCalledWith("perf"));
    expect(onLabelMutate).toHaveBeenCalledTimes(1);
    await waitFor(() =>
      expect(screen.getByTestId("label-new-name")).toHaveValue(""),
    );
  });

  it("不传：超时和从前一样显示原文", async () => {
    await createWith({});
    expect(await screen.findByTestId("label-manager-error")).toHaveTextContent(
      /timed out after/,
    );
  });
});
