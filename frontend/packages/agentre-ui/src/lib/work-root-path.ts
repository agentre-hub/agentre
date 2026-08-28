import { classifyLink } from "./link-classify";

/**
 * resolveToolPathInRoot 把一次工具调用给的路径（可能是绝对路径、`~/…`、或相对
 * 会话工作目录）按当前工作根解析一次:落在这个根的子树之外的一律返回 null,
 * 子树之内的归一成相对这个根的路径 —— 同一个文件被绝对与相对两种写法改过时
 * 因此只算作一个文件。
 *
 * 没有工作根（`root === ""`）时无从判断归属、也拼不出相对路径:此时不过滤,
 * 原样返回工具给的路径。
 *
 * 它住在共享包里是因为**两个消费面必须逐字同一套判定**:「变更」页据它决定
 * 一个文件有没有这一行,重放据它决定这一行点开后收哪几次调用。两边各写一份,
 * 漂移的那一天行还在、diff 却少算几次调用（或者反过来),而两处都不会报错。
 */
export function resolveToolPathInRoot(
  raw: string,
  root: string,
): string | null {
  if (raw === "") return null;
  if (root === "") return raw;
  const link = classifyLink(raw, root);
  if (link.kind !== "local-internal") return null;
  // relPath 为空串 = 这条路径就是工作根本身,不是一行文件。
  return link.relPath === "" ? null : link.relPath;
}
