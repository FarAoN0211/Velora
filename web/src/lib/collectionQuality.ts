/** Keep every available library version; only separate their presentation by resolution. */
export function collectionQualityRows<T extends { quality?: string }>(items: T[]): { title: string; items: T[] }[] {
 const groups = new Map<string,T[]>();
 for (const item of items) {
  const raw=(item.quality??'').trim();
  const title=/^(4k|2160p|uhd)$/i.test(raw)?'4K':raw||'Calidad sin identificar';
  groups.set(title,[...(groups.get(title)??[]),item]);
 }
 const rank=(title:string)=>title==='4K'?2160:title==='SD'?480:parseInt(title,10)||0;
 return [...groups].sort(([a],[b])=>rank(b)-rank(a)||a.localeCompare(b)).map(([title,items])=>({title,items}));
}
