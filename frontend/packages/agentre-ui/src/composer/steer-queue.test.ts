import { describe, expect, it } from "vitest";

import {
  adoptSteerHandle,
  clearSteerQueue,
  consumeSteers,
  dropSteers,
  emptySteerQueue,
  enqueueSteer,
  markSteerNotCancellable,
  type SteerQueueState,
} from "./steer-queue";

/** 攒一条已经拿到权威句柄的排队消息（宿主发送成功之后的常态）。 */
function settled(
  id: string,
  text: string,
  cancellable = true,
): SteerQueueState {
  return adoptSteerHandle(
    enqueueSteer(emptySteerQueue, { id: `local-${id}`, text }),
    `local-${id}`,
    { queuedId: id, cancellable },
  );
}

describe("steer queue", () => {
  it("给定刚提交的一条插话，当入队，则它先以本地句柄挂着且不可撤销", () => {
    // 提交那一刻就要挂 chip：输入框已经被清空，这一段字此刻只存在于队列里。
    // 权威句柄要等应答，所以撤回键先不给 —— 拿本地号去撤是撤不到任何东西的。
    const state = enqueueSteer(emptySteerQueue, {
      id: "local-1",
      text: "等一下",
    });

    expect(state.items).toEqual([
      { id: "local-1", text: "等一下", cancellable: false, pending: true },
    ]);
  });

  it("给定应答带回权威句柄，当认领，则句柄与可撤销随之落定", () => {
    const state = settled("desktop-42", "等一下");

    expect(state.items).toEqual([
      { id: "desktop-42", text: "等一下", cancellable: true, pending: false },
    ]);
  });

  it("给定对端还没升级（应答没有句柄），当认领，则这条留在降级档：本地句柄、锁住", () => {
    // 老对端答的仍是空壳。它照样排进去了，字不能凭空消失；但我们没有它认的号，
    // 撤不掉，也只能靠文本去抵消消费。
    const state = adoptSteerHandle(
      enqueueSteer(emptySteerQueue, { id: "local-1", text: "等一下" }),
      "local-1",
      { queuedId: "", cancellable: true },
    );

    expect(state.items).toEqual([
      { id: "local-1", text: "等一下", cancellable: false, pending: true },
    ]);
  });

  it("给定消费事件先于应答到达，当认领那个已被消费的句柄，则这条不复活", () => {
    // 对端取得快时事件会插到应答前面。不记这一笔的话，认领会把一条已经进了转录的
    // 消息重新挂回队列，而此后再没有任何东西能把它清掉。
    const enqueued = enqueueSteer(emptySteerQueue, {
      id: "local-1",
      text: "等一下",
    });
    const consumed = consumeSteers(enqueued, [
      { queuedId: "desktop-42", text: "等一下" },
    ]);

    const state = adoptSteerHandle(consumed, "local-1", {
      queuedId: "desktop-42",
      cancellable: true,
    });

    expect(state.items).toEqual([]);
  });

  it("给定后端取走了一条，当按句柄消费，则对应的那条消失", () => {
    const state = consumeSteers(settled("desktop-42", "等一下"), [
      { queuedId: "desktop-42", text: "等一下" },
    ]);

    expect(state.items).toEqual([]);
  });

  it("给定别的端排的消息被消费，当句柄对不上，则本地这条一动不动", () => {
    // 同一条会话上别的端也能插话，它的消费事件同样扇给这一屏。按文本猜会把自己
    // 那条清掉 —— 屏幕上那句话没了，可它还排在对端的队列里。
    const state = consumeSteers(settled("desktop-42", "等一下"), [
      { queuedId: "other-7", text: "等一下" },
    ]);

    expect(state.items).toHaveLength(1);
    expect(state.items[0].id).toBe("desktop-42");
  });

  it("给定降级档的两条同文本，当消费到达，则只抵消最早那一条", () => {
    const two = enqueueSteer(
      enqueueSteer(emptySteerQueue, { id: "local-1", text: "等一下" }),
      { id: "local-2", text: "等一下" },
    );

    const state = consumeSteers(two, [
      { queuedId: "desktop-42", text: "等一下" },
    ]);

    expect(state.items.map((q) => q.id)).toEqual(["local-2"]);
  });

  it("给定同一条消费事件到达两次，当再消费一次，则什么都不发生", () => {
    // 同一条 steer_consumed 会以预览与持久两种形态各到一次。
    const once = consumeSteers(settled("desktop-42", "等一下"), [
      { queuedId: "desktop-42", text: "等一下" },
    ]);
    const twice = consumeSteers(once, [
      { queuedId: "desktop-42", text: "等一下" },
    ]);

    expect(twice.items).toEqual([]);
  });

  it("给定用户撤回了一条，当按对端返回的句柄移除，则它从队列里消失", () => {
    const state = dropSteers(settled("desktop-42", "等一下"), ["desktop-42"]);

    expect(state.items).toEqual([]);
  });

  it("给定撤回被对端拒了，当标记这条撤不掉，则它留在原位、转成锁住并带上对端那句话", () => {
    // 撤不掉最常见的原因就是它刚好被取走了 —— 那时紧接着的消费事件会把它清掉。
    // 所以这里不弹全局提示、也不乐观清掉：那条字还排着，它只是撤不动了。
    const state = markSteerNotCancellable(
      settled("desktop-42", "等一下"),
      "desktop-42",
      "这条已经被取走了",
    );

    expect(state.items).toEqual([
      {
        id: "desktop-42",
        text: "等一下",
        cancellable: false,
        pending: false,
        note: "这条已经被取走了",
      },
    ]);
  });

  it("给定一轮结束，当清空队列，则连同已消费的记录一起归零", () => {
    const state = clearSteerQueue(settled("desktop-42", "等一下"));

    expect(state).toEqual(emptySteerQueue);
  });
});
