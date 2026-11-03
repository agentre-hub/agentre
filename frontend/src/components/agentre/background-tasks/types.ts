export type BackgroundTaskKind = "local_bash" | "local_agent";
export type BackgroundTaskStatus =
  | "running"
  | "completed"
  | "failed"
  | "canceled";

export interface BackgroundTask {
  toolUseId: string;
  taskId?: string; // 真实 CLI task_id(不可读串,如 b3875slp0)— 直接展示
  kind: BackgroundTaskKind;
  description: string;
  status: BackgroundTaskStatus;
  startedAt?: number; // epoch ms (containing message createtime) — base for live elapsed
  durationMs?: number; // frozen duration for completed/failed bash tasks
  summary?: string; // completion/exit-code text, dynamic
  // 发起这个任务的那条消息;只有已落库的消息才有,用来把对话滚到那张卡片。
  messageId?: number;
  // 以下三项只有子代理(local_agent)才有。
  subagentType?: string; // 派遣时指定的子代理类型,如 Explore
  model?: string; // 实际跑的模型;首帧还没到时是派遣时的入参别名
  toolUses?: number; // 已走的步数
}

// 会话留下的一个存活进程(本机或远程执行设备上)。
export interface SessionProcess {
  pid: number;
  command: string; // 完整命令行,动态值
  ports: number[]; // 监听中的 TCP 端口,可能为空
  startedAt: number; // epoch ms — 实时运行时长的基准
  cwd?: string;
}
