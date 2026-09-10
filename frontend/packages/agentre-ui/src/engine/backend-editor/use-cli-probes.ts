// CLI 引擎在「目标机」$PATH 里的探测：新建时对三个 CLI 引擎各探一次给类型选择器
// 上徽标，以及 CLI 路径字段旁那颗手动「自动识别」。两条路共用同一个后端调用，
// 区别只在错误怎么呈现。
import * as React from "react";

import { agent_backend_svc } from "../port-bridge";
import type { EngineSettingsBridge } from "../port-bridge";
import {
  cliBinaryName,
  isCliBackend,
  type BackendType,
  type CLIProbe,
  type Translate,
} from "../agent-backends-shared";

import type { EditorState } from "./editor-types";

// 打开新建对话框时会对这三个类型各探一次目标机的 $PATH。
const CLI_BACKEND_TYPES: BackendType[] = ["claudecode", "codex", "piagent"];

// 在「目标机」的 $PATH 里找 t 对应的可执行文件。deviceId 空串 = 本机，
// 非空 = 让 agent_backend_svc 派发到那台远端 daemon 去扫它自己的 $PATH。
// 远端不可达时这里会 throw，交给调用方决定怎么呈现。
function probeCLIPath(
  resolve: EngineSettingsBridge["ResolveAgentBackendCLIPath"],
  t: BackendType,
  deviceId: string,
) {
  return resolve({
    type: t,
    deviceId,
  } as unknown as agent_backend_svc.ResolveCLIPathRequest);
}

export type CliProbes = ReturnType<typeof useCliProbes>;

export function useCliProbes(args: {
  stateKind: EditorState["kind"];
  initialCliPath: string;
  type: BackendType;
  deviceId: string;
  resolveCliPath: EngineSettingsBridge["ResolveAgentBackendCLIPath"];
  t: Translate;
}) {
  const { stateKind, type, deviceId, resolveCliPath, t } = args;
  const [cliPath, setCliPath] = React.useState(args.initialCliPath);
  const [cliProbing, setCliProbing] = React.useState(false);
  // 「$PATH 没挂到 binary」的提示文案；命中后清空。
  const [cliProbeMiss, setCliProbeMiss] = React.useState<string | null>(null);
  // 类型选择器右侧徽标的数据源：三个 CLI 引擎在目标机 $PATH 里的探测结果。
  const [cliProbes, setCliProbes] = React.useState<
    Partial<Record<BackendType, CLIProbe>>
  >({});
  const cliProbeGenerationRef = React.useRef(0);

  // detectCLIPath 调后端 ResolveAgentBackendCLIPath；非 CLI 类型直接返回 null。
  // 选了远端 device 时把 deviceId 一起传过去，让 agent_backend_svc 按 device 派发到远端 daemon。
  // 注意：远端调用可能 throw（设备离线 / 超时 / 探测失败），调用方需要自行决定要不要兜底。
  // - handleTypeChange 的隐式自动填：用 .catch(() => undefined) 静默吞错，避免打扰新建流程
  // - handleDetectCli 的显式按钮：catch 后落到 cliProbeMiss 文案槽
  async function detectCLIPath(
    nextType: BackendType,
    dev: string = "",
  ): Promise<string | null> {
    if (!isCliBackend(nextType)) return null;
    const r = await probeCLIPath(resolveCliPath, nextType, dev);
    return r.found ? r.path : null;
  }

  // 新建时对三个 CLI 引擎各探一次目标机的 $PATH，让「装没装」在选类型这一步就可见，
  // 而不是选完之后才在 CLI 路径字段撞墙。换运行设备（本机 ↔ 远端）要整组重探。
  // 探测不阻塞选择：飞行中的类型照样可以点。
  React.useEffect(() => {
    if (stateKind !== "create") return;
    const generation = ++cliProbeGenerationRef.current;
    setCliProbes(
      Object.fromEntries(
        CLI_BACKEND_TYPES.map((cliType) => [
          cliType,
          { state: "probing", path: "" },
        ]),
      ) as Partial<Record<BackendType, CLIProbe>>,
    );
    for (const cliType of CLI_BACKEND_TYPES) {
      void (async () => {
        let probe: CLIProbe;
        try {
          const r = await probeCLIPath(resolveCliPath, cliType, deviceId);
          probe = {
            state: r.found ? "installed" : "missing",
            path: r.found ? r.path : "",
          };
        } catch {
          // 远端离线 / 超时 / 探测报错：只能说「没探到」，不能说「没装」。
          probe = { state: "failed", path: "" };
        }
        // 换设备后旧一轮的迟到结果直接丢弃，避免徽标显示上一台机器的结论。
        if (cliProbeGenerationRef.current !== generation) return;
        setCliProbes((prev) => ({ ...prev, [cliType]: probe }));
      })();
    }
  }, [stateKind, deviceId, resolveCliPath]);

  // 手动「自动识别」按钮：无论命中与否都给用户视觉反馈。命中时覆盖当前值。
  async function handleDetectCli() {
    if (cliProbing) return;
    setCliProbing(true);
    setCliProbeMiss(null);
    try {
      const path = await detectCLIPath(type, deviceId);
      if (path) {
        setCliPath(path);
      } else {
        setCliProbeMiss(
          t("agentBackends.cli.notFound", { bin: cliBinaryName(type) }),
        );
      }
    } catch (e) {
      // 远端报错（设备离线 / 超时 / 探测失败）也要给用户反馈，避免 unhandled promise rejection。
      setCliProbeMiss(e instanceof Error ? e.message : String(e));
    } finally {
      setCliProbing(false);
    }
  }

  return {
    cliPath,
    setCliPath,
    cliProbing,
    cliProbeMiss,
    setCliProbeMiss,
    cliProbes,
    detectCLIPath,
    handleDetectCli,
  };
}
