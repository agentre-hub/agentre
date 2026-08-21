/**
 * 「目录」与「Git」两个模式共用的空态 / 骨架屏 / 错误文案。两边的取数形状一样
 * （会话快照 + 后端已本地化的失败文案），这三段此前是两份逐字相同的私有拷贝 ——
 * 各自演进只会让同一个面板里的两个模式看起来不像同一个东西。
 */

/**
 * errorText 取后端已本地化的错误文案原样呈现 —— Wails 边界只过 Error() 字符串、
 * 没有结构化的错误码通道，而后端对「远端设备不在线」「远端 agentred 版本过旧」
 * 等各自给了明确文案，照搬即可，不再在前端二次归类。拿不到文案时返回空串，由
 * 调用方回落到自己那句通用提示。
 */
export function errorText(err: unknown): string {
  if (err instanceof Error) return err.message;
  if (typeof err === "string") return err;
  return "";
}

/** PanelNotice 是内容区居中的一段说明文字，hint 是可选的次级补充。 */
export function PanelNotice({ text, hint }: { text: string; hint?: string }) {
  return (
    <div className="px-3 py-6 text-center text-xs leading-relaxed text-muted-foreground">
      {text}
      {hint ? (
        <span className="mt-1.5 block text-2xs opacity-80">{hint}</span>
      ) : null}
    </div>
  );
}

/** PanelSkeleton 是首屏取数时的占位骨架；label 只给读屏，视觉上是几条脉冲条。 */
export function PanelSkeleton({ label }: { label: string }) {
  return (
    <div className="flex flex-col gap-2 px-3 py-3">
      <span role="status" className="sr-only">
        {label}
      </span>
      {[70, 52, 81, 44].map((width) => (
        <div
          key={width}
          className="h-2.5 animate-pulse rounded-sm bg-muted"
          style={{ width: `${width}%` }}
          aria-hidden="true"
        />
      ))}
    </div>
  );
}
