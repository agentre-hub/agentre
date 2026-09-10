// 终端 stdout 到手的是 PTY 原始**字节**(见 TerminalSubscriber.onData):一个多字节
// 字符可能横跨两块,所以按流解码 —— TextDecoder 会把半个字符的尾巴带到下一块,
// 直接一块块 decode 会把它替换成 U+FFFD 永久损坏。
// base64 是桌面端 Wails 事件桥的编码,已经在传输适配层解掉了,这里不再插手。
export function makeStreamDecoder() {
  const dec = new TextDecoder();
  return (bytes: Uint8Array): string => dec.decode(bytes, { stream: true });
}
