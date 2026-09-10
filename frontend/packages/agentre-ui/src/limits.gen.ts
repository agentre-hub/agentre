/**
 * wire 协议的尺寸预算。
 *
 * 本文件由 Go 生成器产出,**不要手改** —— 手改会被下一次重新生成覆盖,
 * 而且 TestGeneratedTSFresh 会立刻变红。
 *
 * 真理源:  pkg/wire/wirelimits 的常量
 * 生成器:  internal/pkg/agentruntime/runtimes/remote/wire/tsgen_test.go
 * 重新生成:
 *
 *   WIRE_TS_WRITE=1 go test ./internal/pkg/agentruntime/runtimes/remote/wire/ -run TestWriteTSCodec
 *
 * 两处产物:同一组数在本仓写到两个包 —— @agentre-hub/agentre-wire 的
 * src/limits.gen.ts(与 codec / constants 同包),以及本文件。理由与
 * event-kinds.gen.ts 的第二份逐字同构:agentre-server 通过 git 依赖只取
 * frontend/packages/agentre-ui 这一个子目录,抽出来的 tarball 里没有兄弟包,
 * 所以这个包不能 import @agentre-hub/agentre-wire。
 *
 * 两份不是手抄:值都由同一个 Go 生成器从同一组 Go 常量写出,
 * TestGeneratedTSFresh 对两处产物逐字节比对,漂移在机械上不可能发生。
 *
 * 格式:生成器直接输出 Prettier(printWidth 80,本仓默认配置)的形态,
 * 与手写代码同一套 ESLint 规则,没有整文件豁免。格式化是产物的一部分 ——
 * 若放到生成之后当外部工序,「重新生成 → 逐字节比对」的守卫会永久误报。
 */

/**
 * 一条 RPC 载荷的上限,整条链路共用这一个数。
 *
 * 超限不是「这一次请求失败了」:gorilla 回 1009 并让读循环出错,于是整条物理
 * 连接被拆掉,而那条链路上跑着那台机器的全部虚拟通道,所有会话一起断线重连。
 */
export const MaxPayloadBytes = 10485760;

/**
 * 一条消息里全部附件的**原始字节**总量上限(不是 base64 之后的量)。
 *
 * 附件以 base64 过线、膨胀 4/3,所以这个数 base64 之后正好占满载荷的 8/9,
 * 余下的 1/9 留给正文、提及、模型键与其余字段。
 *
 * 它管的是总量这一维;单张上限与张数上限是另外两件事,各自在产生方那一侧。
 */
export const MaxAttachmentBytes = 6990506;
