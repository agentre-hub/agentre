import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const appMocks = vi.hoisted(() => ({
  ProjectDelete: vi.fn(),
  ProjectLocationList: vi.fn(),
  RemoteDeviceList: vi.fn(),
}));

vi.mock("../../../wailsjs/go/app/App", () => appMocks);

import { DeleteProjectDialog } from "./delete-project-dialog";

/**
 * 桌面端这一侧的**接缝**（规格 2026-08-22 B 段，决策 8）。确认弹窗本身住在共享包里；
 * 这里只问：`ProjectDelete` 接对了没有，以及「哪几台机器此刻离线」这份宿主数据算对了没有。
 */

function renderDialog(
  target: { id: number; name: string; childCount?: number } | null,
  onDeleted = vi.fn(),
) {
  render(
    <DeleteProjectDialog
      target={target}
      onClose={vi.fn()}
      onDeleted={onDeleted}
    />,
  );
  return { onDeleted };
}

beforeEach(() => {
  vi.clearAllMocks();
  appMocks.ProjectDelete.mockResolvedValue(undefined);
  appMocks.ProjectLocationList.mockResolvedValue([]);
  appMocks.RemoteDeviceList.mockResolvedValue([]);
});

describe("接缝", () => {
  it("没有目标就什么都不渲染", () => {
    renderDialog(null);
    expect(screen.queryByTestId("delete-project-submit")).toBeNull();
  });

  it("名字输对才放行，删的是那个数字 id", async () => {
    const { onDeleted } = renderDialog({ id: 7, name: "Nebula" });

    const submit = screen.getByTestId("delete-project-submit");
    expect(submit).toBeDisabled();
    fireEvent.change(screen.getByTestId("delete-project-confirm"), {
      target: { value: "wrong" },
    });
    expect(submit).toBeDisabled();

    fireEvent.change(screen.getByTestId("delete-project-confirm"), {
      target: { value: "Nebula" },
    });
    fireEvent.click(submit);

    await waitFor(() => expect(appMocks.ProjectDelete).toHaveBeenCalledWith(7));
    await waitFor(() => expect(onDeleted).toHaveBeenCalledTimes(1));
  });

  it("后端拒绝时那句原文就地透出，onDeleted 不发", async () => {
    appMocks.ProjectDelete.mockRejectedValue(new Error("has active sessions"));
    const { onDeleted } = renderDialog({ id: 7, name: "Nebula" });

    fireEvent.change(screen.getByTestId("delete-project-confirm"), {
      target: { value: "Nebula" },
    });
    fireEvent.click(screen.getByTestId("delete-project-submit"));

    expect(await screen.findByText(/has active sessions/)).toBeInTheDocument();
    expect(onDeleted).not.toHaveBeenCalled();
  });

  it("配了这个项目又离线的那几台点名说出来；没配的那台不算", async () => {
    appMocks.ProjectLocationList.mockResolvedValue([
      { deviceId: "42", path: "/srv/a" },
    ]);
    appMocks.RemoteDeviceList.mockResolvedValue([
      { id: 42, name: "build-01", online: false },
      // 离线但没配这个项目 —— 它不跟着删，不该出现在这句话里。
      { id: 43, name: "build-02", online: false },
    ]);

    renderDialog({ id: 7, name: "Nebula" });

    await waitFor(() => {
      const line =
        screen.getByTestId("delete-project-offline").textContent ?? "";
      expect(line).toContain("build-01");
      expect(line).not.toContain("build-02");
    });
  });

  it("读不上来那份名单时说「都在线」，不凭空说有一批看不见的机器", async () => {
    appMocks.ProjectLocationList.mockRejectedValue(new Error("boom"));

    renderDialog({ id: 7, name: "Nebula" });

    await waitFor(() =>
      expect(
        screen.getByTestId("delete-project-offline").textContent,
      ).toBeTruthy(),
    );
    expect(
      screen.getByTestId("delete-project-offline").textContent,
    ).not.toContain("build-");
  });

  it("子项目数由调用方从树上数出来递进去", () => {
    renderDialog({ id: 7, name: "Nebula", childCount: 3 });
    expect(screen.getByTestId("delete-project-children").textContent).toContain(
      "3",
    );
  });
});
