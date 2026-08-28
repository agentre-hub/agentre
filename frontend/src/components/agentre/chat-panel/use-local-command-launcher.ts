import * as React from "react";
import { useTranslation } from "react-i18next";
import {
  makeStreamDecoder,
  useTerminalTransport,
  type LocalCommandHistoryScope,
  type TerminalSubscriber,
  type TerminalUnsubscribe,
} from "@agentre-hub/agentre-ui";

import type { ChatSessionDetail } from "@/hooks/use-chat-session";
import { splitErrorDetail } from "@/lib/error-detail";
import {
  localCommandRuntimeStore,
  type LocalCommandRuntimeController,
} from "@/stores/local-command-runtime-store";
import { useLocalCommandsStore } from "@/stores/local-commands-store";

import type { SetChatPanelNotice } from "./notice";

import {
  EnsureChatSession,
  ResolveLocalCommandScope,
  TerminalClose,
  TerminalRunCommand,
} from "../../../../wailsjs/go/app/App";
import type { chat_svc } from "../../../../wailsjs/go/models";

type ChatAgentItem = chat_svc.ChatAgentItem;

const TERMINAL_NOT_OPEN_ERROR = "terminal not open";

function isTerminalNotOpenError(error: unknown): boolean {
  return (
    error === TERMINAL_NOT_OPEN_ERROR ||
    (error instanceof Error && error.message === TERMINAL_NOT_OPEN_ERROR)
  );
}

type UseLocalCommandLauncherOptions = {
  sessionId: number;
  session: ChatSessionDetail | null;
  newSessionAgent?: ChatAgentItem | null;
  /** newSessionContext?.projectId 原样传入(可能 undefined),内部按既有 `?? 0` 兜底。*/
  newSessionProjectId: number | undefined;
  composerCwd: string;
  onSessionCreated?: (sessionId: number, agentId: number) => void;
  onSidebarShouldReload?: () => void;
  setNotice: SetChatPanelNotice;
};

type LocalCommandLauncher = {
  /** composer 命令模式的历史检索范围(设备 + cwd),由后端解析器给出。*/
  localCommandHistoryScope: LocalCommandHistoryScope | undefined;
  handleLocalCommandModeChange: (active: boolean) => void;
  handleStopLocalCommand: (terminalId: string) => Promise<void>;
  runLocalCommand: (
    targetSessionId: number,
    command: string,
  ) => Promise<LocalCommandHistoryScope | undefined>;
};

