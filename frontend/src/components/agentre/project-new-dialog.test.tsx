import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const appMocks = vi.hoisted(() => ({
  ProjectCreate: vi.fn(),
  ProjectDetectGitRepo: vi.fn(),
  SelectDirectory: vi.fn(),
}));

vi.mock("../../../wailsjs/go/app/App", () => appMocks);

import { ProjectNewDialog } from "./project-new-dialog";

/**
 * 桌面端这一侧的**接缝**（规格 2026-08-22 B 段）。表单本身住在共享包里、在那边测过；
 * 这里只问 wails 那三个绑定翻成 `ProjectCreatePorts` 有没有翻对。
 */

function renderDialog(tree: never[] = []) {
  const onCreated = vi.fn();
  render(
    <ProjectNewDialog
      open
      onOpenChange={vi.fn()}
      tree={tree}
      onCreated={onCreated}
    />,
  );
  return { onCreated };
}

beforeEach(() => {
  vi.clearAllMocks();
  appMocks.ProjectCreate.mockResolvedValue({ id: 42 });
  appMocks.ProjectDetectGitRepo.mockResolvedValue({
    currentBranch: "",
    isGitRepo: false,
    origin: "",
  });
});

describe("接缝", () => {
  it("整套调色板都在，选中的那个原样送进 ProjectCreate", async () => {
    const { onCreated } = renderDialog();

    expect(screen.getAllByRole("button", { name: /^agent-\d+$/ })).toHaveLength(
      16,
    );
    fireEvent.change(screen.getByTestId("project-create-path"), {
      target: { value: "/tmp/nebula" },
    });
    fireEvent.change(screen.getByTestId("project-create-name"), {
      target: { value: "Nebula" },
    });
    fireEvent.click(screen.getByRole("button", { name: "agent-16" }));
    fireEvent.click(screen.getByTestId("project-create-submit"));

    await waitFor(() =>
      expect(appMocks.ProjectCreate).toHaveBeenCalledWith(
        expect.objectContaining({
          color: "agent-16",
          name: "Nebula",
          path: "/tmp/nebula",
          parentID: 0,
        }),
      ),
    );
    // 包交回来的是字符串 id，这一端要的是数字。
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith(42));
  });

  it("没挑颜色 / 图标时 adapter 补上这一端后端要的默认值", async () => {
    renderDialog();
    fireEvent.change(screen.getByTestId("project-create-path"), {
      target: { value: "/tmp/plain" },
    });
    fireEvent.change(screen.getByTestId("project-create-name"), {
      target: { value: "Plain" },
    });
    fireEvent.click(screen.getByTestId("project-create-submit"));

    await waitFor(() =>
      // Project.Check 只认调色板里的颜色，空串会被后端拒。
      expect(appMocks.ProjectCreate).toHaveBeenCalledWith(
        expect.objectContaining({ color: "agent-1", icon: "folder" }),
      ),
    );
  });

  it("本机路径在这一端是必填的 —— 后端建不出没有路径的项目", () => {
    renderDialog();
    fireEvent.change(screen.getByTestId("project-create-name"), {
      target: { value: "Nebula" },
    });
    expect(screen.getByTestId("project-create-submit")).toBeDisabled();
    expect(screen.getByTestId("project-create-path-required")).toBeTruthy();
  });

  it("挑完目录就地探 git，探到的分支标出来", async () => {
    appMocks.SelectDirectory.mockResolvedValue("/tmp/nebula");
    appMocks.ProjectDetectGitRepo.mockResolvedValue({
      currentBranch: "main",
      isGitRepo: true,
      origin: "git@github.com:a/b.git",
    });

    renderDialog();
    fireEvent.click(screen.getByTestId("project-create-browse"));

    await waitFor(() =>
      expect(appMocks.ProjectDetectGitRepo).toHaveBeenCalledWith("/tmp/nebula"),
    );
    expect(
      (await screen.findByTestId("project-create-git")).textContent,
    ).toContain("main");
  });

  it("探测本身抛错时什么都不标 —— 编一个「不是仓库」比不说更糟", async () => {
    appMocks.SelectDirectory.mockResolvedValue("/tmp/nebula");
    appMocks.ProjectDetectGitRepo.mockRejectedValue(new Error("boom"));

    renderDialog();
    fireEvent.click(screen.getByTestId("project-create-browse"));

    await waitFor(() =>
      expect(appMocks.ProjectDetectGitRepo).toHaveBeenCalled(),
    );
    await waitFor(() =>
      expect(screen.queryByTestId("project-create-git")).toBeNull(),
    );
  });

  it("后端拒绝时那句原文就地透出，窗不关、内容不清", async () => {
    appMocks.ProjectCreate.mockRejectedValue("project path does not exist");

    const { onCreated } = renderDialog();
    fireEvent.change(screen.getByTestId("project-create-path"), {
      target: { value: "/tmp/gone" },
    });
    fireEvent.change(screen.getByTestId("project-create-name"), {
      target: { value: "Nebula" },
    });
    fireEvent.click(screen.getByTestId("project-create-submit"));

    expect(
      await screen.findByText(/project path does not exist/),
    ).toBeInTheDocument();
    expect(
      (screen.getByTestId("project-create-name") as HTMLInputElement).value,
    ).toBe("Nebula");
    expect(onCreated).not.toHaveBeenCalled();
  });

  it("挑目录被取消时不当成失败", async () => {
    appMocks.SelectDirectory.mockRejectedValue(new Error("cancelled"));

    renderDialog();
    fireEvent.click(screen.getByTestId("project-create-browse"));

    await waitFor(() => expect(appMocks.SelectDirectory).toHaveBeenCalled());
    expect(
      (screen.getByTestId("project-create-path") as HTMLInputElement).value,
    ).toBe("");
  });
});
