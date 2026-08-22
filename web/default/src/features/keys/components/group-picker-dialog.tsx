import { useEffect, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ChevronsUpDown, CircleCheck, Search } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import {
  OTHER_PLATFORM,
  PLATFORMS,
  classifyGroup,
  renderPlatformIcon,
} from '@/features/group-square/lib/classify'
import { useGroupUptimeSeries } from '@/features/group-square/hooks/use-group-uptime'
import { getPricing } from '@/features/pricing/api'
import { getUserGroups } from '@/lib/api'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { UptimeSparkline } from '@/components/uptime-sparkline'

// 平台配色：浅底深字（含深色模式），复用 group-square 的分类 key。
// 灰系（OpenAI/xAI/其他）特意上彩，避免整体黑白。
const PLATFORM_TONE: Record<string, { box: string; dot: string }> = {
  auto: {
    box: 'bg-teal-50 text-teal-700 dark:bg-teal-950/40 dark:text-teal-300',
    dot: 'bg-teal-500',
  },
  claude: {
    box: 'bg-orange-50 text-orange-700 dark:bg-orange-950/40 dark:text-orange-300',
    dot: 'bg-orange-500',
  },
  openai: {
    box: 'bg-green-50 text-green-700 dark:bg-green-950/40 dark:text-green-300',
    dot: 'bg-green-500',
  },
  gemini: {
    box: 'bg-blue-50 text-blue-700 dark:bg-blue-950/40 dark:text-blue-300',
    dot: 'bg-blue-500',
  },
  deepseek: {
    box: 'bg-indigo-50 text-indigo-700 dark:bg-indigo-950/40 dark:text-indigo-300',
    dot: 'bg-indigo-500',
  },
  xai: {
    box: 'bg-amber-50 text-amber-700 dark:bg-amber-950/40 dark:text-amber-300',
    dot: 'bg-amber-500',
  },
  cn: {
    box: 'bg-rose-50 text-rose-700 dark:bg-rose-950/40 dark:text-rose-300',
    dot: 'bg-rose-500',
  },
  other: {
    box: 'bg-slate-100 text-slate-600 dark:bg-slate-800/50 dark:text-slate-300',
    dot: 'bg-slate-400',
  },
}

function toneOf(platform: string) {
  return PLATFORM_TONE[platform] ?? PLATFORM_TONE.other
}

function platformLabelKey(platform: string): string {
  return PLATFORMS.find((p) => p.key === platform)?.labelKey ?? OTHER_PLATFORM.labelKey
}

function PlatformIcon({ platform }: { platform: string }) {
  const p = PLATFORMS.find((p) => p.key === platform)
  if (p) return <>{renderPlatformIcon(p)}</>
  return <span className='text-sm font-semibold'>·</span>
}

function formatRatio(ratio: number): string {
  if (Number.isNaN(ratio)) return '—'
  return `×${parseFloat(ratio.toFixed(2))}`
}

export interface GroupPickerOption {
  name: string
  desc: string
  platform: string
  /** 用户当前实际倍率（含专属折扣） */
  currentRatio: number
  /** 全局默认倍率 */
  defaultRatio: number
  /** 相对默认价的折扣百分比，0 表示无折扣 */
  discountPct: number
}

/**
 * 合并 /api/user/self/groups（当前倍率）与 /api/pricing 的 group_ratio（默认倍率），
 * 套用 group-square 的 classify 做平台分类。仅在 enabled 时请求。
 */
export function useGroupPickerOptions(enabled: boolean): GroupPickerOption[] {
  const { data: groupsRes } = useQuery({
    queryKey: ['user-groups'],
    queryFn: getUserGroups,
    staleTime: 5 * 60 * 1000,
    enabled,
  })
  const { data: pricingRes } = useQuery({
    queryKey: ['pricing'],
    queryFn: getPricing,
    staleTime: 5 * 60 * 1000,
    enabled,
  })

  return useMemo(() => {
    const raw = groupsRes?.data ?? {}
    // 默认价用全局原价 base_group_ratio；注意 pricing.group_ratio 对登录用户
    // 已被后端覆盖成「用户视角价」(= self/groups)，不能拿来当默认价。
    const baseRatio = pricingRes?.base_group_ratio ?? {}
    return Object.entries(raw).map(([name, info]) => {
      const desc = info?.desc ?? ''
      const currentRatio = Number(info?.ratio ?? 0)
      const dft =
        typeof baseRatio[name] === 'number' ? baseRatio[name] : currentRatio
      const discountPct =
        dft > currentRatio && dft > 0
          ? Math.round((1 - currentRatio / dft) * 100)
          : 0
      return {
        name,
        desc,
        platform: classifyGroup(name, desc),
        currentRatio,
        defaultRatio: dft,
        discountPct,
      }
    })
  }, [groupsRes, pricingRes])
}

