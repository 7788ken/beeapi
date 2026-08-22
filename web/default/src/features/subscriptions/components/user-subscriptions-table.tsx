import { useState, useMemo } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Loader2, X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Skeleton } from '@/components/ui/skeleton'
import { renderQuotaCompat } from '@/features/dashboard/lib'
import {
  invalidateUserSubscription,
  listAllUserSubscriptions,
  listBoundGroups,
  type AdminUserSubscriptionRow,
} from '../api'

type StatusFilter = 'active' | 'expired' | 'cancelled'

const STATUS_TABS: { value: StatusFilter; label: string; tone: string }[] = [
  { value: 'active', label: '生效中', tone: 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-400' },
  { value: 'expired', label: '已过期', tone: 'bg-amber-500/15 text-amber-700 dark:text-amber-400' },
  { value: 'cancelled', label: '已作废', tone: 'bg-rose-500/15 text-rose-700 dark:text-rose-400' },
]

const PAGE_SIZE = 20
const ALL_GROUPS = '__all__'

function formatTime(ts: number) {
  if (!ts) return '—'
  const d = new Date(ts * 1000)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}

function usagePercent(used: number, total: number) {
  if (!total || total <= 0) return 0
  return Math.min(100, (used / total) * 100)
}

export function UserSubscriptionsTable() {
  const [status, setStatus] = useState<StatusFilter>('active')
  const [page, setPage] = useState(1)
  const [usernameInput, setUsernameInput] = useState('')
  const [usernameFilter, setUsernameFilter] = useState('')
  const [boundGroup, setBoundGroup] = useState<string>(ALL_GROUPS)
  const queryClient = useQueryClient()

  const { data: groupOptions } = useQuery({
    queryKey: ['admin-subscription-bound-groups'],
    queryFn: async () => (await listBoundGroups()).data ?? [],
    staleTime: 5 * 60_000,
  })

  const queryParams = useMemo(
    () => ({
      status,
      page,
      page_size: PAGE_SIZE,
      ...(usernameFilter ? { username: usernameFilter } : {}),
      ...(boundGroup !== ALL_GROUPS ? { bound_group: boundGroup } : {}),
    }),
    [status, page, usernameFilter, boundGroup]
  )

  const { data, isLoading, isFetching } = useQuery({
    queryKey: ['admin-user-subscriptions', queryParams],
    queryFn: async () => {
      const res = await listAllUserSubscriptions(queryParams)
      return res.data
    },
    placeholderData: (prev) => prev,
    staleTime: 30_000,
  })

  const applyUsername = () => {
    const trimmed = usernameInput.trim()
    setUsernameFilter(trimmed)
    setPage(1)
  }

  const clearUsername = () => {
    setUsernameInput('')
    setUsernameFilter('')
    setPage(1)
  }

  const handleGroupChange = (value: string) => {
    setBoundGroup(value)
    setPage(1)
  }

  const items: AdminUserSubscriptionRow[] = useMemo(
    () => data?.items ?? [],
    [data]
  )
  const total = data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  const handleInvalidate = async (id: number) => {
    if (!window.confirm('确定要作废这条订阅吗？')) return
    try {
      await invalidateUserSubscription(id)
      queryClient.invalidateQueries({ queryKey: ['admin-user-subscriptions'] })
    } catch (err) {
      window.alert('作废失败：' + String(err))
    }
  }

  return (
    <div className='space-y-3'>
      <div className='flex flex-wrap items-center gap-1.5 sm:gap-2'>
        <div className='flex shrink-0 items-center gap-1.5 rounded-md border p-0.5'>
          {STATUS_TABS.map((tab) => (
            <button
              key={tab.value}
              type='button'
              onClick={() => {
                setStatus(tab.value)
                setPage(1)
              }}
              className={`rounded-[5px] px-3 py-1 text-xs font-medium transition-colors ${
                status === tab.value
                  ? 'bg-primary text-primary-foreground shadow-sm'
                  : 'text-muted-foreground hover:bg-muted hover:text-foreground'
              }`}
            >
              {tab.label}
            </button>
          ))}
        </div>

        <div className='flex shrink-0 items-center gap-1'>
          <Input
            value={usernameInput}
            onChange={(e) => setUsernameInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') applyUsername()
            }}
            placeholder='按用户名筛选'
            className='h-8 w-44 text-xs'
          />
          <Button
            size='sm'
            variant='outline'
            className='h-8 text-xs'
            onClick={applyUsername}
          >
            搜索
          </Button>
          {usernameFilter ? (
            <Button
              size='sm'
              variant='ghost'
              className='h-8 px-2 text-xs'
              onClick={clearUsername}
              title='清除用户名筛选'
            >
              <X className='size-3.5' />
            </Button>
          ) : null}
        </div>

        <Select value={boundGroup} onValueChange={handleGroupChange}>
          <SelectTrigger className='h-8 w-56 text-xs'>
            <SelectValue placeholder='全部绑定分组' />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL_GROUPS}>全部绑定分组</SelectItem>
            {(groupOptions ?? []).map((g) => (
              <SelectItem key={g} value={g}>
                {g}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        <span className='text-muted-foreground text-xs'>
          共 {total} 条
        </span>
        {isFetching ? (
          <Loader2 className='text-muted-foreground size-3.5 animate-spin' />
        ) : null}
      </div>

      <div className='overflow-hidden rounded-md border'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className='w-16'>ID</TableHead>
              <TableHead>用户</TableHead>
              <TableHead>订阅计划</TableHead>
              <TableHead>绑定分组</TableHead>
              <TableHead className='text-right'>已用 / 总额</TableHead>
              <TableHead>使用进度</TableHead>
              <TableHead>开始</TableHead>
              <TableHead>结束</TableHead>
              <TableHead className='w-24 text-right'>操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {isLoading ? (
              Array.from({ length: 5 }).map((_, i) => (
                <TableRow key={i}>
                  <TableCell colSpan={9}>
                    <Skeleton className='h-6 w-full' />
                  </TableCell>
                </TableRow>
              ))
            ) : items.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={9}
                  className='text-muted-foreground py-8 text-center text-sm'
                >
                  暂无{STATUS_TABS.find((t) => t.value === status)?.label}订阅
                </TableCell>
              </TableRow>
            ) : (
              items.map((row) => {
                const pct = usagePercent(row.amount_used, row.amount_total)
                const tone = STATUS_TABS.find((t) => t.value === status)?.tone
                return (
                  <TableRow key={row.id}>
                    <TableCell className='font-mono text-xs'>
                      {row.id}
                    </TableCell>
                    <TableCell>
                      <div className='font-medium'>
                        {row.username || '—'}
                      </div>
                      <div className='text-muted-foreground text-xs'>
                        uid={row.user_id}
                      </div>
                    </TableCell>
                    <TableCell>
                      {row.plan_title || `plan #${row.plan_id}`}
                    </TableCell>
                    <TableCell>
                      <span className='text-xs'>
                        {row.plan_bound_group || '—'}
                      </span>
                    </TableCell>
                    <TableCell className='text-right tabular-nums'>
                      <div className='text-xs'>
                        {renderQuotaCompat(row.amount_used, 2)}
                      </div>
                      <div className='text-muted-foreground text-xs'>
                        / {renderQuotaCompat(row.amount_total, 2)}
                      </div>
                    </TableCell>
                    <TableCell>
                      <div className='flex items-center gap-2'>
                        <div className='bg-muted h-1.5 w-24 overflow-hidden rounded-full'>
                          <div
                            className={`h-full rounded-full transition-all ${
                              pct >= 90
                                ? 'bg-rose-500'
                                : pct >= 60
                                  ? 'bg-amber-500'
                                  : 'bg-emerald-500'
                            }`}
                            style={{ width: `${pct}%` }}
                          />
                        </div>
                        <span className='text-xs tabular-nums'>
                          {pct.toFixed(0)}%
                        </span>
                      </div>
                    </TableCell>
                    <TableCell className='text-xs'>
                      {formatTime(row.start_time)}
                    </TableCell>
                    <TableCell className='text-xs'>
                      <span className={tone ? `${tone} rounded px-1.5 py-0.5` : ''}>
                        {formatTime(row.end_time)}
                      </span>
                    </TableCell>
                    <TableCell className='text-right'>
                      {status === 'active' ? (
                        <Button
                          size='sm'
                          variant='ghost'
                          className='text-destructive hover:bg-destructive/10 h-7 text-xs'
                          onClick={() => handleInvalidate(row.id)}
                        >
                          作废
                        </Button>
                      ) : null}
                    </TableCell>
                  </TableRow>
                )
              })
            )}
          </TableBody>
        </Table>
      </div>

      {totalPages > 1 ? (
        <div className='flex items-center justify-end gap-2 text-xs'>
          <span className='text-muted-foreground'>
            第 {page} / {totalPages} 页
          </span>
          <Button
            size='sm'
            variant='outline'
            disabled={page <= 1}
            onClick={() => setPage((p) => Math.max(1, p - 1))}
          >
            上一页
          </Button>
          <Button
            size='sm'
            variant='outline'
            disabled={page >= totalPages}
            onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
          >
            下一页
          </Button>
        </div>
      ) : null}
    </div>
  )
}
