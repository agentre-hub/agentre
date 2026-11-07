import * as React from "react";

import type { MatchSegment } from "./scope-tree";

/**
 * 关键词命中片段的高亮：包内只有这一档（主色加粗、无底色）。看板卡片、项目范围
 * 选择器、手机行标题与手机范围清单都画这一份，不各自再造一种 `<mark>`。
 */
export function MatchHighlight({
  segments,
  slot,
}: {
  segments: MatchSegment[];
  /** 写到每个 `<mark>` 的 data-slot 上，供宿主与测试定位。 */
  slot?: string;
}) {
  return (
    <>
      {segments.map((segment, index) =>
        segment.match ? (
          <mark
            key={index}
            data-slot={slot}
            className="bg-transparent font-semibold text-primary-text"
          >
            {segment.text}
          </mark>
        ) : (
          <React.Fragment key={index}>{segment.text}</React.Fragment>
        ),
      )}
    </>
  );
}
