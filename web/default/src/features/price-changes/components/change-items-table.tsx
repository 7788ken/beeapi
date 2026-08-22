import type { TFunction } from 'i18next'
import { Minus, Plus, TrendingDown, TrendingUp } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { GroupBadge } from '@/components/group-badge'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { formatRatioValue, formatUsd, parseGroupGroupRatioName, resolveDisplayEntry } from '../lib'
import type { PriceChangeItem } from '../types'
import { getPriceTypeLabel } from './price-change-badge'

function DirectionMark({ direction }: { direction: string }) {
  const { t } = useTranslation()
  switch (direction) {
    case 'up':
      return (
        <span className='inline-flex items-center gap-1 text-red-600 dark:text-red-400'>
          <TrendingUp className='size-3.5' />
          {t('Up')}
        </span>
      )
    case 'down':
      return (
        <span className='inline-flex items-center gap-1 text-emerald-600 dark:text-emerald-400'>
          <TrendingDown className='size-3.5' />
          {t('Down')}
        </span>
      )
    case 'added':
      return (
        <span className='inline-flex items-center gap-1 text-sky-600 dark:text-sky-400'>
          <Plus className='size-3.5' />
          {t('Added')}
        </span>
      )
    case 'removed':
      return (
        <span className='text-muted-foreground inline-flex items-center gap-1'>
          <Minus className='size-3.5' />
          {t('Removed')}
        </span>
      )
    default:
      return <span className='text-muted-foreground'>{direction}</span>
  }
}

function formatChangeValue(
  t: TFunction,
  item: PriceChangeItem,
  preferredGroup?: string
): { text: string; groupHint?: string } {
  const resolved =
    item.scope === 'model' ? resolveDisplayEntry(item, preferredGroup) : null

  const fmt = resolved
    ? (v: number) => formatUsd(v)
    : (v: number) => formatRatioValue(v)
  const oldRaw = resolved ? resolved.entry.old_usd : item.old_value
  const newRaw = resolved ? resolved.entry.new_usd : item.new_value
  const unit = resolved
    ? resolved.entry.unit === 'per_call'
      ? ` / ${t('request')}`
      : ' / 1M'
    : ''

  let text: string
  if (item.direction === 'added') {
    text = `${fmt(newRaw)}${unit}`
  } else if (item.direction === 'removed') {
    text = `${fmt(oldRaw)}${unit}`
  } else {
    text = `${fmt(oldRaw)} → ${fmt(newRaw)}${unit}`
  }

  return {
    text,
    groupHint:
      resolved && Object.keys(item.display ?? {}).length > 1
        ? resolved.group
        : undefined,
  }
}

/** group_group_ratio rows carry a composite "userGroup->targetGroup" name */
function GroupObjectCell({ groupName }: { groupName: string }) {
  const parts = parseGroupGroupRatioName(groupName)
  if (!parts) return <GroupBadge group={groupName} size='sm' />
  return (
    <span className='inline-flex items-center gap-1'>
      <GroupBadge group={parts.userGroup} size='sm' />
      <span className='text-muted-foreground text-xs'>→</span>
      <GroupBadge group={parts.targetGroup} size='sm' />
    </span>
  )
}

interface ChangeItemsTableProps {
  items: PriceChangeItem[]
  /** Group whose effective USD price should be displayed for model rows */
  preferredGroup?: string
  className?: string
}

/**
 * Detail table of one batch (or pending preview): group-level rows pinned to
 * the top, model-level rows with direction arrows and USD old -> new.
 */
export function ChangeItemsTable({
  items,
  preferredGroup,
  className,
}: ChangeItemsTableProps) {
  const { t } = useTranslation()

  const sorted = [...items].sort((a, b) => {
    if (a.scope !== b.scope) return a.scope === 'group' ? -1 : 1
    const nameA = a.scope === 'group' ? a.group_name : a.model_name
    const nameB = b.scope === 'group' ? b.group_name : b.model_name
    return nameA.localeCompare(nameB)
  })

  if (sorted.length === 0) {
    return (
      <p className='text-muted-foreground py-4 text-center text-sm'>
        {t('No change details')}
      </p>
    )
  }

  return (
    <div className={cn('overflow-hidden rounded-lg border', className)}>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('Object')}</TableHead>
            <TableHead>{t('Item')}</TableHead>
            <TableHead>{t('Direction')}</TableHead>
            <TableHead>{t('Change')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {sorted.map((item, idx) => {
            const change = formatChangeValue(t, item, preferredGroup)
            return (
              <TableRow key={idx}>
                <TableCell>
                  {item.scope === 'group' ? (
                    <GroupObjectCell groupName={item.group_name} />
                  ) : (
                    <span className='font-mono text-xs font-medium break-all'>
                      {item.model_name}
                    </span>
                  )}
                </TableCell>
                <TableCell className='text-xs whitespace-nowrap'>
                  {getPriceTypeLabel(t, item.price_type)}
                </TableCell>
                <TableCell className='text-xs whitespace-nowrap'>
                  <DirectionMark direction={item.direction} />
                </TableCell>
                <TableCell className='font-mono text-xs whitespace-nowrap'>
                  {change.text}
                  {change.groupHint && (
                    <span className='text-muted-foreground ml-1 font-sans text-[10px]'>
                      ({change.groupHint})
                    </span>
                  )}
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
    </div>
  )
}
