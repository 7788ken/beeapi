import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { Pin, AlertTriangle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { quotaUnitsToDollars } from '@/lib/format'
import { getPinnedUsers } from '@/features/users/api'

const LOW_BALANCE_THRESHOLD = 1.0

function getBalanceStyle(dollars: number) {
  if (dollars <= 0) return 'text-red-600 font-semibold bg-red-50 dark:bg-red-950/30'
  if (dollars < LOW_BALANCE_THRESHOLD) return 'text-amber-600 font-semibold bg-amber-50 dark:bg-amber-950/30'
  return 'text-emerald-600'
}

export function PinnedUsersPopover() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)

  const { data, isLoading } = useQuery({
    queryKey: ['pinned-users'],
    queryFn: getPinnedUsers,
    enabled: open,
    staleTime: 30 * 1000,
  })

  const users = [...(data?.data || [])].sort((a, b) => a.quota - b.quota)
  const lowBalanceCount = users.filter(
    (u) => quotaUnitsToDollars(u.quota) < LOW_BALANCE_THRESHOLD
  ).length

  const handleUserClick = (userId: number) => {
    setOpen(false)
    navigate({ to: '/users', search: { edit: userId } })
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button variant='ghost' size='icon' className='relative h-8 w-8'>
          <Pin className='h-4 w-4 text-amber-500' />
          {lowBalanceCount > 0 && (
            <span className='absolute -right-0.5 -top-0.5 flex h-3.5 w-3.5 items-center justify-center rounded-full bg-red-500 text-[9px] font-bold text-white'>
              {lowBalanceCount}
            </span>
          )}
        </Button>
      </PopoverTrigger>
      <PopoverContent className='w-96 p-0' align='start'>
        <div className='border-b px-4 py-2.5'>
          <p className='text-sm font-medium'>{t('Pinned Users')}</p>
        </div>
        <div className='max-h-72 overflow-y-auto'>
          {isLoading && (
            <p className='p-4 text-center text-xs text-muted-foreground'>
              {t('Loading...')}
            </p>
          )}
          {!isLoading && users.length === 0 && (
            <p className='p-4 text-center text-xs text-muted-foreground'>
              {t('No pinned users. Pin users from the user edit panel.')}
            </p>
          )}
          {!isLoading && users.length > 0 && (
            <table className='w-full text-sm'>
              <thead className='sticky top-0 bg-muted/50 text-xs text-muted-foreground'>
                <tr>
                  <th className='px-4 py-1.5 text-left font-medium'>{t('User')}</th>
                  <th className='px-2 py-1.5 text-left font-medium'>{t('Group')}</th>
                  <th className='px-4 py-1.5 text-right font-medium'>{t('Balance')}</th>
                </tr>
              </thead>
              <tbody>
                {users.map((user) => {
                  const dollars = quotaUnitsToDollars(user.quota)
                  const balanceStyle = getBalanceStyle(dollars)
                  const isLow = dollars < LOW_BALANCE_THRESHOLD

                  return (
                    <tr
                      key={user.id}
                      className='cursor-pointer border-t transition-colors hover:bg-accent'
                      onClick={() => handleUserClick(user.id)}
                    >
                      <td className='px-4 py-2'>
                        <div className='flex items-center gap-1.5'>
                          {isLow && (
                            <AlertTriangle className='h-3.5 w-3.5 shrink-0 text-amber-500' />
                          )}
                          <div className='min-w-0'>
                            <p className='truncate font-medium leading-tight'>
                              {user.display_name || user.username}
                            </p>
                            {user.remark && (
                              <p className='truncate text-xs text-muted-foreground'>
                                {user.remark}
                              </p>
                            )}
                          </div>
                        </div>
                      </td>
                      <td className='px-2 py-2'>
                        <span className='inline-block rounded bg-muted px-1.5 py-0.5 text-xs'>
                          {user.group}
                        </span>
                      </td>
                      <td className='px-4 py-2 text-right'>
                        <span className={`inline-block rounded px-1.5 py-0.5 text-xs font-mono ${balanceStyle}`}>
                          ${dollars.toFixed(2)}
                        </span>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          )}
        </div>
      </PopoverContent>
    </Popover>
  )
}
