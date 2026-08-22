import { Pencil, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import type { UserRpmRule } from './api'

type Props = {
  rules: UserRpmRule[]
  busyId?: number | null
  onEdit: (rule: UserRpmRule) => void
  onDelete: (rule: UserRpmRule) => void
}

export function UserRpmTable({ rules, busyId, onEdit, onDelete }: Props) {
  const { t } = useTranslation()

  if (rules.length === 0) {
    return (
      <div className='rounded-lg border bg-muted/30 p-4 text-center text-sm text-muted-foreground'>
        {t('No user RPM rules yet.')}
      </div>
    )
  }

  return (
    <div className='rounded-lg border overflow-hidden'>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className='w-[80px]'>{t('User ID')}</TableHead>
            <TableHead className='w-[90px]'>{t('Base RPM')}</TableHead>
            <TableHead className='w-[80px]'>{t('Jitter %')}</TableHead>
            <TableHead>{t('Delay range (ms)')}</TableHead>
            <TableHead className='w-[80px]'>{t('Enabled')}</TableHead>
            <TableHead className='w-[110px] text-right'>{t('Actions')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rules.map((rule) => (
            <TableRow key={rule.id}>
              <TableCell className='tabular-nums'>{rule.user_id}</TableCell>
              <TableCell className='tabular-nums'>{rule.base_rpm}</TableCell>
              <TableCell className='tabular-nums'>
                {Math.round(rule.jitter_pct * 100)}%
              </TableCell>
              <TableCell className='tabular-nums'>
                {rule.min_delay_ms} ~ {rule.max_delay_ms}
              </TableCell>
              <TableCell>
                {rule.enabled ? (
                  <Badge variant='default'>{t('On')}</Badge>
                ) : (
                  <Badge variant='secondary'>{t('Off')}</Badge>
                )}
              </TableCell>
              <TableCell className='text-right'>
                <div className='flex justify-end gap-1'>
                  <Button
                    type='button'
                    size='sm'
                    variant='ghost'
                    disabled={busyId === rule.id}
                    onClick={() => onEdit(rule)}
                  >
                    <Pencil className='size-4' />
                  </Button>
                  <Button
                    type='button'
                    size='sm'
                    variant='ghost'
                    disabled={busyId === rule.id}
                    onClick={() => onDelete(rule)}
                  >
                    <Trash2 className='size-4 text-rose-600' />
                  </Button>
                </div>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}
