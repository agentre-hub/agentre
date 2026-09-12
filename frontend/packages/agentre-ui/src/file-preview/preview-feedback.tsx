import { Skeleton } from "../ui/skeleton";

/** PreviewNotice 是内容区居中的一段说明文字，hint 是可选的次级补充。 */
export function PreviewNotice({ text, hint }: { text: string; hint?: string }) {
  return (
    <div className="px-3 py-6 text-center text-xs leading-relaxed text-muted-foreground">
      {text}
      {hint ? (
        <span className="mt-1.5 block text-2xs opacity-80">{hint}</span>
      ) : null}
    </div>
  );
}

/** PreviewSkeleton 是首屏取数时的占位骨架；label 只给读屏，视觉上是几条脉冲条。 */
export function PreviewSkeleton({ label }: { label: string }) {
  return (
    <div className="flex flex-col gap-2 px-3 py-3">
      <span role="status" className="sr-only">
        {label}
      </span>
      {[70, 52, 81, 44].map((width) => (
        <Skeleton
          key={width}
          className="h-2.5"
          style={{ width: `${width}%` }}
        />
      ))}
    </div>
  );
}
