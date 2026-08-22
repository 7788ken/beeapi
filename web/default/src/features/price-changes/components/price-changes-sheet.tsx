import { useTranslation } from 'react-i18next'
import { ScrollArea } from '@/components/ui/scroll-area'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import type { PriceChangeBatch } from '../types'
import { PriceChangeBatchList } from './batch-list'

interface PriceChangesSheetProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  batches: PriceChangeBatch[]
  preferredGroup?: string
  loading?: boolean
}

/** Right-side drawer on the pricing page showing the 30-day batch timeline */
export function PriceChangesSheet({
  open,
  onOpenChange,
  batches,
  preferredGroup,
  loading,
}: PriceChangesSheetProps) {
  const { t } = useTranslation()

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className='flex w-full flex-col gap-0 sm:max-w-xl'>
        <SheetHeader>
          <SheetTitle>{t('Price Updates')}</SheetTitle>
          <SheetDescription>
            {t('Published price changes in the last 30 days')}
          </SheetDescription>
        </SheetHeader>
        <ScrollArea className='min-h-0 flex-1 px-4 pb-4'>
          <PriceChangeBatchList
            batches={batches}
            preferredGroup={preferredGroup}
            loading={loading}
          />
        </ScrollArea>
      </SheetContent>
    </Sheet>
  )
}
