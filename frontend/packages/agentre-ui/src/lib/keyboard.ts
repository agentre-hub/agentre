export function isOpenInNewTabModifier(e: {
  metaKey?: boolean;
  ctrlKey?: boolean;
}): boolean {
  return Boolean(e.metaKey || e.ctrlKey);
}
