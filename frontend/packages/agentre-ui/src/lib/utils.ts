import { clsx, type ClassValue } from "clsx";
import { extendTailwindMerge } from "tailwind-merge";

// globals.css 的 @theme inline 里定义了三个自定义字号 token(text-prose /
// text-aux / text-meta)。裸 twMerge 不认识它们,启发式会把它们误判进
// text-color 冲突组,导致字号类和真正的文字颜色类(如 text-muted-foreground)
// 互斥、二选一被静默丢弃。这里在共享 cn() 上一次性注册成独立的 font-size
// 组,让全仓所有调用点(包括经过 shadcn Button 等组件转发的 className)都
// 拿到一致的合并结果 —— 不要在单个组件文件里再建局部 cn() 重复 normalize。
const twMerge = extendTailwindMerge({
  extend: {
    classGroups: { "font-size": ["text-prose", "text-aux", "text-meta"] },
  },
});

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
