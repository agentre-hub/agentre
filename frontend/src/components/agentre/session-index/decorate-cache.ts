// frontend/src/components/agentre/session-index/decorate-cache.ts
//
// 装饰结果缓存：同一个 (行对象, meta) 装饰出来的还是同一个对象，没变的行才能在
// 兄弟行变化时保持引用不变。依赖（axis / 解析函数 / t）一变整张表作废。
export class DecorateCache<Row extends object> {
  private deps: readonly unknown[] = [];
  private map = new WeakMap<Row, { meta: unknown; out: Row }>();

  get(
    row: Row,
    meta: unknown,
    deps: readonly unknown[],
    compute: () => Row,
  ): Row {
    if (
      this.deps.length !== deps.length ||
      this.deps.some((d, i) => d !== deps[i])
    ) {
      this.deps = deps;
      this.map = new WeakMap();
    }
    const hit = this.map.get(row);
    if (hit && hit.meta === meta) return hit.out;
    const out = compute();
    this.map.set(row, { meta, out });
    return out;
  }
}
