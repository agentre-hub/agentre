import type { MemoryWriteResult } from "@agentre-hub/agentre-ui";

import { errorText, parseCodedError } from "@/lib/app-error";
import { splitErrorDetail } from "@/lib/error-detail";

/**
 * 记忆写入失败 → 共享包认识的结果。
 *
 * 带业务码的错误（`agentre-code:<码> <文案>`）里那句是后端按语言渲染好的事实
 * （「记忆摘要至多 200 字」「含有明显的凭据格式」），原样作为原因；不带码的是机器
 * 原话，不进界面，包退回它自己的通用句。
 */
export function memoryWriteFailure(error: unknown): MemoryWriteResult {
  const coded = parseCodedError(errorText(error).trim());
  if (!coded) return { ok: false };
  const reason = splitErrorDetail(coded.message).msg.trim();
  return reason ? { ok: false, reason } : { ok: false };
}

/** 跑一次写入，把成败收成 `MemoryWriteResult`。 */
export async function runMemoryWrite(
  write: () => Promise<unknown>,
): Promise<MemoryWriteResult> {
  try {
    await write();
    return { ok: true };
  } catch (error) {
    return memoryWriteFailure(error);
  }
}
