import { useEffect, useState } from 'react'
import { Filter, RotateCcw, Calendar, Search } from 'lucide-react'
import { getRollingDateRange, type TimeGranularity } from '@/lib/time'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ScrollArea } from '@/components/ui/scroll-area'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { DateTimePicker } from '@/components/datetime-picker'
import {
  TIME_GRANULARITY_OPTIONS,
  TIME_RANGE_PRESETS,
} from '@/features/dashboard/constants'
import type {
  DashboardChartPreferences,
  GroupBillingSourceFilter,
  GroupDashboardFilters,
} from '@/features/dashboard/types'

interface GroupsFilterProps {
  preferences: DashboardChartPreferences
  filters: GroupDashboardFilters
  onFilterChange: (filters: GroupDashboardFilters) => void
  onReset: () => void
}

const SectionDivider = ({ label }: { label: string }) => (
  <div className='relative'>
    <div className='absolute inset-0 flex items-center'>
      <span className='w-full border-t' />
    </div>
    <div className='relative flex justify-center text-xs uppercase'>
      <span className='bg-background text-muted-foreground px-2'>{label}</span>
    </div>
  </div>
)

const TIME_GRANULARITY_LABELS: Record<string, string> = {
  hour: '按小时',
  day: '按天',
  week: '按周',
  month: '按月',
}

const TIME_RANGE_LABELS: Record<string, string> = {
  '今天': '今天',
  'Today': '今天',
  '昨天': '昨天',
  'Yesterday': '昨天',
  '7 Days': '7 天',
  '1 Month': '1 个月',
  '近 1 小时': '近 1 小时',
  'Last 1 hour': '近 1 小时',
  '近 24 小时': '近 24 小时',
  'Last 24 hours': '近 24 小时',
  '近 7 天': '近 7 天',
  'Last 7 days': '近 7 天',
  '近 14 天': '近 14 天',
  'Last 14 days': '近 14 天',
  '近 30 天': '近 30 天',
  'Last 30 days': '近 30 天',
  '近 90 天': '近 90 天',
  'Last 90 days': '近 90 天',
}

const localizeRangeLabel = (label: string) => TIME_RANGE_LABELS[label] ?? label
const localizeGranularityLabel = (label: string, value: string) =>
  TIME_GRANULARITY_LABELS[value] ?? label

const BILLING_SOURCE_ALL = 'all'

const BILLING_SOURCE_OPTIONS: {
  value: typeof BILLING_SOURCE_ALL | Exclude<GroupBillingSourceFilter, ''>
  label: string
}[] = [
  { value: BILLING_SOURCE_ALL, label: '全部计费源' },
  { value: 'subscription', label: '订阅抵扣' },
  { value: 'wallet', label: '余额扣费' },
]

const TOP_GROUP_OPTIONS = [5, 10, 20, 50]

export function buildDefaultGroupFilters(
  preferences: DashboardChartPreferences
): GroupDashboardFilters {
  const { start, end } = getRollingDateRange(preferences.defaultTimeRangeDays)
  return {
    start_timestamp: start,
    end_timestamp: end,
    time_granularity: preferences.defaultTimeGranularity,
    username: '',
    billing_source: '',
    top_groups: 10,
  }
}

