/**
 * 次按钮（STYLE.md 第二章）：白底描边 + 内高光，悬停上浮，按下换内凹阴影。
 * 配合 Button 的 outline 变体使用；outline 自带的 dark:border-input 换成与卡片分得开的 border。
 */
export const secondaryButtonClass =
  'bg-card dark:border-border shadow-[0_1px_2px_rgba(10,37,64,0.06),inset_0_1px_0_rgba(255,255,255,0.2)] transition-all duration-[300ms] ease-out hover:-translate-y-0.5 active:translate-y-0 active:scale-[0.98] active:shadow-[inset_0_2px_4px_rgba(0,0,0,0.2)] motion-reduce:transition-none motion-reduce:hover:translate-y-0 motion-reduce:active:scale-100'
