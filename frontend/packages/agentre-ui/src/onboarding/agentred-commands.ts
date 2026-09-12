/**
 * agentred 接入引导用到的每一条命令，**两端唯一的一份**。
 *
 * 在这之前桌面端引导与 agentre-server 的引导各抄了一份，后者的文件头上写着
 * 「与桌面端引导给的是同一条命令；改这里之前先确认那边也改了」——靠人记的同步迟早
 * 漂移，所以命令搬到这里，两端都从这里取。
 *
 * 这里只放**命令本身**。装到了哪、要不要改 PATH、要不要 `loginctl enable-linger`
 * 这些命令自己会印；挂载、端口、`network_mode` 归 `deploy/README.md`。引导不复述
 * 任何一方已经说过的话。
 */

/** 装 agentred 的两条路：直接装二进制，或跑官方镜像。 */
export type AgentredInstallMethod = "native" | "docker";

/** 原生方式下要装到哪个系统上。容器方式与此无关。 */
export type AgentredTargetOS = "linux" | "macos" | "windows";

/** 让它常驻的两种形态。容器方式没有「前台」这回事——compose 起的就是后台。 */
export type AgentredRunMode = "service" | "foreground";

export const AGENTRED_RELEASES_URL =
  "https://github.com/agentre-hub/agentre/releases/latest";

/** 由 .github/workflows/release.yml 的 IMAGE 推送，多架构由 docker pull 自己选。 */
export const AGENTRED_IMAGE = "ghcr.io/agentre-hub/agentred:latest";

export const AGENTRED_DEPLOY_DOC_URL =
  "https://github.com/agentre-hub/agentre/blob/main/deploy/README.md";

/** daemon 状态目录在容器里的位置，登录与 compose 必须挂到同一个宿主目录上。 */
const AGENTRED_STATE_MOUNT = "~/.config/agentred:/root/.config/agentred";

export const agentredCommands = {
  installUnix: `curl -fsSL ${AGENTRED_RELEASES_URL}/download/install.sh | sh`,
  installWindows: `irm ${AGENTRED_RELEASES_URL}/download/install.ps1 | iex`,
  version: "agentred --version",

  dockerPull: `docker pull ${AGENTRED_IMAGE}`,
  dockerVersion: `docker run --rm ${AGENTRED_IMAGE} --version`,

  serviceInstall: "agentred service install --start",
  serviceStatus: "agentred service status",
  serviceRestart: "agentred service restart",
  foregroundRun: "agentred run",

  composeUp: "docker compose up -d",
  composePs: "docker compose ps",
  composeLogs: "docker compose logs -f agentred",

  pair: "agentred pair",
  dockerPair: "docker compose exec agentred agentred pair",
} as const;

export function agentredInstallCommand(
  method: AgentredInstallMethod,
  os: AgentredTargetOS,
): string {
  if (method === "docker") return agentredCommands.dockerPull;
  return os === "windows"
    ? agentredCommands.installWindows
    : agentredCommands.installUnix;
}

export function agentredVersionCommand(method: AgentredInstallMethod): string {
  return method === "docker"
    ? agentredCommands.dockerVersion
    : agentredCommands.version;
}

/**
 * 服务器地址由宿主给（控制台给的是它自己的 origin），这里不写死任何域名——自建部署
 * 照抄一条写死的地址只会连不上，而错误要等到那台机器上才暴露。
 */
export function agentredLoginCommand(
  method: AgentredInstallMethod,
  serverURL: string,
): string {
  if (method === "native") return `agentred login --server ${serverURL}`;
  return [
    "docker run --rm -it \\",
    `  -v ${AGENTRED_STATE_MOUNT} \\`,
    `  ${AGENTRED_IMAGE} login --server ${serverURL}`,
  ].join("\n");
}

/** 容器里的 daemon 只听容器内的本地 IPC，所以取码得进到容器里跑。 */
export function agentredPairCommand(method: AgentredInstallMethod): string {
  return method === "docker"
    ? agentredCommands.dockerPair
    : agentredCommands.pair;
}
