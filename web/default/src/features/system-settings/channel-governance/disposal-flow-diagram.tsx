import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import {
  getGovernanceDistribution,
  type ChannelGovernanceDistribution,
} from './api'
import { openGovernanceBlock, tabFromSection } from './blocks'
import type { ChannelGovernanceSectionId } from './section-registry.tsx'

/**
 * 处置链路图：触发源 → 处置 → 恢复归谁管。
 *
 * 手写内联 SVG，不引图表库——本前端目前零图表依赖（只有 lucide-react 图标），而本文件里
 * 已有 30+ 个组件在用内联 SVG，这是既有做法。
 *
 * 三件事是这张图存在的理由：
 *  1. 未生效的路径灰显 + 虚线，取决于**当前表单值**而不是已保存值：改完开关立刻看到后果。
 *  2. 健康度关闭时降级 / 降级探测 / 恢复探活三格灰显，但「巡检测通启用」那格**保持生效**。
 *     这条不对称是全图最大的价值：ShouldEnableChannel 只看 AutomaticEnableChannelEnabled
 *     与 status，不看健康度开关，所以巡检救得了停用、救不了降级。
 *  3. 节点上挂实时计数，灰节点上的非零计数就是「冻结在这一档、无自动路径可出去」。
 *
 * 颜色全部走主题 token，深浅两套主题都可读；图外另有一份等价的文本表格作无障碍替代。
 */

// ── 布局常量 ──
const VIEW_W = 920
const VIEW_H = 486
const NODE_H = 62
const COLS = [
  { x: 12, w: 190 },
  { x: 340, w: 210 },
  { x: 686, w: 222 },
] as const
const HEADER_Y = 18
const ARROW_ACTIVE = 'governance-flow-arrow-active'
const ARROW_IDLE = 'governance-flow-arrow-idle'

type NodeId =
  | 'live'
  | 'probe'
  | 'removeModel'
  | 'degrade'
  | 'stopChannel'
  | 'permanentLock'
  | 'recheckRestore'
  | 'degradeProbe'
  | 'recoveryProbe'
  | 'probeEnable'
  | 'manualOnly'

type FlowNode = {
  id: NodeId
  col: 0 | 1 | 2
  y: number
  title: string
  /** 实时计数或补充说明，渲染在标题下方 */
  detail?: string
  active: boolean
  severe?: boolean
  section?: ChannelGovernanceSectionId
}

type FlowEdge = {
  from: NodeId
  to: NodeId
  active: boolean
  /** 无障碍表格里描述这条路径何时生效 */
  condition: string
}

export type DisposalFlowContext = {
  /** 当前表单值（form.watch），不是已保存值 */
  autoTestEnabled: boolean
  healthEnabled: boolean
  modelMissingRemovalEnabled: boolean
  /** 以下为只读上下文，归属其他 section */
  modelRateLimitRemovalEnabled: boolean
  modelForbiddenRemovalEnabled: boolean
  automaticDisableChannelEnabled: boolean
  automaticEnableChannelEnabled: boolean
  disableThreshold: number
  maxDegradeLevel: number
  degradeProbeEnabled: boolean
  recoveryStrategy: string
  rebounceProtectionMinutes: number
}

/**
 * 把译文切成最多两行。Latin 按空格断，CJK 无空格则按字数硬断，
 * 两种 locale 都不会溢出节点框。
 */
function wrapLabel(text: string, maxChars: number): string[] {
  if (text.length <= maxChars) return [text]
  if (text.includes(' ')) {
    const words = text.split(' ')
    const first: string[] = []
    let width = 0
    while (words.length > 0 && width + words[0].length + 1 <= maxChars) {
      width += words[0].length + 1
      first.push(words.shift() as string)
    }
    if (first.length === 0) first.push(words.shift() as string)
    const second = words.join(' ')
    return second ? [first.join(' '), second] : [first.join(' ')]
  }
  return [text.slice(0, maxChars), text.slice(maxChars)]
}

/** CJK 每字占位接近 Latin 的两倍，按是否含空格粗判即可，不需要真去量文本。 */
function maxCharsFor(text: string, boxWidth: number): number {
  const perChar = text.includes(' ') ? 6.3 : 12
  return Math.max(6, Math.floor((boxWidth - 24) / perChar))
}

