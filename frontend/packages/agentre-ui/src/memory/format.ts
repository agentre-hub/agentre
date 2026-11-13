function pad2(n: number): string {
  return String(n).padStart(2, "0");
}

/**
 * 记忆行上的日期：今年的只写「月-日」，往年的带上年份。数字日期不随语言变化，
 * 所以不走 i18n。`ms` 为 0（没有时间）时返回空串。
 */
export function formatMemoryDate(ms: number, now: number = Date.now()): string {
  if (!ms) return "";
  const date = new Date(ms);
  const monthDay = `${pad2(date.getMonth() + 1)}-${pad2(date.getDate())}`;
  return date.getFullYear() === new Date(now).getFullYear()
    ? monthDay
    : `${date.getFullYear()}-${monthDay}`;
}
