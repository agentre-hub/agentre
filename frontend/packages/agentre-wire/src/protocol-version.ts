import { getOption } from "@bufbuild/protobuf";

import {
  file_agentre_wire_wire,
  protocol_version,
} from "./gen/agentre/wire/wire_pb";

/**
 * agentre ↔ agentred wire 协议版本。
 *
 * 版本号的**唯一真相**是 schema 自己:它写在 `.proto` 的
 * `(agentre.wire.protocol_version)` 文件选项上,这里从生成的 descriptor 读出来 ——
 * 和 `event-kind.ts` 读字段选项是同一套做法。Go 侧读同一格
 * (`pkg/wire/protocolversion`),两侧不再各存一份。
 *
 * 本包 `package.json` 的 `version` 现在是这个值的**复述**(它是个真的 npm 包,消费方
 * 按它 pin),由 `src/__tests__/protocol-version.test.ts` 盯着两者逐字相等。
 */
export const PROTOCOL_VERSION: string = getOption(
  file_agentre_wire_wire,
  protocol_version,
);