function buildFlow(
  ctx: DisposalFlowContext,
  dist: ChannelGovernanceDistribution | undefined,
  t: (key: string, opts?: Record<string, unknown>) => string
): { nodes: FlowNode[]; edges: FlowEdge[] } {
  const removalAnyEnabled =
    ctx.modelMissingRemovalEnabled ||
    ctx.modelRateLimitRemovalEnabled ||
    ctx.modelForbiddenRemovalEnabled
  // 停渠道有两条互不相干的触发源，其中「线上连错触顶」不受自动禁用总开关约束。
  const stopByKeyword = ctx.automaticDisableChannelEnabled
  const stopByStreak = ctx.healthEnabled && ctx.disableThreshold > 0
  const probeStop = ctx.autoTestEnabled && ctx.automaticDisableChannelEnabled
  const probeEnable = ctx.autoTestEnabled && ctx.automaticEnableChannelEnabled
  const degradeProbe = ctx.healthEnabled && ctx.degradeProbeEnabled
  const recoveryProbe = ctx.healthEnabled && ctx.recoveryStrategy === 'probe'
  const rebounceOn = ctx.rebounceProtectionMinutes > 0

  const count = (value: number | undefined) => value ?? 0
  // 计数取不到时（接口失败或仍在加载）必须显示占位符，不能落成 0。
  // 「0 个降级渠道」与「计数取不到」是两件完全不同的事，前者会让人以为一切正常。
  const detailOr = (text: string) =>
    dist === undefined ? t('Counts unavailable') : text
  const channels = (value: number | undefined) =>
    detailOr(t('{{count}} channels', { count: count(value) }))

  const nodes: FlowNode[] = [
    {
      id: 'live',
      col: 0,
      y: 84,
      title: t('Live traffic'),
      detail: channels(dist?.enabled),
      active: true,
    },
    {
      id: 'probe',
      col: 0,
      y: 268,
      title: t('Scheduled probing'),
      detail: detailOr(
        t('skips {{count}} verify-disabled', {
          count: count(dist?.verify_disabled),
        })
      ),
      active: ctx.autoTestEnabled,
      section: 'probes',
    },
    {
      id: 'removeModel',
      col: 1,
      y: 46,
      title: t('Remove the model'),
      detail: detailOr(
        t('{{count}} models out', {
          count: count(dist?.removed_models?.total_models),
        })
      ),
      active: removalAnyEnabled,
      section: 'removal',
    },
    {
      id: 'degrade',
      col: 1,
      y: 144,
      title: t('Degrade L1-L{{max}}', { max: ctx.maxDegradeLevel }),
      detail: channels(dist?.degraded_channels),
      active: ctx.healthEnabled,
      section: 'degradation',
    },
    {
      id: 'stopChannel',
      col: 1,
      y: 242,
      title: t('Stop the channel'),
      detail: channels(dist?.auto_disabled),
      active: stopByKeyword || stopByStreak || probeStop,
      severe: true,
      section: 'degradation',
    },
    {
      id: 'permanentLock',
      col: 1,
      y: 340,
      title: t('Permanent lock'),
      detail: channels(dist?.permanent_disabled),
      active: rebounceOn,
      severe: true,
      section: 'degradation',
    },
    {
      id: 'recheckRestore',
      col: 2,
      y: 46,
      title: t('Recheck adds it back'),
      active: removalAnyEnabled,
      section: 'removal',
    },
    {
      id: 'degradeProbe',
      col: 2,
      y: 124,
      title: t('Degrade probing upgrades it'),
      active: degradeProbe,
      section: 'probes',
    },
    {
      id: 'recoveryProbe',
      col: 2,
      y: 202,
      title: t('Recovery probing'),
      active: recoveryProbe,
      section: 'probes',
    },
    {
      id: 'probeEnable',
      col: 2,
      y: 280,
      title: t('A passing probe re-enables it'),
      active: probeEnable,
      section: 'probes',
    },
    {
      id: 'manualOnly',
      col: 2,
      y: 358,
      title: t('Manual recovery only'),
      active: true,
    },
  ]

  const edges: FlowEdge[] = [
    {
      from: 'live',
      to: 'removeModel',
      active: removalAnyEnabled,
      condition: t('Any of the three model-level removal switches is on'),
    },
    {
      from: 'live',
      to: 'degrade',
      active: ctx.healthEnabled,
      condition: t('Channel health is on'),
    },
    {
      from: 'live',
      to: 'stopChannel',
      active: stopByKeyword || stopByStreak,
      condition: t(
        'A failure keyword or status code matches (needs auto-disable), or the live error streak reaches the stop threshold (needs channel health, and is not bound by the auto-disable switch)'
      ),
    },
    {
      from: 'probe',
      to: 'stopChannel',
      active: probeStop,
      condition: t('Scheduled probing is on and auto-disable is on'),
    },
    {
      from: 'stopChannel',
      to: 'permanentLock',
      active: rebounceOn,
      condition: t('Rebound protection is on (window in minutes is above zero)'),
    },
    {
      from: 'removeModel',
      to: 'recheckRestore',
      active: removalAnyEnabled,
      condition: t(
        'Always, once a model is out: the recheck task is independent of the channel health switch'
      ),
    },
    {
      from: 'degrade',
      to: 'degradeProbe',
      active: degradeProbe,
      condition: t('Channel health is on and degrade probing is on'),
    },
    {
      from: 'stopChannel',
      to: 'recoveryProbe',
      active: recoveryProbe,
      condition: t('Channel health is on and the recovery strategy is probing'),
    },
    {
      from: 'stopChannel',
      to: 'probeEnable',
      active: probeEnable,
      condition: t(
        'Scheduled probing is on and auto-enable is on. This one does not depend on channel health at all'
      ),
    },
    {
      from: 'degrade',
      to: 'manualOnly',
      active: !ctx.healthEnabled,
      condition: t(
        'Channel health is off, so already degraded channels are frozen with no automatic way back up'
      ),
    },
    {
      from: 'permanentLock',
      to: 'manualOnly',
      active: true,
      condition: t('Always: recovery probing skips permanently locked channels'),
    },
  ]

  return { nodes, edges }
}

