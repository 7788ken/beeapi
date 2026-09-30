import { useMemo } from 'react'
import { AlertTriangle, OctagonAlert } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Separator } from '@/components/ui/separator'
import { SettingsSection } from '../components/settings-section'
import { ClassificationDryRun } from './classification-dry-run'
import { detectGovernanceConflicts } from './conflict-detection'
import {
  DisposalFlowDiagram,
  type DisposalFlowContext,
} from './disposal-flow-diagram'

/** 只读：开关和判定条件都在各自的功能 tab 里保存，这里只展示后果。 */
export type OverviewContext = {
  autoTestEnabled: boolean
  healthEnabled: boolean
  modelMissingRemovalEnabled: boolean
  autoTestMinutes: number
  automaticDisableChannelEnabled: boolean
  automaticEnableChannelEnabled: boolean
  disableThreshold: number
  baseDegradeThreshold: number
  levelStepThreshold: number
  maxDegradeLevel: number
  count429AsError: boolean
  recoveryStrategy: string
  modelRateLimitRemovalEnabled: boolean
  modelForbiddenRemovalEnabled: boolean
  degradeProbeEnabled: boolean
  rebounceProtectionMinutes: number
}

export type OverviewSectionProps = {
  context: OverviewContext
}

export function GovernanceOverviewSection({ context }: OverviewSectionProps) {
  const { t } = useTranslation()

  const flowContext = useMemo<DisposalFlowContext>(
    () => ({
      autoTestEnabled: context.autoTestEnabled,
      healthEnabled: context.healthEnabled,
      modelMissingRemovalEnabled: context.modelMissingRemovalEnabled,
      modelRateLimitRemovalEnabled: context.modelRateLimitRemovalEnabled,
      modelForbiddenRemovalEnabled: context.modelForbiddenRemovalEnabled,
      automaticDisableChannelEnabled: context.automaticDisableChannelEnabled,
      automaticEnableChannelEnabled: context.automaticEnableChannelEnabled,
      disableThreshold: context.disableThreshold,
      maxDegradeLevel: context.maxDegradeLevel,
      degradeProbeEnabled: context.degradeProbeEnabled,
      recoveryStrategy: context.recoveryStrategy,
      rebounceProtectionMinutes: context.rebounceProtectionMinutes,
    }),
    [context]
  )
  const conflicts = useMemo(
    () =>
      detectGovernanceConflicts({
        autoTestEnabled: context.autoTestEnabled,
        healthEnabled: context.healthEnabled,
        automaticDisableChannelEnabled: context.automaticDisableChannelEnabled,
        automaticEnableChannelEnabled: context.automaticEnableChannelEnabled,
        disableThreshold: context.disableThreshold,
        baseDegradeThreshold: context.baseDegradeThreshold,
        levelStepThreshold: context.levelStepThreshold,
        maxDegradeLevel: context.maxDegradeLevel,
        count429AsError: context.count429AsError,
        recoveryStrategy: context.recoveryStrategy,
        modelRateLimitRemovalEnabled: context.modelRateLimitRemovalEnabled,
      }),
    [context]
  )

  return (
    <SettingsSection
      title={t('Governance Overview')}
      description={t(
        'A map of what can happen on its own. The switches, status codes and keywords are edited on the tab that uses them.'
      )}
    >
      {conflicts.length > 0 && (
        <div className='space-y-3'>
          {conflicts.map((conflict) => (
            <Alert
              key={conflict.id}
              variant={conflict.level === 'error' ? 'destructive' : 'default'}
            >
              {conflict.level === 'error' ? (
                <OctagonAlert />
              ) : (
                <AlertTriangle />
              )}
              <AlertTitle>{t(conflict.title)}</AlertTitle>
              <AlertDescription>
                {t(conflict.detail, conflict.values ?? {})}
              </AlertDescription>
            </Alert>
          ))}
        </div>
      )}

      <div className='space-y-2'>
        <h4 className='text-sm font-semibold'>{t('Disposal paths')}</h4>
        <p className='text-muted-foreground text-sm'>
          {t(
            'What can happen to a channel without anyone watching, and what can undo it. Greyed dashed paths are not in effect right now; the boxes carry live counts, so a greyed box with a non-zero count is where channels are stuck.'
          )}
        </p>
        <DisposalFlowDiagram ctx={flowContext} />
      </div>

      <Separator />

      <ClassificationDryRun />
    </SettingsSection>
  )
}
