/**
 * 查询失败时是否换成全站错误页。
 *
 * 模板默认对任何 500 都 router.navigate('/500')：一个子面板拉不到数据，整个后台
 * 就被换成一张通用错误页，页面自己写好的「失败原因 + 上次成功数据」永远显示不出来。
 * 带 inlineError 的查询自己负责在页面内如实显示失败，不掀掉整个后台。
 */
export function shouldShowErrorPage(
  status: number | undefined,
  meta?: Record<string, unknown>
): boolean {
  if (status !== 500) return false
  return meta?.inlineError !== true
}