function nodeBox(node: FlowNode) {
  const col = COLS[node.col]
  return { x: col.x, y: node.y, w: col.w, h: NODE_H }
}

/** 从左侧节点右缘到右侧节点左缘的三次贝塞尔，横向出入保证箭头不压到节点边框。 */
function edgePath(from: FlowNode, to: FlowNode): string {
  const a = nodeBox(from)
  const b = nodeBox(to)
  const x1 = a.x + a.w
  const y1 = a.y + a.h / 2
  const x2 = b.x
  const y2 = b.y + b.h / 2
  const dx = Math.max(40, (x2 - x1) / 2)
  return `M ${x1} ${y1} C ${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}`
}

export function DisposalFlowDiagram({ ctx }: { ctx: DisposalFlowContext }) {
  const { t } = useTranslation()

  const { data } = useQuery({
    queryKey: ['channel-governance', 'distribution'],
    queryFn: getGovernanceDistribution,
    staleTime: 60 * 1000,
    refetchOnWindowFocus: false,
  })
  const dist = data?.success ? data.data : undefined

  const { nodes, edges } = useMemo(() => buildFlow(ctx, dist, t), [ctx, dist, t])
  const nodeById = useMemo(
    () => new Map(nodes.map((node) => [node.id, node])),
    [nodes]
  )

  const goToSection = (section: ChannelGovernanceSectionId | undefined) => {
    if (!section) return
    openGovernanceBlock(tabFromSection(section))
  }

  const columnTitles = [
    t('Trigger'),
    t('Disposition'),
    t('Who brings it back'),
  ]

  return (
    <div className='space-y-3'>
      {/* 窄屏横向滚动，SVG 不压缩变形 */}
      <div className='overflow-x-auto rounded-lg border'>
        <svg
          viewBox={`0 0 ${VIEW_W} ${VIEW_H}`}
          width={VIEW_W}
          height={VIEW_H}
          className='block max-w-none'
          role='img'
          aria-label={t(
            'Diagram of channel disposal paths: what triggers each action, which action is taken, and what can undo it. Paths that are currently inactive are drawn greyed out and dashed. An equivalent text table follows below the diagram.'
          )}
        >
          <defs>
            <marker
              id={ARROW_ACTIVE}
              viewBox='0 0 10 10'
              refX='9'
              refY='5'
              markerWidth='6'
              markerHeight='6'
              orient='auto-start-reverse'
            >
              <path d='M 0 0 L 10 5 L 0 10 z' fill='var(--primary)' />
            </marker>
            <marker
              id={ARROW_IDLE}
              viewBox='0 0 10 10'
              refX='9'
              refY='5'
              markerWidth='6'
              markerHeight='6'
              orient='auto-start-reverse'
            >
              <path
                d='M 0 0 L 10 5 L 0 10 z'
                fill='var(--muted-foreground)'
                opacity='0.5'
              />
            </marker>
          </defs>

          {COLS.map((col, index) => (
            <text
              key={col.x}
              x={col.x}
              y={HEADER_Y}
              fontSize='11'
              fontWeight='600'
              letterSpacing='0.06em'
              fill='var(--muted-foreground)'
            >
              {columnTitles[index].toUpperCase()}
            </text>
          ))}

          {/* 连线先画，节点覆盖其上，避免线头压在文字上 */}
          {edges.map((edge) => {
            const from = nodeById.get(edge.from)
            const to = nodeById.get(edge.to)
            if (!from || !to) return null
            return (
              <path
                key={`${edge.from}->${edge.to}`}
                d={edgePath(from, to)}
                fill='none'
                stroke={
                  edge.active ? 'var(--primary)' : 'var(--muted-foreground)'
                }
                strokeWidth={edge.active ? 1.75 : 1.25}
                strokeDasharray={edge.active ? undefined : '5 4'}
                opacity={edge.active ? 0.85 : 0.4}
                markerEnd={`url(#${edge.active ? ARROW_ACTIVE : ARROW_IDLE})`}
              />
            )
          })}

          {nodes.map((node) => {
            const box = nodeBox(node)
            const titleLines = wrapLabel(
              node.title,
              maxCharsFor(node.title, box.w)
            )
            const stroke = node.active
              ? node.severe
                ? 'var(--destructive)'
                : 'var(--primary)'
              : 'var(--border)'
            const titleFill = node.active
              ? 'var(--card-foreground)'
              : 'var(--muted-foreground)'
            const titleTop =
              box.y + (titleLines.length > 1 ? 22 : node.detail ? 26 : 36)
            return (
              <g
                key={node.id}
                onClick={() => goToSection(node.section)}
                className={node.section ? 'cursor-pointer' : undefined}
                opacity={node.active ? 1 : 0.62}
              >
                <rect
                  x={box.x}
                  y={box.y}
                  width={box.w}
                  height={box.h}
                  rx='8'
                  fill='var(--card)'
                  stroke={stroke}
                  strokeWidth={node.active ? 1.5 : 1}
                  strokeDasharray={node.active ? undefined : '5 4'}
                />
                {titleLines.map((line, index) => (
                  <text
                    key={line + index}
                    x={box.x + 12}
                    y={titleTop + index * 15}
                    fontSize='12.5'
                    fontWeight='600'
                    fill={titleFill}
                  >
                    {line}
                  </text>
                ))}
                {node.detail && (
                  <text
                    x={box.x + 12}
                    y={box.y + box.h - 12}
                    fontSize='10.5'
                    fill='var(--muted-foreground)'
                  >
                    {node.detail}
                  </text>
                )}
              </g>
            )
          })}
        </svg>
      </div>

      <p className='text-muted-foreground text-sm'>
        {ctx.healthEnabled
          ? t(
              'Channel health is on, so degraded channels can climb back up on their own.'
            )
          : t(
              'Channel health is off: already degraded channels are frozen. Their degrade level, original priority and original weight stay in the database, no automatic path can lift them back up, and only manual recovery will. Channels that were stopped are a different case — a passing scheduled probe still re-enables those.'
            )}
      </p>

      {/* 无障碍替代：图里的信息不能只存在于图里 */}
      <details className='rounded-lg border'>
        <summary className='cursor-pointer px-4 py-2 text-sm font-medium'>
          {t('Same paths as a table')}
        </summary>
        <div className='overflow-x-auto border-t px-4 py-3'>
          <table className='w-full text-left text-sm'>
            <caption className='sr-only'>
              {t(
                'Every disposal path with its trigger, its outcome, whether it is currently active, and the condition that decides that.'
              )}
            </caption>
            <thead>
              <tr className='text-muted-foreground'>
                <th scope='col' className='py-1 pe-4 font-medium'>
                  {t('From')}
                </th>
                <th scope='col' className='py-1 pe-4 font-medium'>
                  {t('To')}
                </th>
                <th scope='col' className='py-1 pe-4 font-medium'>
                  {t('Active now')}
                </th>
                <th scope='col' className='py-1 font-medium'>
                  {t('Condition')}
                </th>
              </tr>
            </thead>
            <tbody>
              {edges.map((edge) => {
                const from = nodeById.get(edge.from)
                const to = nodeById.get(edge.to)
                return (
                  <tr
                    key={`${edge.from}->${edge.to}`}
                    className='border-t align-top'
                  >
                    <th
                      scope='row'
                      className='py-1.5 pe-4 text-left font-normal'
                    >
                      {from?.title}
                      {from?.detail ? ` (${from.detail})` : ''}
                    </th>
                    <td className='py-1.5 pe-4'>
                      {to?.title}
                      {to?.detail ? ` (${to.detail})` : ''}
                    </td>
                    <td className='py-1.5 pe-4'>
                      {edge.active ? t('Yes') : t('No')}
                    </td>
                    <td className='text-muted-foreground py-1.5'>
                      {edge.condition}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
          <p className='text-muted-foreground mt-3 text-xs'>
            {t(
              'Counts come from the current channel table. Clicking a box in the diagram opens the page that owns those settings; the section links in the sidebar do the same thing from the keyboard.'
            )}
          </p>
        </div>
      </details>
    </div>
  )
}
