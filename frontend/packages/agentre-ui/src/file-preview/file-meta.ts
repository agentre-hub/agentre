/** 会话级 relPath 的文件名部分。 */
export function basename(path: string): string {
  const parts = path.split(/[\\/]/);
  return parts[parts.length - 1] ?? path;
}

/**
 * 会话级 relPath 的目录前缀（含末尾分隔符）；根目录下的文件返回空串。
 *
 * 分隔符必须与 basename 认的是同一套：Windows 会话的 relPath 用 "\" 分隔
 * （previewable.ts 的 toRelPath 按 cwd 的分隔符切，「变动」模式的行路径来自工具
 * 调用的绝对路径），只认 "/" 的话文件名照样切得出来、目录前缀却恒为空串——
 * header 里那一栏「这个文件在哪」就无声消失了。
 */
export function dirname(path: string): string {
  const i = Math.max(path.lastIndexOf("/"), path.lastIndexOf("\\"));
  return i > 0 ? path.slice(0, i + 1) : "";
}
