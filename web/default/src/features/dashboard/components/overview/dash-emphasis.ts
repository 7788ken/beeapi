// 仪表盘"层级与强调"：每组同级元素只给一个实心主色焦点，规则见 STYLE.md「层级与强调」。
// 纯样式模块，不放组件（react-refresh 要求组件文件只导出组件和常量）。
import { cn } from '@/lib/utils'

/** 实心主色元素共用的外阴影 + 内高光：主按钮、激活项、主卡、第 1 名。 */
export const dashPrimaryShadow =
  'shadow-[0_2px_5px_rgba(99,91,255,0.4),inset_0_1px_0_rgba(255,255,255,0.2)]'

/** 主卡：一组卡片里最多一张，放第一位，装本页要回答的核心数字。紫底上只放白字。 */
export const dashHeroCard = `bg-primary text-primary-foreground rounded-xl border border-transparent ${dashPrimaryShadow}`

/** 分段切换外框：白底描边，总高 32px，与旁边 h-8 的按钮、输入框齐平。 */
export const dashSegTrack =
  'bg-card inline-flex shrink-0 items-center gap-0.5 rounded-lg border p-0.5'

const segItemBase =
  'inline-flex h-6.5 shrink-0 items-center justify-center gap-1.5 rounded-md px-3 text-xs font-medium whitespace-nowrap transition-all duration-[300ms] ease-out focus-visible:shadow-[0_0_0_3px_rgba(99,91,255,0.3)] focus-visible:outline-none active:scale-[0.98] disabled:pointer-events-none disabled:opacity-50 motion-reduce:transition-none motion-reduce:active:scale-100'

/** 分段切换选项：激活项实心主色白字，同一组只有它一个实心。 */
export function dashSegItem(active: boolean) {
  return cn(
    segItemBase,
    active
      ? `bg-primary text-primary-foreground ${dashPrimaryShadow}`
      : 'text-muted-foreground hover:bg-foreground/5 hover:text-foreground'
  )
}

/** 排行名次徽标配色：第 1 名实心主色，2–3 名主色描边，其余灰底；尺寸与形状由调用方定。 */
export function dashRankTone(rank: number) {
  if (rank === 1) {
    return 'bg-primary text-primary-foreground shadow-[0_1px_3px_rgba(99,91,255,0.45),inset_0_1px_0_rgba(255,255,255,0.2)]'
  }
  if (rank <= 3) {
    return 'text-primary ring-primary/40 dark:text-primary-foreground dark:ring-primary/70 ring-1 ring-inset'
  }
  return 'bg-foreground/5 text-muted-foreground'
}
