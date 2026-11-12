import { MemoryEntryRow, type MemoryOwnerRef } from "@agentre-hub/agentre-ui";

import { useMemoryOwnerEntry } from "./use-memory-owner-entry";

/** 详情页里的「记忆 N 条」：点一下去记忆页并选中这个归属。读不到就不画。 */
export function OrgMemoryRow({ owner }: { owner: MemoryOwnerRef }) {
  const entry = useMemoryOwnerEntry(owner);
  if (!entry) return null;
  return <MemoryEntryRow count={entry.count} onOpen={entry.open} />;
}
