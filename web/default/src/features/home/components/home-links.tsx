import type { ReactNode } from 'react'
import { Link } from '@tanstack/react-router'
import { cn } from '@/lib/utils'

const motion =
  'transition-transform duration-300 ease-out hover:-translate-y-0.5 active:translate-y-0 active:scale-[0.98] focus-visible:outline-none motion-reduce:transition-none motion-reduce:hover:translate-y-0 motion-reduce:active:scale-100'

export const homeAccent = 'text-[#1b55e2] dark:text-[#78a0f0]'
export const homeMuted = 'text-[#5a6072] dark:text-[#9aa1b2]'

/** 章节小标签：参考站每个板块都以一枚描边胶囊开场 */
export function Eyebrow(props: { children: ReactNode; className?: string }) {
  return (
    <span
      className={cn(
        'inline-flex h-7 items-center rounded-full bg-black/4 px-3 text-[12px] font-medium tracking-[0.08em] text-[#0b1c47] ring-1 ring-black/8 dark:bg-white/6 dark:text-[#eef1f6] dark:ring-white/12',
        props.className
      )}
    >
      {props.children}
    </span>
  )
}

/** 板块大标题，行高收紧到 1.08，与参考站一致 */
export function SectionHeading(props: {
  children: ReactNode
  className?: string
}) {
  return (
    <h2
      className={cn(
        'text-[clamp(1.875rem,4.2vw,2.875rem)] leading-[1.08] font-extrabold tracking-[-0.03em] text-balance text-[#0b1c47] dark:text-[#eef1f6]',
        props.className
      )}
    >
      {props.children}
    </h2>
  )
}

/** 板块右上角的次级入口，如「查看全部方案 ↗」 */
export function SectionAside(props: {
  to: string
  children: ReactNode
  className?: string
}) {
  return (
    <Link
      to={props.to}
      className={cn(
        'inline-flex items-center gap-1 text-sm font-medium text-[#0b1c47] underline-offset-4 transition-opacity duration-200 hover:opacity-70 dark:text-[#eef1f6]',
        props.className
      )}
    >
      {props.children}
    </Link>
  )
}

export function HomePrimaryLink(props: {
  to: string
  children: ReactNode
  className?: string
}) {
  return (
    <Link
      to={props.to}
      className={cn(
        motion,
        'inline-flex h-11 items-center justify-center gap-1.5 rounded-full bg-[#0b1c47] px-5 text-sm font-medium text-white shadow-[0_1px_2px_rgba(0,0,0,0.18),inset_0_1px_0_rgba(255,255,255,0.18)] focus-visible:shadow-[0_0_0_3px_rgba(11,28,71,0.30)] dark:bg-[#eef1f6] dark:text-[#0b1c47] dark:focus-visible:shadow-[0_0_0_3px_rgba(238,241,246,0.28)]',
        props.className
      )}
    >
      {props.children}
    </Link>
  )
}

export function HomeSecondaryLink(props: {
  to: string
  children: ReactNode
  className?: string
}) {
  return (
    <Link
      to={props.to}
      className={cn(
        motion,
        'inline-flex h-11 items-center justify-center gap-1.5 rounded-full bg-white px-5 text-sm font-medium text-[#0b1c47] shadow-[0_1px_2px_rgba(0,0,0,0.06),inset_0_1px_0_rgba(255,255,255,0.7)] ring-1 ring-black/8 focus-visible:shadow-[0_0_0_3px_rgba(11,28,71,0.18)] dark:bg-[#161c2b] dark:text-[#eef1f6] dark:ring-white/10',
        props.className
      )}
    >
      {props.children}
    </Link>
  )
}
