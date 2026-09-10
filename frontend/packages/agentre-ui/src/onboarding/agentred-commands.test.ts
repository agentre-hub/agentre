import { describe, expect, it } from "vitest";

import {
  AGENTRED_DEPLOY_DOC_URL,
  AGENTRED_IMAGE,
  AGENTRED_RELEASES_URL,
  agentredCommands,
  agentredInstallCommand,
  agentredLoginCommand,
  agentredPairCommand,
  agentredVersionCommand,
} from "./agentred-commands";

/**
 * 命令是**跨端唯一的一份**。
 *
 * 在这之前桌面端引导与 agentre-server 的引导各抄了一份安装命令，后者的文件头上
 * 甚至写着「改这里之前先确认那边也改了」——那句提醒本身就是这份模块存在的理由。
 * 两端此后都从这里取，改一处两端一起变。
 */
describe("agentred 接入命令", () => {
  it("类 Unix 走 install.sh，Windows 走 install.ps1，都指向 latest 发布", () => {
    expect(agentredInstallCommand("native", "linux")).toBe(
      `curl -fsSL ${AGENTRED_RELEASES_URL}/download/install.sh | sh`,
    );
    expect(agentredInstallCommand("native", "macos")).toBe(
      agentredInstallCommand("native", "linux"),
    );
    expect(agentredInstallCommand("native", "windows")).toBe(
      `irm ${AGENTRED_RELEASES_URL}/download/install.ps1 | iex`,
    );
  });

  /** 容器方式下宿主系统不进命令——三个系统给的是同一条 docker pull。 */
  it("Docker 方式的安装命令与目标系统无关", () => {
    const commands = (["linux", "macos", "windows"] as const).map((os) =>
      agentredInstallCommand("docker", os),
    );

    expect(new Set(commands).size).toBe(1);
    expect(commands[0]).toBe(`docker pull ${AGENTRED_IMAGE}`);
  });

  it("自证命令按安装方式分岔：原生问二进制，容器问镜像", () => {
    expect(agentredVersionCommand("native")).toBe("agentred --version");
    expect(agentredVersionCommand("docker")).toBe(
      `docker run --rm ${AGENTRED_IMAGE} --version`,
    );
  });

  /**
   * 容器里登录必须挂上 daemon 的状态目录，否则批准之后写下的凭据落在容器里，
   * 而随后 compose 起的那个容器读的是宿主挂进去的目录。
   */
  it("登录命令带上宿主给的服务器地址；容器方式还要挂状态目录", () => {
    expect(agentredLoginCommand("native", "https://console.example.com")).toBe(
      "agentred login --server https://console.example.com",
    );

    const docker = agentredLoginCommand(
      "docker",
      "https://console.example.com",
    );
    expect(docker).toContain("docker run --rm -it");
    expect(docker).toContain("-v ~/.config/agentred:/root/.config/agentred");
    expect(docker).toContain(
      `${AGENTRED_IMAGE} login --server https://console.example.com`,
    );
  });

  /** 容器里的 pair 走 compose exec —— 它要问的是容器内那个 daemon 的本地 IPC。 */
  it("取配对码：原生直接跑，容器要进容器里跑", () => {
    expect(agentredPairCommand("native")).toBe("agentred pair");
    expect(agentredPairCommand("docker")).toBe(
      "docker compose exec agentred agentred pair",
    );
  });

  it("常驻相关的命令两端共用同一组字面量", () => {
    expect(agentredCommands.serviceInstall).toBe(
      "agentred service install --start",
    );
    expect(agentredCommands.serviceStatus).toBe("agentred service status");
    expect(agentredCommands.serviceRestart).toBe("agentred service restart");
    expect(agentredCommands.foregroundRun).toBe("agentred run");
    expect(agentredCommands.composeUp).toBe("docker compose up -d");
    expect(agentredCommands.composePs).toBe("docker compose ps");
    expect(agentredCommands.composeLogs).toBe(
      "docker compose logs -f agentred",
    );
  });

  /** 挂载、端口、network_mode 这些不进界面，靠这条链接把人送到部署文档。 */
  it("两条对外链接指向发布页与部署说明", () => {
    expect(AGENTRED_RELEASES_URL).toBe(
      "https://github.com/agentre-hub/agentre/releases/latest",
    );
    expect(AGENTRED_DEPLOY_DOC_URL).toBe(
      "https://github.com/agentre-hub/agentre/blob/main/deploy/README.md",
    );
  });
});
