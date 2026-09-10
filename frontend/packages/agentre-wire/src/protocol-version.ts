/**
 * agentre ↔ agentred wire 协议版本。
 *
 * 版本号的**唯一真相**是本包 `package.json` 的 `version`:本包发布 schema、
 * 生成的消息与编解码,所以它的发布号就是「协议」本身的版本。这里把它复述成一个
 * 可被握手代码引用的常量,`src/__tests__/protocol-version.test.ts` 盯着两者逐字
 * 相等——改一边忘另一边直接红。
 *
 * Go 侧的对应常量是 `internal/pkg/wireversion.Protocol`,由它自己的守卫测试
 * 盯着同一份 package.json。
 */
export const PROTOCOL_VERSION = "0.3.0";
