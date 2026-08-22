import { useMemo, useState } from 'react'
import { Tag } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { useSystemOptions } from '@/features/system-settings/hooks/use-system-options'

type Props = {
  group: string
}

type RatioMap = Record<string, number>
type GroupGroupMap = Record<string, RatioMap>

function safeParse<T>(value: string | undefined, fallback: T): T {
  if (!value) return fallback
  try {
    return JSON.parse(value) as T
  } catch {
    return fallback
  }
}

export function ChannelGroupPricingDialog({ group }: Props) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant='outline' size='sm' className='h-8 gap-1.5'>
          <Tag className='h-3.5 w-3.5' />
          {t('Pricing')}
        </Button>
      </DialogTrigger>
      {open && (
        <DialogContent className='sm:max-w-[560px]'>
          <PricingContent group={group} onClose={() => setOpen(false)} />
        </DialogContent>
      )}
    </Dialog>
  )
}

function PricingContent({
  group,
  onClose,
}: {
  group: string
  onClose: () => void
}) {
  const { t } = useTranslation()
  const { data: options, isLoading } = useSystemOptions()

  const { defaultRatio, rows } = useMemo(() => {
    const list = options?.data ?? []
    const map: Record<string, string> = {}
    for (const o of list) map[o.key] = o.value
    const groupRatio = safeParse<RatioMap>(map.GroupRatio, {})
    const groupGroupRatio = safeParse<GroupGroupMap>(map.GroupGroupRatio, {})

    const def = groupRatio[group]
    const tiers = Object.keys(groupGroupRatio)
    const rowList = tiers.map((tier) => {
      const override = groupGroupRatio[tier]?.[group]
      const hasOverride = override !== undefined
      return {
        tier,
        ratio: hasOverride ? override : def,
        hasOverride,
      }
    })
    return { defaultRatio: def, rows: rowList }
  }, [options, group])

  return (
    <>
      <DialogHeader>
        <DialogTitle>{t('Pricing for "{{group}}"', { group })}</DialogTitle>
        <DialogDescription>
          {t(
            'Effective per-user-tier ratio for this channel group. Highlighted rows have a tier-specific override; otherwise the default applies.'
          )}
        </DialogDescription>
      </DialogHeader>

      <div className='space-y-3'>
        <div className='flex items-center gap-2 rounded-md border bg-muted/40 px-3 py-2 text-sm'>
          <span className='font-medium'>{t('Default ratio')}</span>
          <Badge variant='secondary'>
            {defaultRatio === undefined ? t('Not set') : String(defaultRatio)}
          </Badge>
        </div>

        <div className='rounded-md border max-h-[420px] overflow-auto'>
          {isLoading ? (
            <div className='p-6 text-center text-sm text-muted-foreground'>
              {t('Loading...')}
            </div>
          ) : rows.length === 0 ? (
            <div className='p-6 text-center text-sm text-muted-foreground'>
              {t('No user tiers configured yet.')}
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('User tier')}</TableHead>
                  <TableHead className='text-right'>{t('Ratio')}</TableHead>
                  <TableHead className='text-right w-[110px]'>
                    {t('Source')}
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((r) => (
                  <TableRow
                    key={r.tier}
                    className={r.hasOverride ? 'bg-amber-50/60' : ''}
                  >
                    <TableCell className='font-medium'>{r.tier}</TableCell>
                    <TableCell
                      className={
                        'text-right font-mono ' +
                        (r.hasOverride ? 'text-amber-900 font-semibold' : '')
                      }
                    >
                      {r.ratio === undefined ? '—' : String(r.ratio)}
                    </TableCell>
                    <TableCell className='text-right'>
                      {r.hasOverride ? (
                        <Badge variant='default' className='bg-amber-500'>
                          {t('Override')}
                        </Badge>
                      ) : (
                        <Badge variant='outline'>{t('Default')}</Badge>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </div>
      </div>

      <DialogFooter>
        <Button variant='outline' onClick={onClose}>
          {t('Close')}
        </Button>
      </DialogFooter>
    </>
  )
}
