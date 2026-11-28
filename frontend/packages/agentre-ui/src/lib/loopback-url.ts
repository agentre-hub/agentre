/**
 * 指向执行设备自己的主机名。`0.0.0.0` / `[::]` 是开发服务器常打印的「绑定全部网卡」
 * 地址，访问它到的仍是这台设备上的端口（远端会话因此同样要经端口转发）。
 */
const LOOPBACK_HOSTS = ["localhost", "127.0.0.1", "[::1]", "0.0.0.0", "[::]"];

/**
 * 环回 http(s) 地址实际访问的端口（没写端口按协议默认）；不是环回 http(s) 地址时为
 * null。转录链接的「应用内预览」与网页预览窗把地址归到端口标签共用这一份判定。
 */
export function loopbackURLPort(raw: string): number | null {
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    return null;
  }
  if (u.protocol !== "http:" && u.protocol !== "https:") return null;
  if (!LOOPBACK_HOSTS.includes(u.hostname)) return null;
  if (u.port !== "") return Number(u.port);
  return u.protocol === "https:" ? 443 : 80;
}
