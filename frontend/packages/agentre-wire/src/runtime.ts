/**
 * wire codec 的手写运行时:解码骨架 + 校验助手。
 *
 * 这一层**不生成**,因为它不随 Go 侧的 wire 结构变化 —— 帧类型、字段名、可选性
 * 全部由 `codec.gen.ts` 从 `wire.go` 生成(见该文件头),这里只提供它调用的那几个
 * 稳定原语。生成器因此不必产出通用逻辑,产物也只剩「结构声明 + 逐字段校验」。
 *
 * 未知字段不丢弃:解码时把 codec 不认识的 JSON 键原样留在对象顶层
 * (WireObject 的索引签名),编码时随已知字段一起序列化出去。老版本 agentred /
 * 未来扩展多加的字段不会在这一层被吃掉。
 */

/** 所有 wire 帧的基类型:已知字段之外的键原样保留。 */
export interface WireObject {
  /** 本 codec 不认识的键原样保留;编码时随已知字段一起输出。 */
  [key: string]: unknown;
}

/** 解码过程中的中间形态:JSON 解析出来的裸对象。 */
export type JsonObject = Record<string, unknown>;

export function asObject(v: unknown, what: string): JsonObject {
  if (typeof v !== "object" || v === null || Array.isArray(v)) {
    throw new TypeError(`wire: ${what} 应是 JSON 对象,实际是 ${typeof v}`);
  }
  return v as JsonObject;
}

export function reqNum(v: unknown, what: string): number {
  if (typeof v !== "number" || !Number.isFinite(v)) {
    throw new TypeError(`wire: ${what} 缺少必填数字字段`);
  }
  return v;
}

export function reqStr(v: unknown, what: string): string {
  if (typeof v !== "string") {
    throw new TypeError(`wire: ${what} 缺少必填字符串字段`);
  }
  return v;
}

export function reqBool(v: unknown, what: string): boolean {
  if (typeof v !== "boolean") {
    throw new TypeError(`wire: ${what} 缺少必填布尔字段`);
  }
  return v;
}

export function optNum(v: unknown, what: string): number | undefined {
  if (v === undefined) return undefined;
  return reqNum(v, what);
}

export function optStr(v: unknown, what: string): string | undefined {
  if (v === undefined) return undefined;
  return reqStr(v, what);
}

export function optBool(v: unknown, what: string): boolean | undefined {
  if (v === undefined) return undefined;
  if (typeof v !== "boolean") {
    throw new TypeError(`wire: ${what} 布尔字段类型错误`);
  }
  return v;
}

export function reqArr(v: unknown, what: string): unknown[] {
  if (!Array.isArray(v)) {
    throw new TypeError(`wire: ${what} 应是数组`);
  }
  return v;
}

export function optArr(v: unknown, what: string): unknown[] | undefined {
  if (v === undefined) return undefined;
  return reqArr(v, what);
}

/** 必填数组,并把每个元素交给 decode 强类型化。 */
export function reqArrOf<T>(
  v: unknown,
  what: string,
  decode: (e: unknown) => T,
): T[] {
  return reqArr(v, what).map(decode);
}

/** 可选数组;缺席时返回 undefined(键因此在序列化时消失,同 Go omitempty)。 */
export function optArrOf<T>(
  v: unknown,
  what: string,
  decode: (e: unknown) => T,
): T[] | undefined {
  if (v === undefined) return undefined;
  return reqArrOf(v, what, decode);
}

/**
 * 可选的嵌套帧。Go 指针字段配 omitempty 时 nil 直接省键,但对端显式送 null 也
 * 不该让整帧解码失败 —— null 原样留着,由消费方按可空处理。
 */
export function optOf<T>(
  v: unknown,
  decode: (e: unknown) => T,
): T | null | undefined {
  if (v === undefined) return undefined;
  if (v === null) return null;
  return decode(v);
}

export function reqObj(v: unknown, what: string): JsonObject {
  if (typeof v !== "object" || v === null || Array.isArray(v)) {
    throw new TypeError(`wire: ${what} 应是对象`);
  }
  return v as JsonObject;
}

export function optObj(v: unknown, what: string): JsonObject | undefined {
  if (v === undefined) return undefined;
  return reqObj(v, what);
}

/**
 * 通用解码骨架:复制原对象(未知字段因此保留在顶层),再把已知字段逐个
 * 强类型化(coerce)。coerce 里对缺失可选字段赋值 undefined,序列化时这条键
 * 会被 JSON.stringify 丢掉 —— 与 Go omitempty 省略零值的行为一致。
 */
export function decodeWire<T>(
  v: unknown,
  what: string,
  coerce: (o: JsonObject) => void,
): T {
  const src = asObject(v, what);
  const out: JsonObject = { ...src };
  coerce(out);
  return out as unknown as T;
}

/**
 * 编码即 JSON.stringify:解码对象把未知字段留在顶层,已知字段与 Go 同键名,
 * 序列化出去就是协议字节。encode 与 decode 互为逆(黄金样本测试断言
 * decode → encode → parse 与原始帧逐字段相同)。
 */
export function encodeWire(v: unknown): string {
  return JSON.stringify(v);
}
