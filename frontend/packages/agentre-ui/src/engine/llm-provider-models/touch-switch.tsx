import * as React from "react";

import { cn } from "../../lib/utils";
import { Switch } from "../../ui/switch";
import { useTouch } from "../../ui/touch";

// 触屏档：开关视觉不变（24×14），点按区用透明伪元素撑到 44px（与技能入口同一做法，
// 不靠会改版面的外层包装）。
const TOUCH_HIT = "relative before:absolute before:-inset-x-2.5 before:-inset-y-3.5 before:content-['']";

export function TouchSwitch({
  className,
  ...props
}: React.ComponentProps<typeof Switch>) {
  const touch = useTouch();
  return <Switch className={cn(touch && TOUCH_HIT, className)} {...props} />;
}
