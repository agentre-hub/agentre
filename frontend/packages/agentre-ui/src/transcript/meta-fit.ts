// 统计行紧凑档（手机宽度下每一轮回复底下那一行）的纯函数：数字缩写、模型名规整，
// 以及「放不下时让到哪一档」的判定。判定只吃量好的宽度，不碰 DOM —— 量宽度归组件。

/** 紧凑档的 token 数：< 1000 原样，k / M 一位小数（52.4k、1.3M）。 */
export function compactTokenCount(n: number): string {
  if (n < 1000) return String(n);
  const k = (n / 1000).toFixed(1);
  // 999_960 这类会四舍五入成「1000.0k」，交给 M 档。
  if (n < 1_000_000 && Number(k) < 1000) return `${k}k`;
  return `${(n / 1_000_000).toFixed(1)}M`;
}

/**
 * 紧凑档的模型名：去掉厂商前缀（最后一个「/」之前，如 `anthropic/`）、`claude-`
 * 前缀与末尾的 8 位日期后缀（`-20250805`）。规整不算「藏」，完整 id 在明细里。
 * 规整到空串时退回原值。
 */
export function normalizeModelName(model: string): string {
  const bare = model
    .slice(model.lastIndexOf("/") + 1)
    .replace(/^claude-/, "")
    .replace(/-\d{8}$/, "");
  return bare || model;
}

/** 各段的实际渲染宽度（px），每段都已含它前面的间距；没有的段给 0。 */
export type MetaFitInput = {
  /** 可用宽度；0 表示还没排过版（隐藏中 / 测试环境），按「放得下」处理。 */
  available: number;
  /** 规整后模型名的完整宽度。 */
  model: number;
  /** 模型名截断后的下限：前 6 个字符 + 省略号（不足 6 个字符时等于 model）。 */
  modelMin: number;
  /** 永远不藏的部分：↑↓ 计数。 */
  fixed: number;
  duration: number;
  speed: number;
  /** 行尾「›」。 */
  more: number;
};

/** 0 全露；1 藏 tok/s；2 再藏用时；3 再截模型名（至少露 6 个字符）。 */
export type MetaFitLevel = 0 | 1 | 2 | 3;

export type MetaFit = {
  level: MetaFitLevel;
  /** 真的藏了或截了东西才出「›」。 */
  more: boolean;
};

export function fitMetaLine(input: MetaFitInput): MetaFit {
  const { available, model, modelMin, fixed, duration, speed, more } = input;
  const base = model + fixed;
  if (available <= 0 || base + duration + speed <= available) {
    return { level: 0, more: false };
  }
  const hidesSpeed = speed > 0;
  if (base + duration + more <= available) {
    return { level: 1, more: hidesSpeed };
  }
  const hidesDuration = duration > 0;
  if (base + more <= available) {
    return { level: 2, more: hidesSpeed || hidesDuration };
  }
  return {
    level: 3,
    more: hidesSpeed || hidesDuration || model > modelMin,
  };
}
