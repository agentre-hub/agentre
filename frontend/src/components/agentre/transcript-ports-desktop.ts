import { useMemo } from "react";
import { useNavigate } from "react-router-dom";
import type {
  FilePreviewFailure,
  MentionRef,
  TranscriptPorts,
} from "@agentre-hub/agentre-ui";

import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useFilePreviewTabsStore } from "@/stores/file-preview-tabs-store";
import { useFileSettingsStore } from "@/stores/file-settings-store";
import {
  type WorkRoot,
  useSessionRootStore,
} from "@/stores/session-root-store";
import { useSessionTerminalsStore } from "@/stores/session-terminals-store";
import { openSettings } from "@/stores/settings-dialog-store";
import { useToolWindowLayout } from "@/stores/tool-window-layout-store";

import {
  memoryWriteFailure,
  runMemoryWrite,
} from "./memory-page/memory-errors";
import { toEntryView } from "./memory-page/to-entry-view";
import { openWebPreview } from "./tool-windows/web-preview-store";

import {
  AnswerToolApproval,
  AnswerToolPermission,
  AnswerUserQuestion,
  MemoryGet,
  MemoryUndoWrite,
  MemoryUpdate,
  OpenPath,
  ResolveExecApproval,
  ResolvePlanAction,
  ResolveSessionURL,
  WorkspaceFsClaimPath,
  WorkspaceFsReadFile,
} from "../../../wailsjs/go/app/App";
import { chat_svc, type memory_svc } from "../../../wailsjs/go/models";
import { BrowserOpenURL } from "../../../wailsjs/runtime/runtime";

/**
 * 会话的认领集合交给转录做链接归属的形状（`SessionLocationProvider` 的 `roots`）。
 * 集合还没取回时会话工作目录就是唯一的根——今天的口径。
 */
export function toLinkRoots(roots: readonly WorkRoot[], cwd: string) {
  if (roots.length === 0) return cwd ? [{ path: cwd, primary: true }] : [];
  return roots.map((r) => ({
    path: r.path,
    name: r.name,
    primary: r.isPrimary,
  }));
}

/**
 * 预览标签上的根只有一种表示（`FilePreviewTab.root`：主根恒为空串）。转录给的是根的
 * 绝对路径，在这个边界上把主根换成空串——否则侧栏与转录会为同一个文件开出两个标签。
 */
function canonicalPreviewRoot(sessionId: number, root: string): string {
  const entry = useSessionRootStore.getState().bySession[sessionId];
  const primary = entry?.roots.find((r) => r.isPrimary)?.path ?? entry?.cwd;
  const trim = (p: string) => p.replace(/[\\/]+$/, "");
  return primary !== undefined && trim(root) === trim(primary) ? "" : root;
}

/**
 * 桌面端的对话流端口实现 —— 共享包与 Wails 绑定之间唯一的接缝。
 *
 * 这里是**唯一**允许把转录里的动作接到 Wails 上的地方；卡片自身只认
 * `useTranscriptPorts()`。agentre-server 提供另一份实现（同样的接口，
 * 动作走 relay RPC），因此同一套卡片在两端都能用。
 *
 * 模块级常量而不是每次渲染现造：TranscriptRenderContext 的稳定性是
 * 行级 memo 不被击穿的前提，端口对象跟着一起保持同一性。
 */
