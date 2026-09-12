// 桌面端的 composer:把共享包的 ChatComposer 接上本机的数据源 ——
// mention 候选(项目树 / 设备)、slash 命令(skill + 本地命令)、预置 agent 上下文,
// 以及「拖进窗口的文件落盘后读成图片」这条只有 Wails 端才有的通道。

import * as React from "react";

import {
  ChatComposer as SharedChatComposer,
  buildMentionSources,
  type ChatComposerDropZone,
  type ChatComposerHandle,
  type ChatComposerProps as SharedChatComposerProps,
  type SlashCommand,
  type SlashExec,
  useSlashCommands,
} from "@agentre-hub/agentre-ui";

import { useChatAgents } from "@/hooks/use-chat-agents";
import { useProjectList } from "@/hooks/use-project-list";
import { useDeviceMentionItems } from "../remote-devices/use-device-mentions";
import { ChatReadDroppedImages } from "../../../../wailsjs/go/app/App";
import { chat_svc } from "../../../../wailsjs/go/models";
import { registerDropZone } from "@/lib/file-drop";
import { desktopSlashCommands, useAgentSkillCommands } from "../slash-commands";

type DesktopChatComposerProps = Omit<
  SharedChatComposerProps,
  "mentionSources" | "slashCommands" | "onSlashSelect" | "dropZone"
> & {
  /** 当前会话或新会话的 agent id，用于加载该 agent 最终生效的 skill 命令。 */
  agentId?: number;
  /** 当前会话 / 项目的工作目录，用于发现 project-scoped Skill。 */
  cwd?: string;
  /** slash menu 里 rpc 类命令的回调（literal_text 由包内部填回编辑器）。 */
  onSlashRpc?: (
    cmd: SlashCommand,
    exec: Extract<SlashExec, { kind: "rpc" }>,
  ) => void;
};

// 落盘路径 → 图片附件。这是 Wails 绑定，是包里 resolveDroppedPaths 的 readImages
// 端口在桌面端的实现；浏览器端拿不到绝对路径，所以那一端不注入这个通道。
const DESKTOP_DROP_ZONE: ChatComposerDropZone = {
  readImages: async (paths: string[]) => {
    const resp = await ChatReadDroppedImages(
      chat_svc.ReadDroppedImagesRequest.createFrom({ paths }),
    );
    return (resp.items ?? []).map((it) => ({
      dataUrl: it.dataUrl,
      kind: it.kind === "image" ? ("image" as const) : ("path" as const),
      mediaType: it.mediaType,
      name: it.name,
      path: it.path,
    }));
  },
  registerDropZone,
};

// formatResetIn 把"距离 ISO 时间点还有多久"渲染成紧凑的 XdYh / Xh / Xm 形式
// (e.g. "4d21h", "3h", "40m"),用于 QuotaMeter tooltip。
//   - 空串 / 无法解析的输入 → 空串(调用方自己决定是否显示括号)
//   - 已过期(diff<=0)→ "0m"
//   - <1h → "Nm"(向上取整,避免 30s 显示 0m)
//   - <24h → "Nh"(向下取整)
//   - >=24h → "XdYh"(Yh=0 时省略,写 "Xd")
export const ChatComposer = React.forwardRef<
  ChatComposerHandle,
  DesktopChatComposerProps
>(function ChatComposer({ agentId = 0, cwd = "", onSlashRpc, ...rest }, ref) {
  const { agents } = useChatAgents();
  const { projects } = useProjectList();
  const devices = useDeviceMentionItems();
  const mentionSources = React.useMemo(
    () => buildMentionSources(agents, projects, devices),
    [agents, projects, devices],
  );
  // 清单在包里,宿主只递两样它独有的东西:问这台机器拿到的 Skill 目录,以及
  // `/new`(开新标签页,浏览器那一端没有这回事)。
  const skills = useAgentSkillCommands(agentId, rest.backendType ?? "", cwd);
  const slashCommands = useSlashCommands({
    backendType: rest.backendType,
    skills,
    extraCommands: desktopSlashCommands,
  });

  return (
    <SharedChatComposer
      {...rest}
      ref={ref}
      dropZone={DESKTOP_DROP_ZONE}
      mentionSources={mentionSources}
      slashCommands={slashCommands}
      onSlashSelect={(cmd, exec) => {
        // literal_text 由包内部直接填回编辑器(不自动发送),这里只接 rpc 类。
        if (exec.kind === "rpc") onSlashRpc?.(cmd, exec);
      }}
    />
  );
});
