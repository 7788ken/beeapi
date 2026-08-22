import { ArrowRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from '@tanstack/react-router'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import {
  UptimeSparkline,
  type UptimeDayPoint,
} from '@/components/uptime-sparkline'
import type { GroupRecord } from '../types'

interface GroupCardProps {
  group: GroupRecord
  toneClass?: string
  uptime?: UptimeDayPoint[]
}

function formatRatio(ratio: number) {
  if (Number.isNaN(ratio)) return '—'
  if (ratio === Math.floor(ratio)) return `×${ratio}`
  return `×${ratio.toFixed(2)}`
}

export function GroupCard({ group, toneClass, uptime }: GroupCardProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()

  const handlePick = () => {
    void navigate({
      to: '/keys',
      search: { group: group.name } as never,
    })
  }

  return (
    <Card className='flex h-full flex-col rounded-2xl border shadow-sm'>
      <CardHeader className='pb-2'>
        <div className='flex items-start justify-between gap-2'>
          <CardTitle
            className='min-w-0 flex-1 truncate text-base'
            title={group.name}
          >
            {group.name}
          </CardTitle>
          <Badge variant='secondary' className='shrink-0 rounded-full'>
            {t('Ratio')} {formatRatio(group.ratio)}
          </Badge>
        </div>
      </CardHeader>
      <CardContent className='flex flex-1 flex-col gap-3 pb-4'>
        <CardDescription className='line-clamp-4 flex-1 text-sm'>
          {group.desc || t('No description')}
        </CardDescription>
        <div className='flex items-end justify-between gap-2'>
          <div className='min-w-0'>
            <div className='text-muted-foreground mb-1 text-[11px]'>
              {t('Availability (last 24h)')}
            </div>
            <UptimeSparkline
              series={uptime ?? []}
              size='sm'
              emptyLabel={t('No data')}
            />
          </div>
          <TooltipProvider delayDuration={200}>
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant='secondary'
                  size='icon'
                  onClick={handlePick}
                  className={cn('h-9 w-9 rounded-xl', toneClass)}
                  aria-label={t('Create API key for this group')}
                >
                  <ArrowRight className='size-4' />
                </Button>
              </TooltipTrigger>
              <TooltipContent>
                {t('Create API key for this group')}
              </TooltipContent>
            </Tooltip>
          </TooltipProvider>
        </div>
      </CardContent>
    </Card>
  )
}