// 用 `satisfies` 而不是类型标注 `: TranscriptPorts`：标注会把每个可选成员
// widen 成 `... | undefined`，展开进下面的完整端口面时就再也证明不了「一个不缺」。
// `satisfies` 同样校验形状，但保住字面量的精确成员类型。
export const desktopTranscriptPorts = {
  async answerToolPermission(input) {
    await AnswerToolPermission(input as chat_svc.AnswerToolPermissionRequest);
  },

  async answerUserQuestion(input) {
    await AnswerUserQuestion(input as chat_svc.AnswerUserQuestionRequest);
  },

  async answerToolApproval(input) {
    await AnswerToolApproval(
      chat_svc.AnswerToolApprovalRequest.createFrom(input),
    );
  },

  async resolveExecApproval(input) {
    const response = await ResolveExecApproval(
      input as chat_svc.ResolveExecApprovalRequest,
    );

    return { status: response.status, decision: response.decision };
  },

  async resolvePlanAction(input) {
    const response = await ResolvePlanAction(
      input as chat_svc.ResolvePlanActionRequest,
    );

    return {
      sessionId: response.sessionId,
      userMessageId: response.userMessageId,
      assistantMessageId: response.assistantMessageId,
      stream: response.stream,
    };
  },

  // 后端判出「本机没有这条路径」时走应答字段（Wails 边界只过错误字符串），这里
  // 还原成一次带 notFound 标记的失败：转录据此说事实、给预览出口（远端会话的
  // 文件在另一台机器上，通常就是这一档）。
  async openPath(path) {
    const res = await OpenPath(path);
    if (res?.unavailable === "not-found") {
      throw Object.assign(new Error(res.unavailable), {
        kind: "notFound",
      } satisfies FilePreviewFailure);
    }
  },

  fileOpenDefault() {
    return useFileSettingsStore.getState().settings.openAction;
  },

  // 远端会话里的 localhost 链接指向执行设备上的服务，直接交给本机浏览器会打开桌面
  // 本机的同端口。带会话号时先让后端换成经端口转发的本机地址（本机会话与非
  // localhost 链接后端原样交回）；解析失败照实抛出，不退回打开原始链接。
  async openExternalURL(url, opts) {
    const target =
      opts?.sessionId === undefined
        ? url
        : await ResolveSessionURL(opts.sessionId, url);
    BrowserOpenURL(target);
  },

  // 悬浮卡的「应用内预览」：在「网页预览」工具窗里打开这条 localhost 链接并显示该窗。
  // 远端会话的端口转发由窗口自己经 ProbeSessionWebPreview 解析，这里只递地址。
  previewURL(sessionId, url) {
    openWebPreview(sessionId, url);
  },

  async readWorkspaceFile(sessionId, path) {
    // 转录里的一条路径来自当时那次工具调用，转录本身没有「当前工作根」的概念：
    // 空串 = 会话 cwd，保持这个接缝本轮之前的解析口径不变。
    const view = await WorkspaceFsReadFile(sessionId, "", path);

    // 服务层把「文件不存在 / 对端离线」改成了随成功应答回来的结构化原因（预览面
    // 板要据此分出终态与可重试态）。这个端口的消费者只认「成不成」，所以在边界上
    // 把它还原成一次失败 —— 否则内联图片会拿着空 body 当成功渲染出来。
    if (view.unavailable) {
      throw new Error(view.unavailable);
    }

    return {
      content: view.content,
      contentType: view.contentType,
      binary: view.binary,
      tooLarge: view.tooLarge,
    };
  },

  // 接入进程是在转录卡片上点的，而端口不带会话号：落在此刻最前面的会话标签上——
  // 作为那个会话终端工具窗里的新标签打开并显示该窗。前面不是会话标签（没有终端
  // 工具窗可依附）时退回独立的终端对话标签页。
  attachTerminal(input) {
    const { tabs, activeTabId } = useChatTabsStore.getState();
    const active = tabs.find((t) => t.id === activeTabId);
    if (active?.meta.kind !== "session") {
      useChatTabsStore.getState().attachTerminal(input);
      return;
    }
    useSessionTerminalsStore.getState().attach(active.meta.sessionId, input);
    useToolWindowLayout.getState().show("terminal");
  },

  // 设置读取留在这里(ports.ts 已经写明这是宿主的产品决策,包内不知道也不该知道
  // files.open_action):"preview" 时开/复用既有右侧栏预览标签并回 true,调用方
  // (RichLink 的 dispatchClick)据此不再退回 openPath;"external" 时什么都不做,
  // 回 false,调用方退回今天的外部打开路线(byte-identical,含 line:col 后缀)。
  // 读 .getState() 而不是订阅:这是一次性的点击响应,不需要 ports 对象本身随
  // 设置变化重建。
  //
  // 入口模式是 "directory":转录里点一条路径要看的是「这个文件**现在**长什么样」
  // (spec「入口与可用性」),面板据此走 readFile 读工作区正文。不能是 "session"
  // ——那是侧栏「本次会话」档的工具 diff,一次取数都不打。
  //
  // `force`:用户在浮窗 / 失败提示里明确选了「预览」,设置不拦它。
  //
  // `{ root, path }`：链接归属的工作根（转录据会话的认领集合判出）。主根在这里归一成
  // 预览标签的规范表示。
  previewFile(sessionId, path, anchor?, opts?) {
    const { openAction } = useFileSettingsStore.getState().settings;
    if (openAction === "external" && !opts?.force) return false;
    const target =
      typeof path === "string"
        ? path
        : { root: canonicalPreviewRoot(sessionId, path.root), path: path.path };
    useFilePreviewTabsStore
      .getState()
      .openPreview(sessionId, target, "directory", anchor);
    return true;
  },

  // 点了一条不在已认领根内的绝对路径（spec 决策 20）：后端按自动认领的规则当场认领，
  // 回完整集合。集合同时落到会话的工作根状态上，标题栏胶囊与转录的根归属随之更新。
  async claimPath(sessionId, absPath) {
    const roots = await WorkspaceFsClaimPath(sessionId, absPath);
    const store = useSessionRootStore.getState();
    const entry = store.bySession[sessionId];
    if (entry) store.applyRoots(sessionId, entry.cwd, roots);
    return toLinkRoots(roots, entry?.cwd ?? "");
  },

  // 对话里的记忆卡片。创建者在这里不需要可点：卡片只用摘要与详情，所以会话标题查询与
  // 跳转都给空实现。
  memory: {
    async get(id) {
      const item = await MemoryGet(id);
      if (!item) return null;
      return toEntryView(item, {
        sessionTitle: () => undefined,
        fallbackLabel: () => "",
        openSession: () => {},
      });
    },

    async undoWrite({ id, sessionId, action }) {
      try {
        const result = await MemoryUndoWrite({
          id,
          sessionId,
          action,
        } as memory_svc.UndoWriteRequest);
        if (result.status === "undone" || result.status === "changed") {
          return { status: result.status };
        }
        return { status: "gone" };
      } catch (error) {
        const failure = memoryWriteFailure(error);
        return {
          status: "failed",
          reason: failure.ok ? undefined : failure.reason,
        };
      }
    },

    save(id, draft) {
      return runMemoryWrite(() =>
        MemoryUpdate({ id, summary: draft.summary, detail: draft.detail }),
      );
    },
  },
} satisfies TranscriptPorts;

