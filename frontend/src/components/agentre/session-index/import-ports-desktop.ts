// frontend/src/components/agentre/session-index/import-ports-desktop.ts
//
// 桌面端这一侧的「导入本地会话」适配器（规格 2026-08-26）。
//
// 共享包只认 `SessionImportPorts` 里那几件事，不认识 Wails 绑定；这里把它接到
// `ListImportCandidates` / `PreviewImportTranscript` / `ImportLocalSession` 与
// `chat-import:progress` 事件上。id 在包里一律是字符串，在这边是 int64，转换只在
// 这一处发生 —— 与 `engine-ports-desktop.ts` / `transcript-ports-desktop.ts` 同一条路子。
//
// **可选 port 不声明就是没有那个入口**：进度、取消与目录选择这三件桌面端都接得上
// （`chat-import:progress` 事件 + `CancelLocalSessionImport` + `SelectDirectory`），
// 所以都声明。
import * as React from "react";
import { useTranslation } from "react-i18next";
import type {
  ImportCandidatesResult,
  ImportPreviewResult,
  SessionImportPorts,
} from "@agentre-hub/agentre-ui";

import { useChatAgentsStore } from "@/stores/chat-agents-store";

import {
  CancelLocalSessionImport,
  ImportLocalSession,
  ListImportCandidates,
  PreviewImportTranscript,
  SelectDirectory,
} from "../../../../wailsjs/go/app/App";
import { EventsOn } from "../../../../wailsjs/runtime/runtime";

import { useMachineRoster } from "./machine-roster";

/** 与 `internal/app/chat_import.go` 的 ChatImportProgressEvent 同值。 */
const PROGRESS_EVENT = "chat-import:progress";

/**
 * 组装桌面端的 ports。
 *
 * `enabled` 为假时不拉设备清单 —— 索引页常驻渲染，导入对话框没开就不该为它多发
 * 一个 RPC（与 `useMachineRoster` 在机器轴之外不拉是同一条规矩）。
 */
export function useSessionImportPorts(
  enabled: boolean,
  openSession: (sessionID: number) => void,
): SessionImportPorts {
  const { t } = useTranslation();
  const machines = useMachineRoster(enabled, t("sessionIndex.machine.local"));
  const agents = useChatAgentsStore((s) => s.agents);

  const devices = React.useMemo(
    () =>
      machines.map((m) => ({
        id: String(m.deviceId),
        name: m.name,
        online: m.online,
        local: m.deviceId === 0,
      })),
    [machines],
  );

  const agentOptions = React.useMemo(
    () =>
      agents
        // 接不住下一轮的 agent 列出来也只是个选了跑不动的选项。
        .filter((a) => a.chattable)
        .map((a) => ({
          id: String(a.id),
          name: a.name,
          color: a.avatarColor,
          backend: a.backendType,
        })),
    [agents],
  );

  return React.useMemo<SessionImportPorts>(
    () => ({
      devices,
      agents: agentOptions,
      listCandidates: async (req): Promise<ImportCandidatesResult> => {
        const resp = await ListImportCandidates({
          deviceId: Number(req.deviceId),
          backends: req.backends,
          cwdPrefix: req.cwdPrefix,
          titleQuery: req.titleQuery,
          since: 0,
          limit: 0,
        } as never);
        return {
          candidates: (resp.candidates ?? []).map((c) => ({
            backend: c.backend,
            providerSessionId: c.providerSessionId,
            title: c.title,
            cwd: c.cwd,
            startedAt: c.startedAt,
            endedAt: c.endedAt,
            turns: c.turns,
            origin: c.origin,
            locator: c.locator,
            imported: c.imported,
            importedSessionId: c.importedSessionId
              ? String(c.importedSessionId)
              : "",
          })),
          issues: (resp.issues ?? []).map((i) => ({
            backend: i.backend,
            // 后端只出 "unavailable" 这一档，且保证永不为空；真的空了也当「答不出」
            // 而不是当没事发生。
            status: "unavailable" as const,
            reason: i.reason,
          })),
        };
      },
      preview: async (req): Promise<ImportPreviewResult> => {
        const resp = await PreviewImportTranscript({
          deviceId: Number(req.deviceId),
          backend: req.backend,
          locator: req.locator,
          turns: 0,
        } as never);
        return {
          meta: {
            ...resp.meta,
            gaps: resp.meta.gaps ?? [],
            importedSessionId: resp.meta.importedSessionId
              ? String(resp.meta.importedSessionId)
              : "",
          },
          // 预览的消息已经是投影好的块，与真实回放同一条投影 —— 这里只补上转录
          // 渲染链要的几个字段，不解 blocks 原文。
          messages: (resp.messages ?? []).map((m, index) => ({
            id: index + 1,
            sessionId: 0,
            role: m.role,
            blocks: m.blocks ?? [],
            model: m.model,
            promptTokens: 0,
            completionTokens: 0,
            cachedTokens: 0,
            cacheCreationTokens: 0,
            reasoningTokens: 0,
            totalInputTokens: 0,
            durationMs: 0,
            errorText: m.errorText,
            seq: m.seq,
            createtime: m.createtime,
          })),
          previewedTurns: resp.previewedTurns,
          remainingTurns: resp.remainingTurns,
        };
      },
      runImport: async (req) => {
        const resp = await ImportLocalSession({
          deviceId: Number(req.deviceId),
          backend: req.backend,
          locator: req.locator,
          agentId: Number(req.agentId),
          projectId: req.projectId ? Number(req.projectId) : 0,
          cwd: req.cwd,
          requestId: req.requestId,
        } as never);
        return {
          sessionId: String(resp.sessionId),
          alreadyImported: resp.alreadyImported,
          readOnly: resp.readOnly,
          cwd: resp.cwd,
          importedTurns: resp.importedTurns,
        };
      },
      onImportProgress: (listener) =>
        EventsOn(PROGRESS_EVENT, (payload: { done: number; total: number }) =>
          listener(payload?.done ?? 0, payload?.total ?? 0),
        ),
      // 取消是「另敲一下」：导入本身是同步调用，停它只能靠同一个 requestId
      // 从外面把那一笔的 ctx 取消掉（写入侧整笔回滚，不留半截会话）。
      cancelImport: (requestId) => {
        void CancelLocalSessionImport({ requestId } as never);
      },
      // 转录记的目录没了时的另一条出路：弹系统目录选择器，选中的目录成为这条
      // 会话的工作目录（写入侧落在 chat_sessions.cwd 上）。用户按取消时 Wails
      // 回空串，这里折成 null —— 「没选」不该被当成「选了根目录」。
      pickDirectory: async () =>
        (await SelectDirectory(t("sessionIndex.import.pickDirectoryTitle"))) ||
        null,
      openSession: (sessionID) => openSession(Number(sessionID)),
    }),
    [devices, agentOptions, openSession, t],
  );
}
