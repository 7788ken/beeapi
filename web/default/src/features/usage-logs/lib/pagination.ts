export function formatLogTotal(total: number, isCapped: boolean): string {
  const formattedTotal = total.toLocaleString()
  return isCapped ? `${formattedTotal}+` : formattedTotal
}
