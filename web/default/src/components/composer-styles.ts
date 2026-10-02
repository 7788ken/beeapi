// 创作中心两种输入卡（对话 / 生图）共用的控件样式，规则见 STYLE.md 第二章「冲突怎么判」与「层级与强调」。
// 纯样式模块，不放组件（react-refresh 要求组件文件只导出组件和常量）。
import { dashPrimaryShadow } from '@/features/dashboard/components/overview/dash-emphasis'

const pressable =
  'transition-all duration-[300ms] ease-out hover:-translate-y-0.5 active:translate-y-0 active:scale-[0.98] focus-visible:outline-none focus-visible:shadow-[0_0_0_3px_rgba(99,91,255,0.3)] disabled:pointer-events-none disabled:opacity-50 motion-reduce:transition-none motion-reduce:hover:translate-y-0 motion-reduce:active:scale-100'

/** 输入卡外框（传给 InputGroup / PromptInput 的 groupClassName）：白卡 + 多层阴影 */
export const composerFrame = 'border-border bg-card shadow-surface rounded-xl'

/** 工具栏里的选择器与参数按钮：白底细边 + 内高光，悬停上浮 */
export const composerChip = `bg-card text-foreground hover:bg-foreground/5 inline-flex h-8 max-w-full min-w-0 shrink-0 items-center gap-1.5 rounded-lg border px-2.5 text-xs font-medium shadow-[0_1px_2px_rgba(10,37,64,0.06),inset_0_1px_0_rgba(255,255,255,0.2)] ${pressable}`

/** 输入卡主操作（发送 / 生成）：一组里唯一的实心主色 */
export const composerPrimary = `bg-primary text-primary-foreground inline-flex h-8 shrink-0 items-center justify-center gap-1.5 rounded-lg px-3 text-xs font-semibold ${dashPrimaryShadow} active:shadow-[inset_0_2px_4px_rgba(0,0,0,0.2)] ${pressable}`