// useLocalCommandLauncher 收拢「在会话里跑一条本地命令」那一族:PTY 起停、命令卡
// 结算、命令历史检索范围解析,以及未首发新会话上跑命令时的惰性建会话。
function useLocalCommandLauncher({
  sessionId,
  session,
  newSessionAgent,
  newSessionProjectId,
  composerCwd,
  onSessionCreated,
  onSidebarShouldReload,
  setNotice,
}: UseLocalCommandLauncherOptions): LocalCommandLauncher {
  const { t } = useTranslation();
  // 本地命令卡片跑的是一条真 PTY,订阅走终端传输端口(宿主在 App 根挂 Provider)。
  // 它自持订阅表并扇出,所以同一条 PTY 可以同时被终端标签页盯着,谁退订都不连坐。
  const transport = useTerminalTransport();

  const ensuredLocalCommandSessionRef = React.useRef<{
    agentId: number;
    projectId: number;
    promise: Promise<number>;
    requestId: number;
  } | null>(null);
  const ensuredLocalCommandSessionRequestRef = React.useRef(0);
  const handleStopLocalCommand = React.useCallback(
    async (terminalId: string) => {
      const delegated = await localCommandRuntimeStore.stop(terminalId);
      if (
        !delegated &&
        useLocalCommandsStore.getState().get(terminalId)?.status === "running"
      ) {
        console.error(
          "[chat] stop local command failed: runtime controller missing",
          { terminalId },
        );
      }
    },
    [],
  );

  const [localCommandHistoryScope, setLocalCommandHistoryScope] =
    React.useState<LocalCommandHistoryScope>();
  const [localCommandScopeRefreshTick, setLocalCommandScopeRefreshTick] =
    React.useState(0);
  const localCommandScopeResolutionRef = React.useRef(0);
  const commandScopeSessionId = sessionId > 0 ? sessionId : 0;
  const commandScopeAgentId =
    commandScopeSessionId === 0 ? (newSessionAgent?.id ?? 0) : 0;
  const commandScopeProjectId =
    commandScopeSessionId === 0 ? (newSessionProjectId ?? 0) : 0;
  const commandScopeTargetAgentId =
    commandScopeSessionId > 0 ? (session?.agentId ?? 0) : commandScopeAgentId;
  const commandScopeTargetBackendType =
    commandScopeSessionId > 0
      ? (session?.backendType ?? "")
      : (newSessionAgent?.backendType ?? "");
  const commandScopeTargetCwd =
    commandScopeSessionId > 0 ? (session?.cwd ?? "") : composerCwd;
  const commandScopeTargetDeviceId =
    commandScopeSessionId > 0
      ? (session?.deviceID ?? "")
      : (newSessionAgent?.deviceID ?? "");
  const commandScopeTargetProjectId =
    commandScopeSessionId > 0
      ? (session?.projectId ?? 0)
      : commandScopeProjectId;
  React.useLayoutEffect(() => {
    const resolutionID = ++localCommandScopeResolutionRef.current;
    setLocalCommandHistoryScope(undefined);
    if (commandScopeSessionId <= 0 && commandScopeAgentId <= 0) return;

    const request: chat_svc.ResolveLocalCommandScopeRequest = {
      sessionId: commandScopeSessionId,
      agentId: commandScopeAgentId,
      projectId: commandScopeProjectId,
    };
    void (async () => {
      try {
        const scope = await ResolveLocalCommandScope(request);
        if (localCommandScopeResolutionRef.current !== resolutionID) return;
        setLocalCommandHistoryScope({
          deviceId: scope.deviceId,
          cwd: scope.cwd,
        });
      } catch {
        if (localCommandScopeResolutionRef.current !== resolutionID) return;
        setLocalCommandHistoryScope(undefined);
      }
    })();
    return () => {
      if (localCommandScopeResolutionRef.current === resolutionID) {
        localCommandScopeResolutionRef.current += 1;
      }
    };
  }, [
    // These target scalars are refresh signals only. History scope is always
    // the resolver response above, never a frontend-derived device/cwd fallback.
    commandScopeAgentId,
    commandScopeProjectId,
    commandScopeSessionId,
    commandScopeTargetAgentId,
    commandScopeTargetBackendType,
    commandScopeTargetCwd,
    commandScopeTargetDeviceId,
    commandScopeTargetProjectId,
    localCommandScopeRefreshTick,
  ]);
  const handleLocalCommandModeChange = React.useCallback((active: boolean) => {
    if (active) setLocalCommandScopeRefreshTick((tick) => tick + 1);
  }, []);

  const launchLocalCommand = React.useCallback(
    async (
      sid: number,
      command: string,
    ): Promise<LocalCommandHistoryScope | undefined> => {
      const terminalId = crypto.randomUUID();
      const closeRetryInitialDelayMs = 100;
      const closeRetryMaxDelayMs = 5_000;
      // 端口的退订句柄:只摘自己、幂等、同步生效。同一条 PTY 上的终端视图
      // (卡片的“在终端中打开”)与本卡片共用一份扇出,谁走都不影响另一个。
      let releaseObservers: TerminalUnsubscribe | undefined;
      let settled = false;
      let closePromise: Promise<void> | undefined;
      let automaticCloseRequired = false;
      let automaticCloseGuardianTimer: number | undefined;
      let automaticCloseRetryDelayMs = closeRetryInitialDelayMs;
      let userStopRequested = false;
      const clearAutomaticCloseTimer = () => {
        if (automaticCloseGuardianTimer === undefined) return;
        window.clearTimeout(automaticCloseGuardianTimer);
        automaticCloseGuardianTimer = undefined;
      };
      const stopAutomaticCloseGuardian = () => {
        automaticCloseRequired = false;
        clearAutomaticCloseTimer();
      };
      const appendFailure = (error: unknown) => {
        if (settled) return;
        const commands = useLocalCommandsStore.getState();
        if (commands.get(terminalId)?.status === "running") {
          commands.appendOutput(terminalId, String(error));
        }
      };
      const settle = (
        status: "done" | "failed" | "stopped",
        exitCode?: number,
      ) => {
        if (settled) return;
        settled = true;
        stopAutomaticCloseGuardian();
        const commands = useLocalCommandsStore.getState();
        if (commands.get(terminalId)?.status === "running") {
          if (exitCode === undefined) commands.finish(terminalId, status);
          else commands.finish(terminalId, status, exitCode);
        }
        // 退订同步生效且只摘自己:走人之后本卡片再收不到任何帧,不需要“确认清干净”
        // 的重试世代 —— 底层监听撤没撤掉是传输层的事,与本卡片的结算无关。
        releaseObservers?.();
        localCommandRuntimeStore.unregister(terminalId, controller);
      };
      const fail = (error: unknown) => {
        if (settled) return;
        appendFailure(error);
        settle("failed", -1);
      };
      const scheduleAutomaticCloseGuardian = () => {
        if (
          settled ||
          !automaticCloseRequired ||
          automaticCloseGuardianTimer !== undefined
        ) {
          return;
        }
        const retryDelayMs = automaticCloseRetryDelayMs;
        automaticCloseRetryDelayMs = Math.min(
          automaticCloseRetryDelayMs * 2,
          closeRetryMaxDelayMs,
        );
        automaticCloseGuardianTimer = window.setTimeout(() => {
          automaticCloseGuardianTimer = undefined;
          void requestTerminalClose("automatic");
        }, retryDelayMs);
      };
      const requestTerminalClose = (
        ownership: "automatic" | "user",
      ): Promise<void> => {
        if (ownership === "user") {
          userStopRequested = true;
          clearAutomaticCloseTimer();
        }
        if (settled) return Promise.resolve();
        if (closePromise) return closePromise;
        const pending = (async () => {
          let authoritative = false;
          try {
            await TerminalClose(terminalId);
            authoritative = true;
          } catch (error: unknown) {
            if (isTerminalNotOpenError(error)) authoritative = true;
            else if (!automaticCloseRequired) appendFailure(error);
          }
          if (authoritative) {
            if (userStopRequested) settle("stopped");
            else settle("failed", -1);
            return;
          }
          scheduleAutomaticCloseGuardian();
        })();
        closePromise = pending;
        void pending.finally(() => {
          if (closePromise === pending) closePromise = undefined;
        });
        return pending;
      };
      const startAutomaticCloseGuardian = () => {
        if (settled) return;
        if (!automaticCloseRequired) {
          automaticCloseRetryDelayMs = closeRetryInitialDelayMs;
        }
        automaticCloseRequired = true;
        void requestTerminalClose("automatic");
      };
      const decode = makeStreamDecoder();
      const observers: TerminalSubscriber = {
        onData: (bytes) => {
          if (settled) return;
          useLocalCommandsStore
            .getState()
            .appendOutput(terminalId, decode(bytes));
        },
        onExit: ({ code, reason }) => {
          const status =
            reason === "killed" ? "stopped" : code === 0 ? "done" : "failed";
          settle(status, code);
        },
      };
      const controller: LocalCommandRuntimeController = {
        stop: () => requestTerminalClose("user"),
      };
      localCommandRuntimeStore.register(terminalId, controller);
      useLocalCommandsStore.getState().start({
        id: terminalId,
        sessionId: sid,
        command,
        createdAt: Date.now(),
      });
      // 先订阅再起 PTY:第一帧完全可能早于 TerminalRunCommand 兑现。
      // 传输层保证一次失败的 subscribe 不留半截注册,所以重试一次即可,
      // 不需要自持世代 / 清理看门狗。
      let observerError: unknown;
      for (let attempt = 0; attempt < 2 && !releaseObservers; attempt += 1) {
        try {
          releaseObservers = transport.subscribe(terminalId, observers);
        } catch (error: unknown) {
          observerError = error;
        }
      }
      const observersReady = releaseObservers !== undefined;
      try {
        const response = await TerminalRunCommand(
          terminalId,
          sid,
          command,
          80,
          24,
        );
        if (response.startError) fail(response.startError);
        else if (!observersReady) {
          appendFailure(observerError);
          startAutomaticCloseGuardian();
        }
        return {
          deviceId: response.scope.deviceId,
          cwd: response.scope.cwd,
        };
      } catch (error: unknown) {
        if (observersReady) fail(error);
        else {
          appendFailure(error);
          startAutomaticCloseGuardian();
        }
        return undefined;
      }
    },
    [transport],
  );

  React.useEffect(() => {
    if (
      sessionId > 0 ||
      !newSessionAgent ||
      ensuredLocalCommandSessionRef.current?.agentId !== newSessionAgent.id ||
      ensuredLocalCommandSessionRef.current?.projectId !==
        (newSessionProjectId ?? 0)
    ) {
      ensuredLocalCommandSessionRef.current = null;
    }
  }, [newSessionAgent, newSessionProjectId, sessionId]);

  function ensureLocalCommandSession(): Promise<number> {
    if (!newSessionAgent) return Promise.resolve(0);
    const agentId = newSessionAgent.id;
    const projectId = newSessionProjectId ?? 0;
    const current = ensuredLocalCommandSessionRef.current;
    if (current?.agentId === agentId && current.projectId === projectId) {
      return current.promise;
    }

    const requestId = ++ensuredLocalCommandSessionRequestRef.current;
    const promise = (async () => {
      try {
        const sid = await EnsureChatSession(agentId, projectId);
        if (!sid) {
          if (ensuredLocalCommandSessionRef.current?.requestId === requestId) {
            ensuredLocalCommandSessionRef.current = null;
          }
          return 0;
        }
        onSessionCreated?.(sid, agentId);
        onSidebarShouldReload?.();
        return sid;
      } catch (e: unknown) {
        if (ensuredLocalCommandSessionRef.current?.requestId === requestId) {
          ensuredLocalCommandSessionRef.current = null;
        }
        const { msg, detail } = splitErrorDetail(e);
        setNotice({
          kind: "error",
          text: t("chatPanel.errors.send", { msg }),
          detail,
        });
        return 0;
      }
    })();
    ensuredLocalCommandSessionRef.current = {
      agentId,
      projectId,
      promise,
      requestId,
    };
    return promise;
  }

  async function runLocalCommand(
    targetSessionId: number,
    command: string,
  ): Promise<LocalCommandHistoryScope | undefined> {
    let sid = targetSessionId;
    if (!sid) sid = await ensureLocalCommandSession();
    if (!sid) return undefined;
    return launchLocalCommand(sid, command);
  }

  return {
    localCommandHistoryScope,
    handleLocalCommandModeChange,
    handleStopLocalCommand,
    runLocalCommand,
  };
}

export { useLocalCommandLauncher };
export type { LocalCommandLauncher };
