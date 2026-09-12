// streaming-markdown: 把「流式累积中的 markdown 文本」切成
// [已定稿 block...] + [活跃尾巴]。
//
// 关键事实:流式消息里只有末尾正在生长的那一个 block 会变,它前面的内容
// 一旦产出就永不改变。调用方据此对每个已定稿 block 做 React.memo —— 闭合的
// 代码块只跑一次 highlight.js,后续 chunk 直接跳过,把单 chunk 渲染开销从
// O(n)(全量重解析+重高亮)降到 O(Δ)(只解析活跃尾巴)。
//
// 定稿边界的判定保守取「fence 之外的空行」与「闭合的围栏代码块」两类:
//   - fence 外的空行 = CommonMark block 边界,之前的 block 不会再被后续 token
//     改写;
//   - 闭合的围栏代码块整体是一个原子 block,内部空行不算边界。
// 未闭合的 fence 及最后一个仍在生长的 block 留在 tail 里每 chunk 重解析。
//
// 切分只是近似:turn done 后消息会从持久化数据走普通路径整体重渲染一次
// (单次完整解析),所以流式期间的任何切分近似都会自愈。

export type SplitStreamingMarkdown = {
  /** 已定稿、逐字节稳定的 block 文本,按出现顺序排列。 */
  committed: string[];
  /** 仍在生长的尾部(可能是半截段落或未闭合的 fence);为空表示无活跃尾巴。 */
  tail: string;
};

// 围栏起始:最多 3 个前导空格 + 连续 3 个以上的 ` 或 ~。
const FENCE_OPEN = /^ {0,3}(`{3,}|~{3,})/;

function isFenceClose(line: string, marker: "`" | "~"): boolean {
  const re = marker === "`" ? /^ {0,3}`{3,}\s*$/ : /^ {0,3}~{3,}\s*$/;
  return re.test(line);
}

export function splitStreamingMarkdown(text: string): SplitStreamingMarkdown {
  const lines = text.split("\n");
  const committed: string[] = [];
  let current: string[] = [];
  let fenceOpen = false;
  let fenceMarker: "`" | "~" = "`";

  const flush = () => {
    if (current.length > 0) {
      committed.push(current.join("\n"));
      current = [];
    }
  };

  for (const line of lines) {
    if (fenceOpen) {
      current.push(line);
      if (isFenceClose(line, fenceMarker)) {
        // 闭合的围栏代码块整体定稿。
        fenceOpen = false;
        flush();
      }
      continue;
    }

    const open = FENCE_OPEN.exec(line);
    if (open) {
      // fence 起始会打断上一段 —— 先把已累积的 prose 定稿,再开始攒 fence。
      flush();
      fenceOpen = true;
      fenceMarker = open[1][0] as "`" | "~";
      current.push(line);
      continue;
    }

    if (line.trim() === "") {
      // fence 外的空行 = block 边界,之前的内容已定稿。
      flush();
      continue;
    }

    current.push(line);
  }

  // 残留在 current 里的就是仍在生长的尾巴(含未闭合的 fence)。
  return { committed, tail: current.join("\n") };
}
