import type { ReactNode } from 'react'
import { useStatus } from '@/hooks/use-status'
import { AnnouncementsPanel } from './announcements-panel'
import { ApiInfoPanel } from './api-info-panel'
import { FAQPanel } from './faq-panel'
import { MonthUsageChart } from './month-usage-chart'
import { PerformanceHealthPanel } from './performance-health-panel'
import { SummaryCards } from './summary-cards'
import { UptimePanel } from './uptime-panel'

// 不加 items-start：grid 行默认 stretch，两栏才会等高，卡片上的 h-full 才有意义
function TwoThirdsRow(props: { main: ReactNode; side: ReactNode }) {
  return (
    <div className='grid min-w-0 grid-cols-1 gap-6 lg:grid-cols-3'>
      <div className='grid min-w-0 lg:col-span-2'>{props.main}</div>
      <div className='grid min-w-0'>{props.side}</div>
    </div>
  )
}

export function OverviewConsole(props: { showPerformance: boolean }) {
  const { status } = useStatus()
  // 后台「启用 Uptime Kuma 面板」开关；关闭时不渲染运行时间卡，旁边的卡占满整行
  const showUptime = status?.['uptime_kuma_enabled'] === true

  return (
    <div className='flex min-w-0 flex-col gap-6'>
      <MonthUsageChart />
      <SummaryCards showPerformance={props.showPerformance} />
      {props.showPerformance ? (
        <TwoThirdsRow
          main={<PerformanceHealthPanel />}
          side={<AnnouncementsPanel />}
        />
      ) : showUptime ? (
        <TwoThirdsRow main={<UptimePanel />} side={<AnnouncementsPanel />} />
      ) : (
        <AnnouncementsPanel />
      )}
      <ApiInfoPanel />
      {props.showPerformance && showUptime ? <UptimePanel /> : null}
      {/* 问答是手风琴，展开会改变高度：单独占一行，不和别的卡拉成等高 */}
      <FAQPanel />
    </div>
  )
}
