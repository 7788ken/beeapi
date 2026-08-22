import { useEffect, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { getStats, setOption } from '../api'
import { SENSITIVE_OPTION_KEYS as K } from '../types'

function clampSampleRate(s: string): number {
  const n = Number.parseInt(s, 10)
  if (Number.isNaN(n)) return 0
  return Math.max(0, Math.min(100, n))
}

const STATS_KEY = ['sensitive', 'stats'] as const

export function ConfigBar() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  // Single source of truth: /api/sensitive_block/stats returns BOTH the live
  // counters and the current config snapshot (async_enabled / sample_rate /
  // dump_to_file / retention_days / disk_guard_pct), so we don't need a
  // separate /api/option/ fetch.
  const statsQuery = useQuery({
    queryKey: STATS_KEY,
    queryFn: async () => (await getStats()).data,
    refetchInterval: 15_000,
  })

  const enabled = statsQuery.data?.async_enabled ?? false
  const dumpToFile = statsQuery.data?.dump_to_file ?? false
  const sampleRate = String(statsQuery.data?.sample_rate ?? 20)

  const [pendingRate, setPendingRate] = useState<string>(sampleRate)
  useEffect(() => setPendingRate(sampleRate), [sampleRate])

  const setOptionMutation = useMutation({
    mutationFn: ({
      key,
      value,
    }: {
      key: string
      value: string | boolean | number
    }) => setOption(key, value),
    onSuccess: (res) => {
      // Backend returns 200 with { success: false, message } for domain errors
      // (unknown key, validation). Surface the message instead of swallowing.
      if (res && (res as { success?: boolean }).success === false) {
        toast.error(
          (res as { message?: string }).message ?? t('Failed to save')
        )
        return
      }
      queryClient.invalidateQueries({ queryKey: STATS_KEY })
    },
    onError: () => {
      toast.error(t('Failed to save'))
    },
  })

  return (
    <div className='space-y-3 rounded-lg border p-4'>
      <div className='flex flex-wrap items-center gap-4'>
        <div className='flex items-center gap-2'>
          <Switch
            id='sensitive-enabled'
            checked={enabled}
            disabled={setOptionMutation.isPending || statsQuery.isLoading}
            onCheckedChange={(v) => {
              setOptionMutation.mutate(
                { key: K.ASYNC_ENABLED, value: v },
                {
                  onSuccess: (res) => {
                    if (
                      res &&
                      (res as { success?: boolean }).success !== false
                    ) {
                      toast.success(
                        v
                          ? t('Sensitive monitor enabled')
                          : t('Sensitive monitor disabled')
                      )
                    }
                  },
                }
              )
            }}
          />
          <Label htmlFor='sensitive-enabled'>{t('Enable monitoring')}</Label>
        </div>

        <div className='flex items-center gap-2'>
          <Switch
            id='sensitive-dump-body'
            checked={dumpToFile}
            disabled={setOptionMutation.isPending || statsQuery.isLoading}
            onCheckedChange={(v) =>
              setOptionMutation.mutate({ key: K.DUMP_TO_FILE, value: v })
            }
          />
          <Label htmlFor='sensitive-dump-body'>{t('Persist body')}</Label>
        </div>

        <div className='flex items-center gap-2'>
          <Label htmlFor='sensitive-sample' className='text-sm'>
            {t('Sample rate')}
          </Label>
          <div className='relative'>
            <Input
              id='sensitive-sample'
              type='number'
              min={0}
              max={100}
              step={1}
              value={pendingRate}
              onChange={(e) => setPendingRate(e.target.value)}
              className='h-8 w-24 pe-7 tabular-nums'
              placeholder='20'
            />
            <span className='text-muted-foreground absolute top-1/2 right-2 -translate-y-1/2 text-xs'>
              %
            </span>
          </div>
          <Button
            variant='outline'
            size='sm'
            onClick={() => {
              const r = clampSampleRate(pendingRate)
              setPendingRate(String(r))
              setOptionMutation.mutate(
                { key: K.SAMPLE_RATE, value: r },
                {
                  onSuccess: () => toast.success(t('Sample rate saved')),
                }
              )
            }}
            disabled={pendingRate === sampleRate}
          >
            {t('Save')}
          </Button>
        </div>

        <div className='ms-auto flex items-center gap-2'>
          <Badge variant='outline' className='font-normal'>
            {t('Queue')}{' '}
            <span className='ms-1 tabular-nums'>
              {statsQuery.data?.queue_depth ?? '-'} /{' '}
              {statsQuery.data?.queue_cap ?? '-'}
            </span>
          </Badge>
          <Badge variant='outline' className='font-normal'>
            {t('Processed')}{' '}
            <span className='ms-1 tabular-nums'>
              {statsQuery.data?.processed ?? '-'}
            </span>
          </Badge>
          <Badge variant='outline' className='font-normal'>
            {t('Dropped')}{' '}
            <span className='ms-1 tabular-nums'>
              {statsQuery.data?.dropped ?? '-'}
            </span>
          </Badge>
          <Button
            variant='ghost'
            size='icon'
            className='h-8 w-8'
            onClick={() => {
              queryClient.invalidateQueries({ queryKey: STATS_KEY })
              void statsQuery.refetch()
            }}
            aria-label={t('Refresh stats')}
          >
            <RefreshCw
              className={cn(
                'size-3.5',
                statsQuery.isFetching && 'animate-spin'
              )}
            />
          </Button>
        </div>
      </div>

      {!enabled && !statsQuery.isLoading && (
        <Alert>
          <AlertTitle>{t('Sensitive monitor disabled')}</AlertTitle>
          <AlertDescription>
            {t(
              'No requests will be sampled until you turn on Enable monitoring above.'
            )}
          </AlertDescription>
        </Alert>
      )}
    </div>
  )
}
