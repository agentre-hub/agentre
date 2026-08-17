/**
 * JSON-RPC 2.0 信封(帧壳)的手写编解码。
 *
 * **为什么这一份不生成**:信封的 Go 真身是 `internal/daemon/rpc.Frame` +
 * `internal/pkg/jsonrpc.Error`,都在 wire 包**之外** —— wire 包刻意不反向依赖
 * daemon(见 wire.go 包注释与 golden_test.go 里手工组装信封样本的理由),所以
 * 生成器按定义看不到它们,追进去就是把那条分层约束打穿。
 *
 * 这份手抄本因此是可接受的:JSON-RPC 2.0 的信封由 RFC 冻结,六个字段永不增减,
 * 不存在 wire.go 那种「加了字段没人同步」的漂移面。真正会长字段的是帧体
 * (params / result),而帧体全部由 `codec.gen.ts` 从 Go 生成。
 */
import {
  type WireObject,
  decodeWire,
  encodeWire,
  reqNum,
  reqStr,
} from "./runtime";

/** mirror jsonrpc.Error(JSON-RPC 2.0 标准 error object)。 */
export interface WireError extends WireObject {
  code: number;
  message: string;
  data?: unknown;
}

/** mirror daemon/rpc.Frame:一种 shape 同时承载请求 / 响应 / 通知。 */
export interface WireFrame extends WireObject {
  jsonrpc: string;
  /** 请求/响应才有;通知缺省。Go 侧是 json.RawMessage,协议里用 number/string。 */
  id?: number | string | null;
  method?: string;
  params?: unknown;
  result?: unknown;
  error?: WireError | null;
}

export function decodeWireError(v: unknown): WireError {
  return decodeWire<WireError>(v, "Frame.error", (o) => {
    o.code = reqNum(o.code, "error.code");
    o.message = reqStr(o.message, "error.message");
  });
}

export function decodeFrame(v: unknown): WireFrame {
  return decodeWire<WireFrame>(v, "Frame", (o) => {
    o.jsonrpc = reqStr(o.jsonrpc, "Frame.jsonrpc");
    if (
      o.id !== undefined &&
      typeof o.id !== "number" &&
      typeof o.id !== "string" &&
      o.id !== null
    ) {
      throw new TypeError("wire: Frame.id 只支持 number/string/null");
    }
    if (o.method !== undefined) {
      o.method = reqStr(o.method, "Frame.method");
    }
    if (o.error !== undefined && o.error !== null) {
      o.error = decodeWireError(o.error);
    }
  });
}

export function encodeFrame(f: WireFrame): string {
  return encodeWire(f);
}
