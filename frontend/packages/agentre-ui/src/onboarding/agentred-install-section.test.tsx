import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { AgentredInstallSection } from "./agentred-install-section";
import {
  AGENTRED_DEPLOY_DOC_URL,
  AGENTRED_IMAGE,
  AGENTRED_RELEASES_URL,
} from "./agentred-commands";

function renderSection(
  over: Partial<React.ComponentProps<typeof AgentredInstallSection>> = {},
) {
  const onMethodChange = vi.fn();
  const onOsChange = vi.fn();
  const view = render(
    <AgentredInstallSection
      method="native"
      onMethodChange={onMethodChange}
      os="linux"
      onOsChange={onOsChange}
      {...over}
    />,
  );
  return { ...view, onMethodChange, onOsChange };
}

const commandsOf = () =>
  screen.getAllByRole("group").map((card) => card.textContent ?? "");

/**
 * 安装这一段两端一字不差，所以归包：选安装方式、（原生时）选系统、给出安装命令与
 * 一条自证命令。**界面只给要执行的命令** —— 装到了哪、要不要改 PATH 这些
 * `install.sh` 自己就会印，不在这里复述。
 */
describe("AgentredInstallSection", () => {
  it("原生方式给安装命令与二进制自证", () => {
    renderSection();

    const text = commandsOf().join("\n");
    expect(text).toContain(
      `curl -fsSL ${AGENTRED_RELEASES_URL}/download/install.sh | sh`,
    );
    expect(text).toContain("agentred --version");
  });

  it("换到 Windows 换成 PowerShell 那条，并把选择交回宿主", () => {
    const { onOsChange } = renderSection();

    fireEvent.click(screen.getByRole("button", { name: "Windows" }));
    expect(onOsChange).toHaveBeenCalledWith("windows");

    renderSection({ os: "windows" });
    expect(commandsOf().join("\n")).toContain(
      `irm ${AGENTRED_RELEASES_URL}/download/install.ps1 | iex`,
    );
  });

  it("Docker 方式给拉镜像与镜像自证", () => {
    renderSection({ method: "docker" });

    const text = commandsOf().join("\n");
    expect(text).toContain(`docker pull ${AGENTRED_IMAGE}`);
    expect(text).toContain(`docker run --rm ${AGENTRED_IMAGE} --version`);
  });

  /** 容器方式下宿主系统不改变任何命令，留着那排按钮只会让人以为它有用。 */
  it("Docker 方式不出现系统选择器", () => {
    renderSection({ method: "docker" });

    expect(screen.queryByRole("button", { name: "Linux" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Windows" })).toBeNull();
  });

  it("切换安装方式交回宿主", () => {
    const { onMethodChange } = renderSection();

    fireEvent.click(screen.getByRole("button", { name: /docker/i }));

    expect(onMethodChange).toHaveBeenCalledWith("docker");
  });

  it("选中的方式与系统以 aria-pressed 表达，不只靠颜色", () => {
    renderSection({ method: "docker" });

    expect(screen.getByRole("button", { name: /docker/i })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(
      screen.getByRole("button", { name: /native binary/i }),
    ).toHaveAttribute("aria-pressed", "false");
  });
});

/**
 * 挂载、端口、`network_mode` 这些随部署形态变化的内容不进界面，由这条链接把人送到
 * `deploy/README.md`；原生方式下它换成发布页。
 */
describe("AgentredInstallDocsLink", () => {
  it("原生指向发布页，Docker 指向部署说明", async () => {
    const { AgentredInstallDocsLink } =
      await import("./agentred-install-section");

    const view = render(<AgentredInstallDocsLink method="native" />);
    expect(screen.getByRole("link")).toHaveAttribute(
      "href",
      AGENTRED_RELEASES_URL,
    );

    view.rerender(<AgentredInstallDocsLink method="docker" />);
    expect(screen.getByRole("link")).toHaveAttribute(
      "href",
      AGENTRED_DEPLOY_DOC_URL,
    );
  });
});
