import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { CloudDownload, Loader2, RefreshCw, ShieldAlert } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Switch } from '@/components/ui/switch'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import {
  createSubSiteChannels,
  getSubSiteGroups,
  listSubSites,
} from '@/features/system-settings/api'
import type {
  SubSite,
  SubSiteCreateGroupRow,
  SubSiteCreateResult,
} from '@/features/system-settings/types'
import { channelsQueryKeys } from '../lib'

type Props = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

type RowState = {
  selected: boolean
  localName: string
}

export function UpstreamSyncDialog({ open, onOpenChange }: Props) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const [siteId, setSiteId] = useState<number | null>(null)
  const [strategy, setStrategy] = useState<'create' | 'overwrite'>('create')
  const [provisionKeys, setProvisionKeys] = useState(true)
  const [rows, setRows] = useState<Record<string, RowState>>({})
  const [confirmText, setConfirmText] = useState('')
  const [plan, setPlan] = useState<SubSiteCreateResult | null>(null)
  const [result, setResult] = useState<SubSiteCreateResult | null>(null)

  // 关闭时重置
  useEffect(() => {
    if (!open) {
      setSiteId(null)
      setRows({})
      setPlan(null)
      setResult(null)
      setConfirmText('')
      setStrategy('create')
      setProvisionKeys(true)
    }
  }, [open])

  const { data: sites = [], isLoading: sitesLoading } = useQuery({
    queryKey: ['channels', 'sub-sites'],
    queryFn: listSubSites,
    enabled: open,
  })

  const availableSites = useMemo(
    () =>
      sites.filter(
        (s: SubSite) => s.enabled && s.last_verified_status === 'ok'
      ),
    [sites]
  )

  const {
    data: groupsResp,
    isLoading: groupsLoading,
    refetch: refetchGroups,
  } = useQuery({
    queryKey: ['channels', 'sub-site-groups', siteId],
    queryFn: () => getSubSiteGroups(siteId!, false),
    enabled: open && siteId != null,
  })

  const groups = groupsResp?.data?.groups ?? []
  const currentSite = availableSites.find((s) => s.id === siteId)

  // groups 变化时初始化 rows
  useEffect(() => {
    if (!groups.length || !currentSite) return
    const next: Record<string, RowState> = {}
    for (const g of groups) {
      next[g.group] = {
        selected: false,
        localName: `${currentSite.name}-${g.group}`,
      }
    }
    setRows(next)
    setPlan(null)
    setResult(null)
  }, [groupsResp, currentSite])

  const selectedGroups = useMemo<SubSiteCreateGroupRow[]>(() => {
    return groups
      .filter((g) => rows[g.group]?.selected)
      .map((g) => ({
        group: g.group,
        local_name: rows[g.group]?.localName?.trim() || `${currentSite?.name ?? 'sub'}-${g.group}`,
        models: g.models ?? [],
      }))
  }, [groups, rows, currentSite?.name])

  const previewMutation = useMutation({
    mutationFn: () =>
      createSubSiteChannels(siteId!, {
        strategy: 'skip',
        provision_keys: provisionKeys,
        groups: selectedGroups,
      }),
    onSuccess: (res) => {
      if (!res.success || !res.data) {
        toast.error(res.message ?? t('Preview failed'))
        return
      }
      setPlan(res.data)
      setResult(null)
    },
    onError: (e: Error) => toast.error(e.message ?? t('Preview failed')),
  })

  const executeMutation = useMutation({
    mutationFn: () =>
      createSubSiteChannels(siteId!, {
        strategy,
        confirm: strategy === 'overwrite',
        provision_keys: provisionKeys,
        groups: selectedGroups,
      }),
    onSuccess: (res) => {
      if (!res.success || !res.data) {
        toast.error(res.message ?? t('Execute failed'))
        return
      }
      setResult(res.data)
      const okN = res.data.ok?.length ?? 0
      const failedN = res.data.failed?.length ?? 0
      if (failedN === 0) toast.success(t('{{n}} channels processed', { n: okN }))
      else toast.warning(t('{{ok}} ok / {{fail}} failed', { ok: okN, fail: failedN }))
      void queryClient.invalidateQueries({ queryKey: channelsQueryKeys.all })
    },
    onError: (e: Error) => toast.error(e.message ?? t('Execute failed')),
  })

  const overwriteUnlocked =
    strategy !== 'overwrite' || confirmText.trim().toUpperCase() === 'OVERWRITE'

  const allSelected =
    groups.length > 0 && groups.every((g) => rows[g.group]?.selected)
  const someSelected = selectedGroups.length > 0

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='max-h-[90vh] sm:max-w-5xl'>
        <DialogHeader>
          <DialogTitle className='flex items-center gap-2'>
            <CloudDownload className='h-5 w-5' />
            {t('Upstream Sync')}
          </DialogTitle>
          <DialogDescription>
            {t(
              'Fetch sellable groups from a sub-site and quickly create local channels.'
            )}
          </DialogDescription>
        </DialogHeader>

        <ScrollArea className='max-h-[60vh] pr-3'>
          <div className='space-y-4'>
            <div className='space-y-2'>
              <Label>{t('Sub-Site')}</Label>
              <div className='flex items-center gap-2'>
                <Select
                  value={siteId != null ? String(siteId) : ''}
                  onValueChange={(v) => setSiteId(v ? Number(v) : null)}
                  disabled={sitesLoading || !availableSites.length}
                >
                  <SelectTrigger className='w-72'>
                    <SelectValue placeholder={t('Select a verified sub-site...')} />
                  </SelectTrigger>
                  <SelectContent>
                    {availableSites.map((s) => (
                      <SelectItem key={s.id} value={String(s.id)}>
                        {s.name} <span className='text-muted-foreground'>· {s.base_url}</span>
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <Button
                  size='sm'
                  variant='outline'
                  disabled={siteId == null || groupsLoading}
                  onClick={() => void refetchGroups()}
                >
                  {groupsLoading ? (
                    <Loader2 className='h-4 w-4 animate-spin' />
                  ) : (
                    <RefreshCw className='h-4 w-4' />
                  )}
                  {t('Refresh groups')}
                </Button>
                {availableSites.length === 0 && !sitesLoading && (
                  <span className='text-muted-foreground text-xs'>
                    {t('No verified sub-sites. Go to System Settings → Integrations → Sub-Site Sync.')}
                  </span>
                )}
              </div>
            </div>

            <div className='flex items-start justify-between gap-3 rounded-md border bg-muted/40 p-3'>
              <div className='flex items-start gap-2 text-sm'>
                <ShieldAlert className='mt-0.5 h-4 w-4 shrink-0 text-amber-600' />
                <div className='space-y-0.5'>
                  <div className='font-medium'>
                    {t('Provision upstream token for each channel')}
                  </div>
                  <div className='text-muted-foreground text-xs leading-snug'>
                    {provisionKeys
                      ? t(
                          'A dedicated token will be issued on the sub-site (POST /api/token/) for every selected group. Local channel.key uses that token, not the admin token.'
                        )
                      : t(
                          'Local channel.key will reuse the sub-site admin token. Created channels start as Disabled — replace the key and enable manually before routing traffic.'
                        )}
                  </div>
                </div>
              </div>
              <div className='flex items-center gap-2'>
                <Label htmlFor='provision-keys' className='text-xs'>
                  {provisionKeys ? t('On') : t('Off')}
                </Label>
                <Switch
                  id='provision-keys'
                  checked={provisionKeys}
                  onCheckedChange={setProvisionKeys}
                />
              </div>
            </div>

            {siteId != null && (
              <div className='overflow-x-auto rounded-md border'>
                <Table className='min-w-[860px] table-fixed'>
                  <TableHeader>
                    <TableRow>
                      <TableHead className='w-10'>
                        <Checkbox
                          checked={allSelected}
                          onCheckedChange={(c) => {
                            const next = { ...rows }
                            for (const g of groups) {
                              if (!next[g.group])
                                next[g.group] = { selected: false, localName: '' }
                              next[g.group].selected = !!c
                            }
                            setRows(next)
                          }}
                        />
                      </TableHead>
                      <TableHead className='w-[14rem]'>{t('Group')}</TableHead>
                      <TableHead className='w-16'>{t('Ratio')}</TableHead>
                      <TableHead className='w-[20rem]'>{t('Models')}</TableHead>
                      <TableHead className='w-[14rem]'>{t('Local channel name')}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {groupsLoading && (
                      <TableRow>
                        <TableCell colSpan={5} className='text-center text-muted-foreground'>
                          <Loader2 className='mr-2 inline h-4 w-4 animate-spin' />
                          {t('Loading...')}
                        </TableCell>
                      </TableRow>
                    )}
                    {!groupsLoading && groups.length === 0 && (
                      <TableRow>
                        <TableCell colSpan={5} className='text-center text-muted-foreground'>
                          {t('No groups returned by upstream.')}
                        </TableCell>
                      </TableRow>
                    )}
                    {groups.map((g) => {
                      const r = rows[g.group] ?? { selected: false, localName: '' }
                      return (
                        <TableRow key={g.group}>
                          <TableCell>
                            <Checkbox
                              checked={r.selected}
                              onCheckedChange={(c) =>
                                setRows((prev) => ({
                                  ...prev,
                                  [g.group]: { ...prev[g.group]!, selected: !!c },
                                }))
                              }
                            />
                          </TableCell>
                          <TableCell className='align-top'>
                            <div className='truncate font-medium' title={g.group}>
                              {g.group}
                            </div>
                            {g.description && (
                              <div
                                className='text-muted-foreground line-clamp-2 text-xs'
                                title={g.description}
                              >
                                {g.description}
                              </div>
                            )}
                          </TableCell>
                          <TableCell className='align-top tabular-nums'>
                            {g.ratio || '-'}
                          </TableCell>
                          <TableCell className='align-top'>
                            <div
                              className='line-clamp-2 break-all text-xs'
                              title={(g.models ?? []).join(', ')}
                            >
                              {(() => {
                                const ms = g.models ?? []
                                return ms.length
                                  ? ms.slice(0, 6).join(', ') +
                                      (ms.length > 6 ? ` +${ms.length - 6}` : '')
                                  : t('(empty)')
                              })()}
                            </div>
                          </TableCell>
                          <TableCell className='align-top'>
                            <Input
                              value={r.localName}
                              onChange={(e) =>
                                setRows((prev) => ({
                                  ...prev,
                                  [g.group]: {
                                    ...prev[g.group]!,
                                    localName: e.target.value,
                                  },
                                }))
                              }
                              className='h-8 w-full'
                            />
                          </TableCell>
                        </TableRow>
                      )
                    })}
                  </TableBody>
                </Table>
              </div>
            )}

            <div className='flex items-center gap-4'>
              <Label>{t('Strategy')}</Label>
              <RadioGroup
                value={strategy}
                onValueChange={(v) => setStrategy(v as 'create' | 'overwrite')}
                className='flex gap-4'
              >
                <div className='flex items-center gap-2'>
                  <RadioGroupItem id='strategy-create' value='create' />
                  <Label htmlFor='strategy-create'>{t('Create (skip duplicates)')}</Label>
                </div>
                <div className='flex items-center gap-2'>
                  <RadioGroupItem id='strategy-overwrite' value='overwrite' />
                  <Label htmlFor='strategy-overwrite'>{t('Overwrite existing')}</Label>
                </div>
              </RadioGroup>
            </div>

            {strategy === 'overwrite' && (
              <div className='space-y-2 rounded-md border border-destructive/40 bg-destructive/5 p-3'>
                <Label className='text-destructive'>
                  {t('Type OVERWRITE to confirm destructive update')}
                </Label>
                <Input
                  placeholder='OVERWRITE'
                  value={confirmText}
                  onChange={(e) => setConfirmText(e.target.value)}
                />
              </div>
            )}

            {plan && (
              <PlanPreview plan={plan} />
            )}

            {result && <ExecuteResult result={result} />}
          </div>
        </ScrollArea>

        <DialogFooter className='gap-2'>
          <Button variant='outline' onClick={() => onOpenChange(false)}>
            {t('Close')}
          </Button>
          <Button
            variant='secondary'
            disabled={
              !someSelected ||
              previewMutation.isPending ||
              executeMutation.isPending
            }
            onClick={() => previewMutation.mutate()}
          >
            {previewMutation.isPending ? (
              <Loader2 className='h-4 w-4 animate-spin' />
            ) : null}
            {t('Generate preview')}
          </Button>
          <Button
            disabled={
              !someSelected ||
              !overwriteUnlocked ||
              executeMutation.isPending
            }
            onClick={() => executeMutation.mutate()}
          >
            {executeMutation.isPending ? (
              <Loader2 className='h-4 w-4 animate-spin' />
            ) : null}
            {t('Execute')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function PlanPreview({ plan }: { plan: SubSiteCreateResult }) {
  const { t } = useTranslation()
  const creates = plan.plan?.filter((p) => p.action === 'will_create') ?? []
  const overwrites = plan.plan?.filter((p) => p.action === 'will_overwrite') ?? []
  return (
    <div className='space-y-2 rounded-md border p-3'>
      <div className='text-sm font-medium'>{t('Plan preview (dry-run)')}</div>
      <div className='grid grid-cols-2 gap-4 text-sm'>
        <div>
          <div className='text-muted-foreground mb-1'>
            <Badge variant='secondary'>{t('Will create')}</Badge> {creates.length}
          </div>
          <ul className='space-y-0.5'>
            {creates.map((p) => (
              <li key={p.local_name} className='font-mono text-xs'>
                {p.group} → {p.local_name}
              </li>
            ))}
          </ul>
        </div>
        <div>
          <div className='text-muted-foreground mb-1'>
            <Badge variant='destructive'>{t('Will overwrite')}</Badge>{' '}
            {overwrites.length}
          </div>
          <ul className='space-y-0.5'>
            {overwrites.map((p) => (
              <li key={p.local_name} className='font-mono text-xs'>
                {p.group} → {p.local_name}{' '}
                <span className='text-muted-foreground'>
                  (channel #{p.channel_id})
                </span>
              </li>
            ))}
          </ul>
        </div>
      </div>
    </div>
  )
}

function ExecuteResult({ result }: { result: SubSiteCreateResult }) {
  const { t } = useTranslation()
  const ok = result.ok ?? []
  const skipped = result.skipped ?? []
  const failed = result.failed ?? []
  return (
    <Tabs defaultValue='ok'>
      <TabsList>
        <TabsTrigger value='ok'>
          {t('OK')} <Badge className='ml-2'>{ok.length}</Badge>
        </TabsTrigger>
        <TabsTrigger value='skipped'>
          {t('Skipped')} <Badge variant='outline' className='ml-2'>{skipped.length}</Badge>
        </TabsTrigger>
        <TabsTrigger value='failed'>
          {t('Failed')} <Badge variant='destructive' className='ml-2'>{failed.length}</Badge>
        </TabsTrigger>
      </TabsList>
      <TabsContent value='ok'>
        <ul className='space-y-0.5 text-xs font-mono'>
          {ok.map((o) => (
            <li key={`${o.action}-${o.local_name}`}>
              [{o.action}] {o.group} → {o.local_name} (channel #{o.channel_id})
              {o.provisioned_key && o.upstream_token_id ? (
                <span className='text-emerald-700 dark:text-emerald-400'>
                  {' '}· {t('upstream token')} #{o.upstream_token_id}
                </span>
              ) : null}
            </li>
          ))}
        </ul>
      </TabsContent>
      <TabsContent value='skipped'>
        <ul className='space-y-0.5 text-xs font-mono'>
          {skipped.map((s) => (
            <li key={s.local_name}>
              {s.group} → {s.local_name} · {s.reason}
            </li>
          ))}
        </ul>
      </TabsContent>
      <TabsContent value='failed'>
        <ul className='space-y-0.5 text-xs font-mono text-destructive'>
          {failed.map((f) => (
            <li key={f.local_name}>
              {f.group} → {f.local_name} · {f.error}
            </li>
          ))}
        </ul>
      </TabsContent>
    </Tabs>
  )
}
