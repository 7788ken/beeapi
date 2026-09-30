import { useMemo } from 'react'
import { useParams } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { useStatus } from '@/hooks/use-status'
import { getOptionValue, useSystemOptions } from '../hooks/use-system-options'
import { DEFAULT_MAINTENANCE_SETTINGS } from './config'
import {
  MAINTENANCE_DEFAULT_SECTION,
  getMaintenanceSectionContent,
} from './section-registry.tsx'

export function MaintenanceSettings() {
  const { t } = useTranslation()
  const { data, isLoading } = useSystemOptions()
  const { status } = useStatus()
  const params = useParams({
    from: '/_authenticated/system-settings/maintenance/$section',
  })

  const settings = useMemo(
    () => getOptionValue(data?.data, DEFAULT_MAINTENANCE_SETTINGS),
    [data?.data]
  )

  if (isLoading) {
    return (
      <div className='text-muted-foreground flex items-center justify-center py-12'>
        {t('Loading maintenance settings...')}
      </div>
    )
  }

  const activeSection = (params?.section ?? MAINTENANCE_DEFAULT_SECTION) as
    | 'update-checker'
    | 'notice'
    | 'logs'
    | 'header-navigation'
    | 'sidebar-modules'
    | 'performance'
  const sectionContent = getMaintenanceSectionContent(
    activeSection,
    settings,
    status?.version as string | undefined,
    status?.start_time as number | null | undefined
  )

  return (
    <div className='space-y-4'>{sectionContent}</div>
  )
}
