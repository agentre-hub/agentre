// frontend/packages/agentre-ui/src/port-forward/target.ts
//
// 端口转发目标的纯呈现 / 校验逻辑 —— 两个宿主共用同一份判定,不各写一份
// (规格「映射与目标」决策 8:写法合不合法的**权威**判定恒在设备侧,这里的
// `isLikelyValidPortForwardTarget` 只是给新增表单一点即时反馈,判不全不是 bug——
// 判不出来的一律放行,交给设备的 -32076 兜底)。

/** 目标解析出的三元组:host 已去掉 IPv6 方括号,port 恒有值(省略时按协议补 80/443)。 */
export interface ParsedPortForwardTarget {
  scheme: "http" | "https";
  host: string;
  port: number;
}

/** 纯端口简写(`3000`)在设备上展开成的主机,设备迁移给旧映射补写的也是它。 */
const SHORTHAND_HOST = "127.0.0.1";

/** 目标是不是显式写成 `https://` —— 决定新增表单要不要出「忽略证书错误」勾选框。 */
export function isExplicitHttpsTarget(raw: string): boolean {
  return /^https:\/\//i.test(raw.trim());
}

/**
 * 把一条**已规范化**的目标(设备总是带着端口回来,见 `MappingView.Target` 的
 * 注释)解析成 scheme / host / port。解析不出来时给 `null` —— 调用方原样展示
 * 输入,不是本函数的职责去补救一条本就不该出现的坏值。
 */
export function parsePortForwardTarget(
  target: string,
): ParsedPortForwardTarget | null {
  let url: URL;
  try {
    url = new URL(target);
  } catch {
    return null;
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") return null;
  if (!url.hostname) return null;
  const scheme = url.protocol === "https:" ? "https" : "http";
  const port = url.port ? Number(url.port) : scheme === "https" ? 443 : 80;
  // WHATWG URL 的 hostname 对 IPv6 保留方括号(`[::1]`)——这里剥掉,host 只是主机
  // 本身,与 scheme / port 两格同一种口径。
  const host = url.hostname.replace(/^\[|\]$/g, "");
  return { scheme, host, port };
}

/**
 * 行上显示用:纯端口简写展开出来的那种目标(`http://127.0.0.1:<端口>`)只显示端口,
 * 其余显示规范化后的完整目标(它已经带着协议与端口,不需要再加工)。只认这一种,
 * 因为同一台设备上 `8080`、`https://127.0.0.1:8080`、`http://[::1]:8080` 是三条
 * 互不冲突的映射,都缩成端口就分不出是哪一条。
 */
export function formatPortForwardTarget(target: string): string {
  const parsed = parsePortForwardTarget(target);
  if (!parsed || parsed.scheme !== "http" || parsed.host !== SHORTHAND_HOST) {
    return target;
  }
  return String(parsed.port);
}

const HOST_PORT_RE = /^[^\s:/]+:\d+$/;

function isValidPort(port: number): boolean {
  return Number.isInteger(port) && port >= 1 && port <= 65535;
}

/**
 * 新增表单的**即时反馈**校验,不是权威判定。三种写法(`3000` / `host:port` /
 * `http(s)://host[:port]`)之外的一律挡下来;三种写法内部挡不住的坏值(比如
 * 主机名语法本身就有问题)留给设备的 -32076 兜底 —— 这里判不全不是 bug。
 */
export function isLikelyValidPortForwardTarget(raw: string): boolean {
  const value = raw.trim();
  if (!value) return false;

  if (/^\d+$/.test(value)) {
    return isValidPort(Number(value));
  }

  if (/^https?:\/\//i.test(value)) {
    let url: URL;
    try {
      url = new URL(value);
    } catch {
      return false;
    }
    if (url.protocol !== "http:" && url.protocol !== "https:") return false;
    if (!url.hostname) return false;
    if (url.username || url.password) return false;
    if (url.search) return false;
    if (url.pathname && url.pathname !== "/") return false;
    if (url.port && !isValidPort(Number(url.port))) return false;
    return true;
  }

  if (HOST_PORT_RE.test(value)) {
    const [host, portText] = value.split(":");
    return host.length > 0 && isValidPort(Number(portText));
  }

  return false;
}
