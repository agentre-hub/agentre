/**
 * `RuntimeEventNotification.event` 的 oneof 与本包**手写**的那份镜像之间的漂移守卫。
 *
 * rpc.ts 里 `StructuredRuntimeEvent` 的 case 联合、以及 `encodeStructuredEvent`
 * 的 switch,都是照着 .proto 手抄的。proto 里加一个 oneof 分支,这两处都不会自动
 * 跟上,而且**两边都不会报错**:
 *
 *   - 编码侧的 switch 少一个分支 → 返回 undefined,帧悄悄编成空的;
 *   - 类型侧的联合少一个名字 → 消费方那些 `Record<RuntimeEventCase, …>` 形状的
 *     穷尽性检查(agentre-server 的 relayClient 就是)跟着少一格,新事件一路绿到
 *     线上才发现渲染不出来。
 *
 * 所以这里不比对字符串清单(那只是把手抄再抄一遍),而是从**生成的 descriptor**
 * 枚举 oneof,逐个真的走一遍 encode → decode。
 */
import { describe, expect, it } from "vitest";

import { ProtobufRpcCodec } from "../rpc";
import { RuntimeEventNotificationSchema } from "../gen/agentre/wire/wire_pb";

/** descriptor 里 `event` oneof 的全部分支名(protobuf-es 的 localName = 手写侧的 case)。 */
function oneofCaseNames(): string[] {
  const oneof = RuntimeEventNotificationSchema.oneofs.find(
    (o) => o.name === "event",
  );
  if (oneof === undefined)
    throw new Error("wire: RuntimeEventNotification 没有 event oneof");
  return oneof.fields.map((f) => f.localName);
}

describe("RuntimeEventNotification.event oneof", () => {
  it("descriptor 里的每个分支都编得出、解得回同一个 case", () => {
    const names = oneofCaseNames();
    expect(names.length).toBeGreaterThan(0);

    const roundTripped = names.map((name) => {
      const payload = ProtobufRpcCodec.encode({
        id: 0n,
        body: {
          case: "runtimeEventNotification",
          sessionId: 1,
          seq: 1,
          // 时间戳字段编码侧要过 BigInt(),缺了会抛 —— 这不是漂移,补零即可。
          event: {
            case: name,
            createdAtMs: 0,
            expiresAtMs: 0,
            resolvedAtMs: 0,
          },
        } as never,
      });
      const frame = ProtobufRpcCodec.decode(payload);
      const body = frame.body as { case: string; event?: { case?: string } };
      return body.event?.case;
    });

    expect(roundTripped).toEqual(names);
  });
});
