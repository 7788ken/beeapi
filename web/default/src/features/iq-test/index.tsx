import { useEffect, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import { LoaderCircle, Play } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useAdminPerms } from '@/hooks/use-admin'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { SectionPageLayout } from '@/components/layout'
import {
  getIQCoverage,
  getIQModels,
  getIQRun,
  getIQSetting,
  getIQRuns,
  iqKeys,
  startIQRun,
} from './api'
import { IQCoverageSummary } from './components/coverage'
import { IQModels } from './components/models'
import { IQResults } from './components/results'
import { IQRuns } from './components/runs'
import { IQSettings } from './components/settings'
import { IQStatus } from './components/shared'
import { iqErrorMessage } from './lib'

const route = getRouteApi('/_authenticated/iq-test/')

export function IQTest() {
  const { t } = useTranslation()
  const perms = useAdminPerms()
  const client = useQueryClient()
  const search = route.useSearch()
  const [tab, setTab] = useState(search.channel_id ? 'results' : 'runs')
  const [scope, setScope] = useState(search.channel_id ? 'single' : 'all')
  const [channelID, setChannelID] = useState(
    search.channel_id ? String(search.channel_id) : ''
  )
  const [models, setModels] = useState<string[]>([])
  const [selectedRun, setSelectedRun] = useState('')
  const [activeRunID, setActiveRunID] = useState('')
  const requestKey = useRef<{ scope: string; key: string } | null>(null)
  const setting = useQuery({ queryKey: iqKeys.setting, queryFn: getIQSetting })
  const configuredModels = useQuery({
    queryKey: [...iqKeys.models, 'enabled'],
    queryFn: () => getIQModels(1, true, 100),
  })
  const latest = useQuery({
    queryKey: [...iqKeys.runs, 'latest'],
    queryFn: () => getIQRuns({ page: 1, page_size: 1 }),
    refetchInterval: 4000,
  })
  const active = useQuery({
    queryKey: [...iqKeys.runs, 'active', activeRunID],
    queryFn: () => getIQRun(activeRunID),
    enabled: Boolean(activeRunID),
    refetchInterval: (query) =>
      query.state.data?.status === 'running' ? 2000 : false,
  })
  const latestRun = latest.data?.items[0]
  const run =
    active.data && (!latestRun || active.data.id >= latestRun.id)
      ? active.data
      : latestRun
  const busy =
    latest.data?.items[0]?.status === 'running' ||
    active.data?.status === 'running'
  const start = useMutation({
    mutationFn: startIQRun,
    onSuccess: (created) => {
      setActiveRunID(created.run_id)
      requestKey.current = null
      setTab('runs')
      void client.invalidateQueries({ queryKey: iqKeys.runs })
    },
    // A 409 means another round already owns the global lease; refresh so that
    // round is shown instead of leaving the operator with a bare error.
    onError: () => {
      void client.invalidateQueries({ queryKey: iqKeys.runs })
    },
  })
  const parsedChannelID = scope === 'single' ? Number(channelID) : 0
  const scopedChannelID =
    Number.isSafeInteger(parsedChannelID) && parsedChannelID > 0
      ? parsedChannelID
      : undefined
  const requestedModels = models.length ? models : undefined
  const coverage = useQuery({
    queryKey: [...iqKeys.coverage, scopedChannelID ?? 'all', models.join(',')],
    queryFn: () =>
      getIQCoverage({
        channel_id: scopedChannelID,
        model_names: requestedModels,
      }),
    // With "single channel" selected but no valid ID typed yet there is
    // nothing meaningful to preview; wait instead of falling back to all.
    enabled:
      perms.channel_edit &&
      (scope !== 'single' || scopedChannelID !== undefined),
    staleTime: 30_000,
  })
  useEffect(() => {
    if (active.data && active.data.status !== 'running') {
      void client.invalidateQueries({ queryKey: iqKeys.results })
      void client.invalidateQueries({ queryKey: ['channels'] })
    }
  }, [active.data, client])

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('IQ Management')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        {setting.data && (
          <span className='text-muted-foreground text-xs'>
            {setting.data.enabled ? t('IQ Enabled') : t('Disabled')}
          </span>
        )}
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='space-y-5'>
          {perms.channel_edit && (
            <form
              className='flex flex-wrap items-end gap-3 border-b pb-5'
              onSubmit={(event) => {
                event.preventDefault()
                if (busy || start.isPending || !setting.data?.enabled) return
                if (
                  scope === 'single' &&
                  (!Number.isSafeInteger(Number(channelID)) ||
                    Number(channelID) <= 0)
                )
                  return
                const identity =
                  scope === 'single' ? `channel:${channelID}` : 'all'
                if (
                  !requestKey.current ||
                  requestKey.current.scope !== identity
                )
                  requestKey.current = {
                    scope: identity,
                    key: crypto.randomUUID(),
                  }
                start.mutate({
                  channel_id: scopedChannelID,
                  model_names: requestedModels,
                  idempotency_key: requestKey.current.key,
                })
              }}
            >
              <div className='w-44 space-y-1.5'>
                <Label htmlFor='iq-run-scope'>{t('IQ Scope')}</Label>
                <Select value={scope} onValueChange={setScope}>
                  <SelectTrigger id='iq-run-scope'>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value='all'>{t('All Channels')}</SelectItem>
                    <SelectItem value='single'>
                      {t('IQ Single channel')}
                    </SelectItem>
                  </SelectContent>
                </Select>
              </div>
              {scope === 'single' && (
                <div className='w-36 space-y-1.5'>
                  <Label htmlFor='iq-run-channel'>{t('Channel ID')}</Label>
                  <Input
                    id='iq-run-channel'
                    type='number'
                    required
                    min={1}
                    step={1}
                    value={channelID}
                    onChange={(event) => setChannelID(event.target.value)}
                  />
                </div>
              )}
              <div className='min-w-48 flex-1 space-y-1.5'>
                <Label>{t('IQ Models to probe')}</Label>
                <div className='flex flex-wrap gap-1.5'>
                  {(configuredModels.data?.items ?? [])
                    .filter((model) => model.enabled)
                    .map((model) => {
                      const picked = models.includes(model.model_name)
                      return (
                        <Button
                          key={model.id}
                          type='button'
                          size='sm'
                          variant={picked ? 'secondary' : 'outline'}
                          aria-pressed={picked}
                          className='text-xs'
                          onClick={() =>
                            setModels((current) =>
                              picked
                                ? current.filter(
                                    (name) => name !== model.model_name
                                  )
                                : [...current, model.model_name]
                            )
                          }
                        >
                          {model.model_name}
                        </Button>
                      )
                    })}
                  {configuredModels.data &&
                    !configuredModels.data.items.some(
                      (model) => model.enabled
                    ) && (
                      <span className='text-muted-foreground text-xs'>
                        {t('IQ No enabled models')}
                      </span>
                    )}
                </div>
                <p className='text-muted-foreground text-xs'>
                  {models.length
                    ? t('IQ Selected models', { count: models.length })
                    : t('IQ All enabled models')}
                </p>
              </div>
              <Button
                type='submit'
                disabled={
                  busy ||
                  start.isPending ||
                  !setting.data?.enabled ||
                  latest.isPending ||
                  latest.isError
                }
              >
                {busy || start.isPending ? (
                  <LoaderCircle className='size-4 animate-spin' />
                ) : (
                  <Play className='size-4' />
                )}
                {t('IQ Run now')}
              </Button>
              {run && (
                <div className='flex min-w-0 items-center gap-2 pb-2'>
                  <IQStatus status={run.status} className='bg-card' />
                  <span className='text-muted-foreground text-xs tabular-nums'>
                    {run.success_count + run.invalid_count + run.error_count} /{' '}
                    {run.candidate_count}
                  </span>
                </div>
              )}
              {start.error && (
                <p
                  role='alert'
                  className='text-destructive w-full text-sm break-words'
                >
                  {iqErrorMessage(start.error)}
                </p>
              )}
            </form>
          )}
          {perms.channel_edit && <IQCoverageSummary coverage={coverage.data} />}
          <Tabs value={tab} onValueChange={setTab}>
            <TabsList className='grid h-auto w-full grid-cols-4 sm:inline-flex sm:w-auto'>
              <TabsTrigger value='runs'>{t('IQ Runs')}</TabsTrigger>
              <TabsTrigger value='results'>{t('IQ Results')}</TabsTrigger>
              <TabsTrigger value='models'>{t('Models')}</TabsTrigger>
              <TabsTrigger value='settings'>{t('Settings')}</TabsTrigger>
            </TabsList>
            <TabsContent value='runs' className='mt-4'>
              <IQRuns
                onSelect={(runID) => {
                  setSelectedRun(runID)
                  setTab('results')
                }}
              />
            </TabsContent>
            <TabsContent value='results' className='mt-4'>
              <IQResults
                key={selectedRun}
                runID={selectedRun}
                initialChannelID={search.channel_id}
                clearRun={() => setSelectedRun('')}
              />
            </TabsContent>
            <TabsContent value='models' className='mt-4'>
              <IQModels editable={perms.channel_edit} />
            </TabsContent>
            <TabsContent value='settings' className='mt-4'>
              <IQSettings editable={perms.channel_edit} />
            </TabsContent>
          </Tabs>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