function FilterChip({
  active,
  onClick,
  label,
  dotClass,
}: {
  active: boolean
  onClick: () => void
  label: string
  dotClass?: string
}) {
  return (
    <button
      type='button'
      onClick={onClick}
      className={cn(
        'inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1 text-xs transition-colors',
        active
          ? 'border-primary bg-primary/10 text-primary font-medium'
          : 'text-muted-foreground hover:bg-muted/50'
      )}
    >
      {dotClass && <span className={cn('size-2 rounded-full', dotClass)} />}
      {label}
    </button>
  )
}

type GroupPickerDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** 预选中的分组名 */
  value?: string
  onConfirm: (group: string) => void
  title?: string
  description?: string
  confirmText?: string
  /** 确认请求进行中 */
  confirming?: boolean
  /** 标记为「当前」的分组名（列表快速切换场景） */
  currentGroup?: string
}

export function GroupPickerDialog({
  open,
  onOpenChange,
  value,
  onConfirm,
  title,
  description,
  confirmText,
  confirming,
  currentGroup,
}: GroupPickerDialogProps) {
  const { t } = useTranslation()
  const options = useGroupPickerOptions(open)
  // 与分组广场同一数据源：近 24h 可用率，选分组时能直接看到哪些分组正在掉链子
  const uptimeSeries = useGroupUptimeSeries(open)
  const [search, setSearch] = useState('')
  const [platform, setPlatform] = useState('all')
  const [selected, setSelected] = useState<string | undefined>(value)

  useEffect(() => {
    if (open) {
      setSelected(value)
      setSearch('')
      setPlatform('all')
    }
  }, [open, value])

  // 只显示存在分组的平台 chips，顺序沿用 PLATFORMS，other 兜底放最后
  const presentPlatforms = useMemo(() => {
    const set = new Set(options.map((o) => o.platform))
    const ordered = PLATFORMS.filter((p) => set.has(p.key)).map((p) => p.key)
    if (set.has('other')) ordered.push('other')
    return ordered
  }, [options])

  const visible = useMemo(() => {
    const kw = search.trim().toLowerCase()
    return [...options]
      .sort((a, b) =>
        a.currentRatio !== b.currentRatio
          ? a.currentRatio - b.currentRatio
          : a.name.localeCompare(b.name)
      )
      .filter((o) => platform === 'all' || o.platform === platform)
      .filter(
        (o) =>
          !kw ||
          o.name.toLowerCase().includes(kw) ||
          o.desc.toLowerCase().includes(kw)
      )
  }, [options, platform, search])

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-2xl'>
        <DialogHeader className='shrink-0 space-y-1 border-b px-4 py-4 text-start sm:px-5'>
          <DialogTitle>{title ?? t('Select a group')}</DialogTitle>
          <DialogDescription className='text-xs sm:text-sm'>
            {description ??
              t('Filter by type, compare default and current price')}
          </DialogDescription>
        </DialogHeader>

        <div className='shrink-0 space-y-3 px-4 pt-4 sm:px-5'>
          <div className='relative'>
            <Search className='text-muted-foreground pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2' />
            <Input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder={t('Search groups by name or description')}
              className='pl-9'
            />
          </div>

          <div className='flex flex-wrap gap-1.5'>
            <FilterChip
              active={platform === 'all'}
              onClick={() => setPlatform('all')}
              label={t('All')}
            />
            {presentPlatforms.map((key) => (
              <FilterChip
                key={key}
                active={platform === key}
                onClick={() => setPlatform(key)}
                label={t(platformLabelKey(key))}
                dotClass={toneOf(key).dot}
              />
            ))}
          </div>

          <div className='text-muted-foreground px-0.5 text-xs'>
            {t('{{count}} group(s)', { count: visible.length })}
          </div>
        </div>

        <div className='min-h-0 flex-1 overflow-y-auto overscroll-contain px-4 sm:px-5'>
          <div className='space-y-2 py-3'>
            {visible.length === 0 ? (
              <div className='text-muted-foreground py-12 text-center text-sm'>
                {t('No group found.')}
              </div>
            ) : (
              visible.map((o) => {
                const tone = toneOf(o.platform)
                const isSel = selected === o.name
                const isCurrent = currentGroup === o.name
                const uptime = uptimeSeries[o.name]
                return (
                  <button
                    type='button'
                    key={o.name}
                    onClick={() => setSelected(o.name)}
                    className={cn(
                      'flex w-full items-start gap-3 rounded-xl border px-3 py-3 text-start transition-colors sm:items-center',
                      isSel
                        ? 'border-primary ring-primary/25 bg-primary/5 ring-1'
                        : 'hover:border-foreground/20 hover:bg-muted/40'
                    )}
                  >
                    <div
                      className={cn(
                        'flex size-9 shrink-0 items-center justify-center rounded-lg',
                        tone.box
                      )}
                    >
                      <PlatformIcon platform={o.platform} />
                    </div>

                    {/* 移动端：三行（名称 / 类型+价格 / 介绍） */}
                    <div className='flex min-w-0 flex-1 flex-col gap-1.5 sm:hidden'>
                      <div className='flex items-center gap-2'>
                        <span className='min-w-0 flex-1 truncate font-medium'>
                          {o.name || t('User Group')}
                        </span>
                        <CircleCheck
                          className={cn(
                            'size-5 shrink-0',
                            isSel ? 'text-primary' : 'text-muted-foreground/25'
                          )}
                        />
                      </div>
                      <div className='flex flex-wrap items-center gap-x-2 gap-y-1'>
                        <span
                          className={cn(
                            'rounded-full px-2 py-0.5 text-[11px]',
                            tone.box
                          )}
                        >
                          {t(platformLabelKey(o.platform))}
                        </span>
                        {isCurrent && (
                          <Badge
                            variant='outline'
                            className='text-muted-foreground text-[10px]'
                          >
                            {t('Current')}
                          </Badge>
                        )}
                        <span className='ms-auto inline-flex items-center gap-1.5 whitespace-nowrap'>
                          {o.discountPct > 0 && (
                            <span className='text-muted-foreground text-[11px] tabular-nums'>
                              {t('Default price')}{' '}
                              <span className='line-through'>
                                {formatRatio(o.defaultRatio)}
                              </span>
                            </span>
                          )}
                          <span className='text-muted-foreground text-[11px]'>
                            {t('Current price')}
                          </span>
                          <span className='text-foreground text-base font-medium tabular-nums'>
                            {formatRatio(o.currentRatio)}
                          </span>
                          {o.discountPct > 0 && (
                            <span className='rounded-full bg-green-100 px-1.5 py-0.5 text-[10px] leading-none font-medium text-green-700 dark:bg-green-950/50 dark:text-green-300'>
                              {t('Save {{pct}}%', { pct: o.discountPct })}
                            </span>
                          )}
                        </span>
                      </div>
                      {o.desc && (
                        <p className='text-muted-foreground text-xs leading-relaxed break-words'>
                          {o.desc}
                        </p>
                      )}
                      <div className='flex items-center gap-1.5'>
                        <span className='text-muted-foreground text-[11px]'>
                          {t('Availability (last 24h)')}
                        </span>
                        <UptimeSparkline
                          series={uptime ?? []}
                          size='sm'
                          emptyLabel={t('No data')}
                        />
                      </div>
                    </div>

                    {/* PC：横向（名字+类型 / 介绍 | 价格列 | 勾选） */}
                    <div className='hidden min-w-0 flex-1 sm:flex sm:items-center sm:gap-3'>
                      <div className='min-w-0 flex-1'>
                        <div className='flex items-center gap-2'>
                          <span className='min-w-0 truncate font-medium'>
                            {o.name || t('User Group')}
                          </span>
                          <span
                            className={cn(
                              'shrink-0 rounded-full px-2 py-0.5 text-[11px]',
                              tone.box
                            )}
                          >
                            {t(platformLabelKey(o.platform))}
                          </span>
                          {isCurrent && (
                            <Badge
                              variant='outline'
                              className='text-muted-foreground shrink-0 text-[10px]'
                            >
                              {t('Current')}
                            </Badge>
                          )}
                        </div>
                        {o.desc && (
                          <p className='text-muted-foreground mt-1 text-xs leading-relaxed break-words'>
                            {o.desc}
                          </p>
                        )}
                        <div className='mt-1.5 flex items-center gap-1.5'>
                          <span className='text-muted-foreground text-[11px]'>
                            {t('Availability (last 24h)')}
                          </span>
                          <UptimeSparkline
                            series={uptime ?? []}
                            size='sm'
                            emptyLabel={t('No data')}
                          />
                        </div>
                      </div>
                      <div className='flex shrink-0 flex-col items-end gap-0.5'>
                        {o.discountPct > 0 && (
                          <span className='text-muted-foreground text-[11px] tabular-nums'>
                            {t('Default price')}{' '}
                            <span className='line-through'>
                              {formatRatio(o.defaultRatio)}
                            </span>
                          </span>
                        )}
                        <span className='inline-flex items-center gap-1.5'>
                          <span className='text-muted-foreground text-[11px]'>
                            {t('Current price')}
                          </span>
                          <span className='text-foreground text-base font-medium tabular-nums'>
                            {formatRatio(o.currentRatio)}
                          </span>
                          {o.discountPct > 0 && (
                            <span className='rounded-full bg-green-100 px-1.5 py-0.5 text-[10px] leading-none font-medium text-green-700 dark:bg-green-950/50 dark:text-green-300'>
                              {t('Save {{pct}}%', { pct: o.discountPct })}
                            </span>
                          )}
                        </span>
                      </div>
                      <CircleCheck
                        className={cn(
                          'size-5 shrink-0',
                          isSel ? 'text-primary' : 'text-muted-foreground/25'
                        )}
                      />
                    </div>
                  </button>
                )
              })
            )}
          </div>
        </div>

        <DialogFooter className='shrink-0 flex-row items-center justify-between gap-2 border-t px-4 py-3 sm:px-5 sm:py-4'>
          <span className='text-muted-foreground min-w-0 truncate text-sm'>
            {selected !== undefined ? (
              <>
                {t('Selected')}:{' '}
                <span className='text-foreground font-medium'>
                  {selected || t('User Group')}
                </span>
              </>
            ) : (
              t('No group selected')
            )}
          </span>
          <div className='flex shrink-0 gap-2'>
            <Button
              variant='outline'
              onClick={() => onOpenChange(false)}
              disabled={confirming}
            >
              {t('Cancel')}
            </Button>
            <Button
              onClick={() => onConfirm(selected ?? '')}
              disabled={selected === undefined || confirming}
            >
              {confirming ? t('Saving...') : (confirmText ?? t('Confirm'))}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

type GroupPickerFieldProps = {
  value?: string
  onChange: (group: string) => void
  placeholder?: string
  disabled?: boolean
}

/** 表单内的分组选择器：触发按钮 + 内置 GroupPickerDialog，替代旧的下拉 combobox。 */
export function GroupPickerField({
  value,
  onChange,
  placeholder,
  disabled,
}: GroupPickerFieldProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const options = useGroupPickerOptions(true)
  const selectedOpt = options.find((o) => o.name === value)
  const isAuto = value === 'auto'
  const tone = isAuto
    ? toneOf('auto')
    : selectedOpt
      ? toneOf(selectedOpt.platform)
      : null
  const displayLabel = isAuto
    ? t('Auto')
    : value
      ? value
      : (placeholder ?? t('Select a group'))

  return (
    <>
      <Button
        type='button'
        variant='outline'
        role='combobox'
        disabled={disabled}
        onClick={() => setOpen(true)}
        className='border-input bg-muted/40 hover:bg-muted/55 flex h-auto min-h-14 w-full min-w-0 justify-between gap-2 rounded-lg px-3 py-2 text-start font-normal shadow-none sm:min-h-16 sm:px-4'
      >
        <span className='flex min-w-0 flex-1 items-center gap-2.5'>
          {tone && (
            <span
              className={cn(
                'flex size-8 shrink-0 items-center justify-center rounded-lg',
                tone.box
              )}
            >
              {isAuto ? (
                <PlatformIcon platform='auto' />
              ) : (
                <PlatformIcon platform={selectedOpt!.platform} />
              )}
            </span>
          )}
          <span className='min-w-0'>
            <span className='block truncate font-medium'>{displayLabel}</span>
            {selectedOpt?.desc && (
              <span className='text-muted-foreground block truncate text-xs'>
                {selectedOpt.desc}
              </span>
            )}
          </span>
        </span>
        <span className='flex shrink-0 items-center gap-2'>
          {selectedOpt && (
            <Badge variant='outline' className='tabular-nums'>
              {formatRatio(selectedOpt.currentRatio)}
            </Badge>
          )}
          <ChevronsUpDown className='size-4 opacity-50' />
        </span>
      </Button>
      <GroupPickerDialog
        open={open}
        onOpenChange={setOpen}
        value={value}
        currentGroup={value}
        onConfirm={(g) => {
          onChange(g)
          setOpen(false)
        }}
        title={t('Select a group')}
        confirmText={t('Confirm')}
      />
    </>
  )
}