export function GroupsFilter(props: GroupsFilterProps) {
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState<GroupDashboardFilters>(props.filters)
  const [selectedRange, setSelectedRange] = useState<number | null>(
    () => props.preferences.defaultTimeRangeDays
  )

  useEffect(() => {
    setDraft(props.filters)
  }, [props.filters, open])

  const handleApply = () => {
    props.onFilterChange({
      ...draft,
      username: draft.username?.trim() || '',
    })
    setOpen(false)
  }

  const handleReset = () => {
    const fresh = buildDefaultGroupFilters(props.preferences)
    setDraft(fresh)
    setSelectedRange(props.preferences.defaultTimeRangeDays)
    props.onReset()
    setOpen(false)
  }

  const handleChange = <K extends keyof GroupDashboardFilters>(
    field: K,
    value: GroupDashboardFilters[K]
  ) => {
    setDraft((prev) => ({ ...prev, [field]: value }))
    if (field === 'start_timestamp' || field === 'end_timestamp') {
      setSelectedRange(null)
    }
  }

  const handleQuickRange = (days: number) => {
    const { start, end } = getRollingDateRange(days)
    setDraft((prev) => ({
      ...prev,
      start_timestamp: start,
      end_timestamp: end,
    }))
    setSelectedRange(days)
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant='outline' size='sm'>
          <Filter className='mr-2 h-4 w-4' />
          筛选
        </Button>
      </DialogTrigger>
      <DialogContent className='flex max-h-[calc(100dvh-2rem)] flex-col max-sm:h-dvh max-sm:w-screen max-sm:max-w-none max-sm:rounded-none max-sm:p-4 sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>分组统计筛选</DialogTitle>
          <DialogDescription>
            按时间、用户、计费源筛选分组统计数据。
          </DialogDescription>
        </DialogHeader>

        <ScrollArea className='flex-1 pr-3 sm:pr-4'>
          <div className='grid gap-3 py-3 sm:gap-4 sm:py-4'>
            <div className='grid gap-2'>
              <Label className='flex items-center gap-2'>
                <Calendar className='h-4 w-4' />
                快捷时间
              </Label>
              <div className='grid grid-cols-2 gap-2 sm:flex'>
                {TIME_RANGE_PRESETS.map((range) => (
                  <Button
                    key={range.days}
                    type='button'
                    size='sm'
                    variant={
                      selectedRange === range.days ? 'default' : 'outline'
                    }
                    onClick={() => handleQuickRange(range.days)}
                    className={cn(
                      'flex-1',
                      selectedRange === range.days &&
                        'ring-ring ring-2 ring-offset-2'
                    )}
                  >
                    {localizeRangeLabel(range.label)}
                  </Button>
                ))}
              </div>
            </div>

            <SectionDivider label='自定义时间' />

            <div className='grid gap-3 sm:gap-4'>
              <div className='grid gap-2'>
                <Label htmlFor='group_start_timestamp'>开始时间</Label>
                <DateTimePicker
                  value={draft.start_timestamp}
                  onChange={(date) =>
                    handleChange('start_timestamp', date || undefined)
                  }
                  placeholder='选择开始时间'
                />
              </div>

              <div className='grid gap-2'>
                <Label htmlFor='group_end_timestamp'>结束时间</Label>
                <DateTimePicker
                  value={draft.end_timestamp}
                  onChange={(date) =>
                    handleChange('end_timestamp', date || undefined)
                  }
                  placeholder='选择结束时间'
                />
              </div>
            </div>

            <SectionDivider label='图表设置' />

            <div className='grid gap-2'>
              <Label htmlFor='group_time_granularity'>时间粒度</Label>
              <Select
                value={draft.time_granularity}
                onValueChange={(value) =>
                  handleChange('time_granularity', value as TimeGranularity)
                }
              >
                <SelectTrigger>
                  <SelectValue placeholder='选择时间粒度' />
                </SelectTrigger>
                <SelectContent>
                  {TIME_GRANULARITY_OPTIONS.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {localizeGranularityLabel(option.label, option.value)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            <div className='grid gap-2'>
              <Label htmlFor='group_top'>展示前 N 个分组</Label>
              <Select
                value={String(draft.top_groups ?? 10)}
                onValueChange={(value) =>
                  handleChange('top_groups', Number(value))
                }
              >
                <SelectTrigger>
                  <SelectValue placeholder='选择数量' />
                </SelectTrigger>
                <SelectContent>
                  {TOP_GROUP_OPTIONS.map((n) => (
                    <SelectItem key={n} value={String(n)}>
                      Top {n}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            <SectionDivider label='计费维度' />

            <div className='grid gap-2'>
              <Label htmlFor='group_billing_source'>计费源</Label>
              <Select
                value={
                  draft.billing_source
                    ? draft.billing_source
                    : BILLING_SOURCE_ALL
                }
                onValueChange={(value) =>
                  handleChange(
                    'billing_source',
                    value === BILLING_SOURCE_ALL
                      ? ''
                      : (value as GroupBillingSourceFilter)
                  )
                }
              >
                <SelectTrigger>
                  <SelectValue placeholder='全部计费源' />
                </SelectTrigger>
                <SelectContent>
                  {BILLING_SOURCE_OPTIONS.map((opt) => (
                    <SelectItem key={opt.value} value={opt.value}>
                      {opt.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            <SectionDivider label='用户筛选（可选）' />

            <div className='grid gap-2'>
              <Label htmlFor='group_username'>用户名</Label>
              <Input
                id='group_username'
                placeholder='留空表示全部用户'
                value={draft.username ?? ''}
                onChange={(e) => handleChange('username', e.target.value)}
              />
            </div>
          </div>
        </ScrollArea>

        <DialogFooter className='grid grid-cols-2 gap-2 sm:flex'>
          <Button onClick={handleReset} variant='outline' type='button'>
            <RotateCcw className='mr-2 h-4 w-4' />
            重置
          </Button>
          <Button onClick={handleApply} type='submit'>
            <Search className='mr-2 h-4 w-4' />
            应用筛选
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