/**
 * 桌面端完整的端口面 —— 应用根用它，不要直接用上面的常量。
 *
 * 为什么单独一层：其余端口都是 Wails 调用，可以做成模块级常量；而
 * `openMention`（点 @提及去哪）是**路由**问题，`navigate` 只能从 hook 拿，
 * 模块级常量表达不了。依赖只有 navigate（react-router 保证其稳定），
 * 所以 TranscriptRenderContext 的稳定性不受影响、行级 memo 不会被击穿。
 *
 * `satisfies Required<TranscriptPorts>` 是这层的重点：桌面端是全能力宿主，
 * 包里的可选端口对它应当**一个不缺**（可选是给 agentre-server 那种缺能力的
 * 宿主留的）。漏接一个的表现是「按钮悄悄消失、点了没反应」，肉眼极难发现 ——
 * 这条标注让它变成编译期错误。新增可选端口而桌面端没接，`tsc` 当场红。
 */
export function useDesktopTranscriptPorts(): TranscriptPorts {
  const navigate = useNavigate();

  return useMemo(
    () =>
      ({
        ...desktopTranscriptPorts,
        openMention: (ref: MentionRef) => {
          if (ref.kind === "agent") return navigate("/org");
          // 项目的去处是索引里那一组本身（`?reveal=` 由索引页消费：展开祖先、
          // 滚到位、亮一下）。`/projects` 那一页早已并进索引，只剩一条回 `/chat`
          // 的重定向 —— 人就在 `/chat` 上，去那儿等于没动。
          if (ref.kind === "project") {
            return navigate(`/chat?reveal=${ref.refId}`);
          }
          // 设备的去处是设置里的设备面板 —— 那是这台机器在桌面端唯一的落脚页。
          openSettings("remote-devices");
        },
        // 会话标签页只在 /chat 下可见：先开/复用标签，再确保人在 /chat 上。
        openSession: (sessionId: number) => {
          useChatTabsStore.getState().openSession(sessionId);
          navigate("/chat");
        },
      }) satisfies Required<TranscriptPorts>,
    [navigate],
  );
}
