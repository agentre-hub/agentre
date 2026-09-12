import {
  Circle,
  CircleCheckBig,
  CircleDashed,
  CircleDot,
  type LucideIcon,
} from "lucide-react";

import { BOARD_STAGES, type BoardStage } from "./types";

/** 列头那枚记号与它的强调色；四列的顺序就是 `BOARD_STAGES` 的顺序。 */
export const BOARD_STAGE_META: Record<
  BoardStage,
  { icon: LucideIcon; accent: string; labelKey: string }
> = {
  todo: {
    icon: Circle,
    accent: "text-status-idle",
    labelKey: "board.stages.todo",
  },
  doing: {
    icon: CircleDot,
    accent: "text-primary-text",
    labelKey: "board.stages.doing",
  },
  review: {
    icon: CircleDashed,
    accent: "text-status-waiting",
    labelKey: "board.stages.review",
  },
  done: {
    icon: CircleCheckBig,
    accent: "text-status-running",
    labelKey: "board.stages.done",
  },
};

export { BOARD_STAGES };
export type { BoardStage };
