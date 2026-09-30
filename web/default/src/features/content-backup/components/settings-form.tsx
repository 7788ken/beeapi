import { useEffect, useMemo, useState } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import * as z from 'zod'
import { CheckCircle2, ChevronDown, RefreshCw, XCircle } from 'lucide-react'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { surfaceClass } from '@/components/ui/card'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Separator } from '@/components/ui/separator'
import { Switch } from '@/components/ui/switch'
import {
  apiErrorStatus,
  describeApiError,
  useContentBackupConfig,
  useContentBackupNodes,
  useSaveContentBackupConfig,
  useTestContentBackupConnection,
} from '../api'
import {
  DEFAULT_FORM_VALUES,
  isValidRemoteBaseDir,
  mergeFormValues,
  normalizeConfigForForm,
  sanitizeInactiveProtocolFields,
} from '../config-form'
import { normalizeHostKeyPin } from '../host-key-pin'

const FTPS_HOST_PATTERN = /^[A-Za-z0-9._-]{1,253}$/
const CERT_SHA256_PATTERN = /^[a-f0-9]{64}$/
/** 与 pkg/contentbackup ValidateSiteLabel 一致 */
const SITE_LABEL_PATTERN = /^[a-z0-9][a-z0-9_-]{0,31}$/

/**
 * 只做数据形状校验（整数/必填）与「开启采集前三项必填」这一条最容易犯的错误；
 * 其余数值范围与跨字段规则（见 pkg/contentbackup/config.go ValidateConfig）
 * 不在前端复制一份，交由后端权威校验，失败信息原样展示在保存结果区。
 * 「开启采集前必填」的三项随所选协议切换：FTPS 看 ftps_host/cert_sha256，
 * SFTP 看 sftp_host/sftp_host_key_sha256；另一协议的字段允许留空。
 */
function createConfigSchema(t: (key: string) => string) {
  const hostRule = (value: string) =>
    value === '' || FTPS_HOST_PATTERN.test(value)
  const hostMessage = t(
    'Must be a bare hostname or IP, without scheme, port, path or credentials'
  )
  return z
    .object({
      enabled: z.boolean(),
      upload_paused: z.boolean(),
      site_label: z
        .string()
        .refine(
          (value) => value === '' || SITE_LABEL_PATTERN.test(value),
          t(
            'Lowercase letters, digits, "_" or "-", starting with a letter or digit, at most 32 characters'
          )
        ),
      target_id: z.string(),
      remote_username: z.string(),
      // GET 恒为空；留空 = 沿用已存密码（后端在事务内补全）。
      remote_password: z.string(),
      // 两种协议的主机/pin 格式只在 superRefine 里对**当前所选协议**校验：另一协议的字段被隐藏，
      // 在隐藏字段上报错等于静默拒绝保存（2026-09-18 ai 实发：FTPS 证书字段里残留了一个
      // base64，切到 SFTP 后保存按钮点了没有任何反应）。未选协议的非法值在提交时被清空。
      ftps_host: z.string(),
      ftps_port: z.number().int(),
      cert_sha256: z.string(),
      remote_protocol: z.enum(['ftps', 'sftp']),
      sftp_host: z.string(),
      sftp_port: z.number().int(),
      // 输入层先把 OpenSSH 的 SHA256:<base64> 翻成 hex（normalizeHostKeyPin），这里只认最终形态。
      sftp_host_key_sha256: z.string(),
      sftp_base_dir: z.string(),

      max_body_bytes: z.number().int(),
      capture_memory_mb: z.number().int(),
      max_inflight_captures: z.number().int(),
      handoff_workers: z.number().int(),
      spool_workers: z.number().int(),
      upload_workers: z.number().int(),
      read_workers: z.number().int(),
      max_spool_mb: z.number().int(),

      handoff_attempt_timeout_seconds: z.number().int(),
      handoff_deadline_seconds: z.number().int(),
      handoff_max_attempts: z.number().int(),

      daemon_db_max_open: z.number().int(),
      daemon_db_max_idle: z.number().int(),
      daemon_sqlite_db_max_open: z.number().int(),
      daemon_db_timeout_seconds: z.number().int(),
      config_reload_seconds: z.number().int(),

      upload_bandwidth_mib: z.number().int(),
      read_bandwidth_mib: z.number().int(),
      upload_timeout_seconds: z.number().int(),
      ftps_connect_timeout_seconds: z.number().int(),
      lease_seconds: z.number().int(),
      lease_renew_seconds: z.number().int(),
      max_upload_attempts: z.number().int(),

      read_timeout_seconds: z.number().int(),
      preview_bytes_per_side: z.number().int(),
      max_decompress_bytes: z.number().int(),
      read_budget_mb: z.number().int(),

      reconcile_interval_seconds: z.number().int(),
      heartbeat_interval_seconds: z.number().int(),
      node_offline_seconds: z.number().int(),
      queue_refresh_seconds: z.number().int(),
      node_stats_refresh_seconds: z.number().int(),

      spool_alert_percent: z.number().int(),
      spool_stop_percent: z.number().int(),
      inode_alert_percent: z.number().int(),
      inode_recover_percent: z.number().int(),
      min_free_bytes: z.number().int(),
      min_free_percent: z.number().int(),

      oldest_pending_alert_minutes: z.number().int(),
      cleanup_pending_alert_minutes: z.number().int(),
      alert_dedup_minutes: z.number().int(),

      notify_oldest_pending: z.boolean(),
      notify_cleanup_pending: z.boolean(),
      notify_spool_high: z.boolean(),
      notify_inode_high: z.boolean(),
      notify_failed: z.boolean(),
      notify_handoff_rejected: z.boolean(),
      notify_node_offline: z.boolean(),

      content_retention_days: z.number().int(),
      index_retention_days: z.number().int(),
      stats_retention_days: z.number().int(),
    })
    .superRefine((values, ctx) => {
      const pinMessage = t('Must be 64 lowercase hex characters')
      if (values.remote_protocol === 'sftp') {
        if (!hostRule(values.sftp_host)) {
          ctx.addIssue({ code: 'custom', path: ['sftp_host'], message: hostMessage })
        }
        if (
          values.sftp_host_key_sha256 !== '' &&
          !CERT_SHA256_PATTERN.test(values.sftp_host_key_sha256)
        ) {
          ctx.addIssue({ code: 'custom', path: ['sftp_host_key_sha256'], message: pinMessage })
        }
        if (!isValidRemoteBaseDir(values.sftp_base_dir)) {
          ctx.addIssue({
            code: 'custom',
            path: ['sftp_base_dir'],
            message: t(
              'Leave empty, or give an absolute path without a trailing slash or ./.. segments'
            ),
          })
        }
      } else {
        if (!hostRule(values.ftps_host)) {
          ctx.addIssue({ code: 'custom', path: ['ftps_host'], message: hostMessage })
        }
        if (values.cert_sha256 !== '' && !CERT_SHA256_PATTERN.test(values.cert_sha256)) {
          ctx.addIssue({ code: 'custom', path: ['cert_sha256'], message: pinMessage })
        }
      }
      if (!values.enabled) return
      if (values.site_label.trim() === '') {
        ctx.addIssue({
          code: 'custom',
          path: ['site_label'],
          message: t('Required before capture can be enabled'),
        })
      }
      if (values.target_id.trim() === '') {
        ctx.addIssue({
          code: 'custom',
          path: ['target_id'],
          message: t('Required before capture can be enabled'),
        })
      }
      if (values.remote_username.trim() === '') {
        ctx.addIssue({
          code: 'custom',
          path: ['remote_username'],
          message: t('Required before capture can be enabled'),
        })
      }
      const required: Array<[keyof typeof values, string]> =
        values.remote_protocol === 'sftp'
          ? [
              ['sftp_host', values.sftp_host],
              ['sftp_host_key_sha256', values.sftp_host_key_sha256],
            ]
          : [
              ['ftps_host', values.ftps_host],
              ['cert_sha256', values.cert_sha256],
            ]
      for (const [field, value] of required) {
        if (value.trim() === '') {
          ctx.addIssue({
            code: 'custom',
            path: [field],
            message: t('Required before capture can be enabled'),
          })
        }
      }
    })
}


type ContentBackupConfigFormValues = z.infer<ReturnType<typeof createConfigSchema>>

const NOTIFY_OPTIONS: Array<{
  key: Extract<
    keyof ContentBackupConfigFormValues,
    | 'notify_oldest_pending'
    | 'notify_cleanup_pending'
    | 'notify_spool_high'
    | 'notify_inode_high'
    | 'notify_failed'
    | 'notify_handoff_rejected'
    | 'notify_node_offline'
  >
  labelKey: string
  descKey: string
}> = [
  {
    key: 'notify_oldest_pending',
    labelKey: 'Notify: oldest pending upload',
    descKey: 'Email when the oldest queued upload has waited longer than the threshold.',
  },
  {
    key: 'notify_cleanup_pending',
    labelKey: 'Notify: local cleanup backlog',
    descKey: 'Email when uploaded files stay on the node longer than the cleanup threshold.',
  },
  {
    key: 'notify_spool_high',
    labelKey: 'Notify: spool space high',
    descKey: 'Email when the local spool directory crosses the alert percent.',
  },
  {
    key: 'notify_inode_high',
    labelKey: 'Notify: inode usage high',
    descKey: 'Email when inode usage crosses the alert percent.',
  },
  {
    key: 'notify_failed',
    labelKey: 'Notify: new upload failures',
    descKey: 'Email when the failed-job count increases.',
  },
  {
    key: 'notify_handoff_rejected',
    labelKey: 'Notify: handoff rejected',
    descKey: 'Email when the node starts rejecting new captures.',
  },
  {
    key: 'notify_node_offline',
    labelKey: 'Notify: storage node offline',
    descKey: 'Email when a storage node misses its heartbeat threshold.',
  },
]


/** 限定为数值字段的 key，既修复 field.value 的联合类型推断，也防止误把开关/字符串字段塞进数值分组 */
type NumericConfigKey = {
  [K in keyof ContentBackupConfigFormValues]: ContentBackupConfigFormValues[K] extends number
    ? K
    : never
}[keyof ContentBackupConfigFormValues]

interface AdvancedFieldMeta {
  key: NumericConfigKey
  labelKey: string
  min: number
  max: number
  /** daemon_sqlite_db_max_open 恒为 1（SQLite 单连接约束），禁止编辑 */
  fixed?: number
}

interface AdvancedFieldGroupMeta {
  titleKey: string
  fields: AdvancedFieldMeta[]
}

/** 分组与字段顺序、边界值均取自 pkg/contentbackup/config.go ValidateConfig 的 bounds 表 */
const ADVANCED_FIELD_GROUPS: AdvancedFieldGroupMeta[] = [
  {
    titleKey: 'Capacity & concurrency',
    fields: [
      { key: 'max_body_bytes', labelKey: 'Max body bytes per side', min: 1024, max: 8388608 },
      { key: 'capture_memory_mb', labelKey: 'Capture memory (MB)', min: 1, max: 4096 },
      { key: 'max_inflight_captures', labelKey: 'Max in-flight captures', min: 1, max: 8192 },
      { key: 'handoff_workers', labelKey: 'Handoff workers', min: 1, max: 16 },
      { key: 'spool_workers', labelKey: 'Spool workers', min: 1, max: 16 },
      { key: 'upload_workers', labelKey: 'Upload workers', min: 1, max: 48 },
      { key: 'read_workers', labelKey: 'Read workers', min: 1, max: 4 },
      { key: 'max_spool_mb', labelKey: 'Max spool size (MB)', min: 1, max: 1048576 },
    ],
  },
  {
    titleKey: 'Handoff timeouts',
    fields: [
      { key: 'handoff_attempt_timeout_seconds', labelKey: 'Handoff attempt timeout (seconds)', min: 5, max: 300 },
      { key: 'handoff_deadline_seconds', labelKey: 'Handoff deadline (seconds)', min: 10, max: 600 },
      { key: 'handoff_max_attempts', labelKey: 'Handoff max attempts', min: 1, max: 5 },
    ],
  },
  {
    titleKey: 'Backup database (only the timeout applies in-process)',
    fields: [
      { key: 'daemon_db_max_open', labelKey: 'Daemon DB max open connections', min: 1, max: 64 },
      { key: 'daemon_db_max_idle', labelKey: 'Daemon DB max idle connections', min: 0, max: 64 },
      {
        key: 'daemon_sqlite_db_max_open',
        labelKey: 'Daemon SQLite DB max open connections',
        min: 1,
        max: 1,
        fixed: 1,
      },
      { key: 'daemon_db_timeout_seconds', labelKey: 'Daemon DB timeout (seconds)', min: 1, max: 60 },
      { key: 'config_reload_seconds', labelKey: 'Config reload interval (seconds)', min: 1, max: 300 },
    ],
  },
  {
    titleKey: 'Upload & read bandwidth',
    fields: [
      { key: 'upload_bandwidth_mib', labelKey: 'Upload bandwidth (MiB/s)', min: 1, max: 4096 },
      { key: 'read_bandwidth_mib', labelKey: 'Read bandwidth (MiB/s)', min: 1, max: 4096 },
      { key: 'upload_timeout_seconds', labelKey: 'Upload timeout (seconds)', min: 10, max: 3600 },
      { key: 'ftps_connect_timeout_seconds', labelKey: 'Remote connect timeout (seconds)', min: 3, max: 300 },
      { key: 'lease_seconds', labelKey: 'Lease duration (seconds)', min: 30, max: 3600 },
      { key: 'lease_renew_seconds', labelKey: 'Lease renew interval (seconds)', min: 5, max: 600 },
      { key: 'max_upload_attempts', labelKey: 'Max upload attempts', min: 1, max: 64 },
    ],
  },
  {
    titleKey: 'Read & preview limits',
    fields: [
      { key: 'read_timeout_seconds', labelKey: 'Read timeout (seconds)', min: 5, max: 600 },
      { key: 'preview_bytes_per_side', labelKey: 'Preview bytes per side', min: 1024, max: 8388608 },
      { key: 'max_decompress_bytes', labelKey: 'Max decompress bytes', min: 1048576, max: 67108864 },
      { key: 'read_budget_mb', labelKey: 'Read budget (MB)', min: 1, max: 4096 },
    ],
  },
  {
    titleKey: 'Refresh intervals',
    fields: [
      { key: 'reconcile_interval_seconds', labelKey: 'Reconcile interval (seconds)', min: 5, max: 3600 },
      { key: 'heartbeat_interval_seconds', labelKey: 'Heartbeat interval (seconds)', min: 5, max: 600 },
      { key: 'node_offline_seconds', labelKey: 'Node offline threshold (seconds)', min: 10, max: 3600 },
      { key: 'queue_refresh_seconds', labelKey: 'Queue refresh interval (seconds)', min: 3, max: 600 },
      { key: 'node_stats_refresh_seconds', labelKey: 'Node stats refresh interval (seconds)', min: 5, max: 3600 },
    ],
  },
  {
    titleKey: 'Free space limits',
    fields: [
      { key: 'min_free_bytes', labelKey: 'Minimum free bytes', min: 0, max: 1099511627776 },
      { key: 'min_free_percent', labelKey: 'Minimum free percent (%)', min: 0, max: 90 },
    ],
  },
  {
    titleKey: 'Alert timing',
    fields: [
      { key: 'oldest_pending_alert_minutes', labelKey: 'Oldest pending alert (minutes)', min: 1, max: 1440 },
      { key: 'cleanup_pending_alert_minutes', labelKey: 'Cleanup pending alert (minutes)', min: 1, max: 14400 },
      { key: 'alert_dedup_minutes', labelKey: 'Alert deduplication window (minutes)', min: 1, max: 1440 },
    ],
  },
]

/**
 * Root only（由父路由 /system-settings 的 beforeLoad 保证，本组件不再重复判断）。
 * 整包保存 + expected_version 乐观并发；凭据只显示徽章，永不回显真实值。
 */
export function ContentBackupSettingsForm() {
  const { t } = useTranslation()
  const [advancedOpen, setAdvancedOpen] = useState(false)

  const configQuery = useContentBackupConfig()
  const nodesQuery = useContentBackupNodes()
  const saveMutation = useSaveContentBackupConfig()
  const testMutation = useTestContentBackupConnection()

  const schema = useMemo(() => createConfigSchema(t), [t])

  const form = useForm<ContentBackupConfigFormValues>({
    // 切协议 / 折叠高级设置会卸掉 Controller，RHF 交给 resolver 的对象缺那些键。
    // 先补成整包再交给 Zod，避免 "expected string, received undefined"。
    resolver: (values, context, options) =>
      zodResolver(schema)(mergeFormValues(values), context, options),
    defaultValues: DEFAULT_FORM_VALUES,
    shouldUnregister: false,
  })
  // 协议切换只影响显示哪一组目标字段；另一组保持挂载（hidden），整包保存不丢。
  const remoteProtocol = form.watch('remote_protocol')

  useEffect(() => {
    if (!configQuery.data) return
    const { version: _version, ...rest } = configQuery.data.config
    form.reset(normalizeConfigForForm(rest))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [configQuery.data])

  const onSubmit = async (values: ContentBackupConfigFormValues) => {
    if (!configQuery.data) return
    const expectedVersion = configQuery.data.config.version
    try {
      await saveMutation.mutateAsync({
        config: {
          ...sanitizeInactiveProtocolFields(values),
          version: expectedVersion,
        },
        expectedVersion,
      })
      toast.success(t('Content backup configuration saved'))
    } catch (error) {
      if (apiErrorStatus(error) === 409) {
        toast.error(
          t('Configuration changed elsewhere. Reload before saving again.')
        )
      } else {
        toast.error(
          describeApiError(error) ||
            t('Failed to save content backup configuration')
        )
      }
    }
  }

  const handleRefresh = () => {
    configQuery.refetch()
    nodesQuery.refetch()
  }

  return (
    <div className='space-y-8'>
      <div className='flex items-start justify-between gap-4'>
        <div>
          <h3 className='text-base font-semibold'>{t('Content backup')}</h3>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Configure the remote upload target (FTPS or SFTP), capture limits and operational tuning for the content backup pipeline.'
            )}
          </p>
        </div>
        <Button
          type='button'
          variant='outline'
          size='icon'
          className='size-8 shrink-0'
          onClick={handleRefresh}
          aria-label={t('Refresh')}
          title={t('Refresh')}
        >
          <RefreshCw />
        </Button>
      </div>

      <div className={cn(surfaceClass, 'space-y-8 p-6')}>
        {configQuery.isLoading && (
          <p className='text-muted-foreground text-sm'>
            {t('Loading configuration...')}
          </p>
        )}

        {configQuery.isError && (
          <Alert variant='destructive'>
            <XCircle />
            <AlertTitle>{t('Failed to load configuration')}</AlertTitle>
            <AlertDescription>
              {describeApiError(configQuery.error) ||
                t('Content backup config response missing data')}
            </AlertDescription>
          </Alert>
        )}

        {configQuery.data && (
          <Form {...form}>
            <form
              onSubmit={form.handleSubmit(onSubmit, (errors) => {
                // 校验失败绝不能静默：把第一条错误连字段名一起说出来，哪怕它在当前未显示的字段上。
                const [field, error] = Object.entries(errors)[0] ?? []
                toast.error(
                  t('Cannot save: {{field}} — {{message}}', {
                    field: field ?? '?',
                    message:
                      (error as { message?: string } | undefined)?.message ??
                      t('invalid value'),
                  })
                )
              })}
              className='space-y-8'
            >
              <section className='space-y-4'>
                <h4 className='text-sm font-medium'>{t('Core settings')}</h4>

                <FormField
                  control={form.control}
                  name='enabled'
                  render={({ field }) => (
                    <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                      <div className='space-y-0.5'>
                        <FormLabel className='text-base'>
                          {t('Capture new requests')}
                        </FormLabel>
                        <FormDescription>
                          {t(
                            'When on, new request and response bodies are captured for backup. Turn off to stop collecting new content immediately.'
                          )}
                        </FormDescription>
                      </div>
                      <FormControl>
                        <Switch
                          checked={field.value}
                          onCheckedChange={field.onChange}
                        />
                      </FormControl>
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='upload_paused'
                  render={({ field }) => (
                    <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                      <div className='space-y-0.5'>
                        <FormLabel className='text-base'>
                          {t('Pause uploads')}
                        </FormLabel>
                        <FormDescription>
                          {t(
                            'When on, captured content stays queued locally and is not uploaded. Independent from capture: pause uploads while still capturing, or keep uploading while capture is off to drain the backlog.'
                          )}
                        </FormDescription>
                      </div>
                      <FormControl>
                        <Switch
                          checked={field.value}
                          onCheckedChange={field.onChange}
                        />
                      </FormControl>
                    </FormItem>
                  )}
                />

                <div className='grid grid-cols-1 gap-4 sm:grid-cols-2'>
                  <FormField
                    control={form.control}
                    name='site_label'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Site label')}</FormLabel>
                        <FormControl>
                          <Input
                            {...field}
                            placeholder='ai'
                            onChange={(event) =>
                              field.onChange(event.target.value.trim())
                            }
                          />
                        </FormControl>
                        <FormDescription>
                          {t(
                            'Identity of this site: every archive is filed under it and it is the first directory on the remote. Set it once; changing it later hides existing archives from this page.'
                          )}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name='remote_username'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Remote username')}</FormLabel>
                        <FormControl>
                          <Input
                            {...field}
                            autoComplete='off'
                            onChange={(event) =>
                              field.onChange(event.target.value.trim())
                            }
                          />
                        </FormControl>
                        <FormDescription>
                          {t('Account used for both FTPS and SFTP.')}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name='remote_password'
                    render={({ field }) => (
                      <FormItem className='sm:col-span-2'>
                        <div className='flex items-center justify-between gap-2'>
                          <FormLabel>{t('Remote password')}</FormLabel>
                          <Badge
                            variant={
                              configQuery.data.remote_password_set
                                ? 'secondary'
                                : 'destructive'
                            }
                          >
                            {configQuery.data.remote_password_set
                              ? t('Password stored')
                              : t('No password stored')}
                          </Badge>
                        </div>
                        <FormControl>
                          <Input
                            {...field}
                            type='password'
                            autoComplete='new-password'
                            placeholder={
                              configQuery.data.remote_password_set
                                ? t('Leave empty to keep the stored password')
                                : ''
                            }
                            onChange={(event) =>
                              field.onChange(event.target.value)
                            }
                          />
                        </FormControl>
                        <FormDescription>
                          {t(
                            'Stored server-side like the SMTP and payment secrets and never sent back to the browser. Leave empty to keep the current password; type a new one to replace it.'
                          )}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </div>

                <div className='grid grid-cols-1 gap-4 sm:grid-cols-2'>
                  <FormField
                    control={form.control}
                    name='target_id'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Target ID')}</FormLabel>
                        <FormControl>
                          <Input
                            {...field}
                            onChange={(event) =>
                              field.onChange(event.target.value)
                            }
                          />
                        </FormControl>
                        <FormDescription>
                          {t(
                            'Your own stable label for this upload target (host, port and account root). Jobs are bound to it: change it only when moving to a different server, after the queue has drained.'
                          )}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name='remote_protocol'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Transfer protocol')}</FormLabel>
                        <Select
                          onValueChange={field.onChange}
                          value={field.value}
                        >
                          <FormControl>
                            <SelectTrigger>
                              <SelectValue />
                            </SelectTrigger>
                          </FormControl>
                          <SelectContent>
                            <SelectItem value='sftp'>
                              {t('SFTP (SSH, host key pinned)')}
                            </SelectItem>
                            <SelectItem value='ftps'>
                              {t('FTPS (explicit TLS, certificate pinned)')}
                            </SelectItem>
                          </SelectContent>
                        </Select>
                        <FormDescription>
                          {t(
                            'Only the selected protocol is used for uploads, reads and the connection test; the other protocol fields may stay empty.'
                          )}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <div
                    className={cn(
                      'col-span-full grid grid-cols-1 gap-4 sm:grid-cols-2',
                      remoteProtocol !== 'sftp' && 'hidden'
                    )}
                    aria-hidden={remoteProtocol !== 'sftp'}
                  >
                    <FormField
                      control={form.control}
                      name='sftp_host'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('SFTP host')}</FormLabel>
                          <FormControl>
                            <Input
                              {...field}
                              onChange={(event) =>
                                field.onChange(event.target.value)
                              }
                            />
                          </FormControl>
                          <FormDescription>
                            {t(
                              'Bare hostname or IP address, without scheme, port, path or credentials.'
                            )}
                          </FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />

                    <FormField
                      control={form.control}
                      name='sftp_port'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('SFTP port')}</FormLabel>
                          <FormControl>
                            <Input
                              type='number'
                              min={1}
                              max={65535}
                              value={field.value}
                              onChange={(event) =>
                                field.onChange(event.target.valueAsNumber)
                              }
                            />
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />

                    <FormField
                      control={form.control}
                      name='sftp_host_key_sha256'
                      render={({ field }) => (
                        <FormItem className='sm:col-span-2'>
                          <FormLabel>
                            {t('SSH host key SHA-256 fingerprint')}
                          </FormLabel>
                          <FormControl>
                            <Input
                              {...field}
                              onChange={(event) =>
                                field.onChange(event.target.value)
                              }
                              onBlur={(event) => {
                                field.onChange(
                                  normalizeHostKeyPin(event.target.value)
                                )
                                field.onBlur()
                              }}
                            />
                          </FormControl>
                          <FormDescription>
                            {t(
                              'Paste the output of "ssh-keyscan -t ed25519 <host> | ssh-keygen -lf -" (SHA256:... form) or 64 lowercase hex characters; the pin is checked before any password is sent.'
                            )}
                          </FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />

                    <FormField
                      control={form.control}
                      name='sftp_base_dir'
                      render={({ field }) => (
                        <FormItem className='sm:col-span-2'>
                          <FormLabel>{t('Remote base directory')}</FormLabel>
                          <FormControl>
                            <Input
                              {...field}
                              placeholder={t(
                                'Empty = the account login directory'
                              )}
                              onChange={(event) =>
                                field.onChange(event.target.value.trim())
                              }
                            />
                          </FormControl>
                          <FormDescription>
                            {t(
                              'Absolute directory the "/<site>/<date>/..." archive tree is created under. SFTP accounts are usually not chrooted, so leave it empty to use the directory the server logs the account into.'
                            )}
                          </FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  </div>
                  <div
                    className={cn(
                      'col-span-full grid grid-cols-1 gap-4 sm:grid-cols-2',
                      remoteProtocol !== 'ftps' && 'hidden'
                    )}
                    aria-hidden={remoteProtocol !== 'ftps'}
                  >
                    <FormField
                      control={form.control}
                      name='ftps_host'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('FTPS host')}</FormLabel>
                          <FormControl>
                            <Input
                              {...field}
                              onChange={(event) =>
                                field.onChange(event.target.value)
                              }
                            />
                          </FormControl>
                          <FormDescription>
                            {t(
                              'Bare hostname or IP address, without scheme, port, path or credentials.'
                            )}
                          </FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />

                    <FormField
                      control={form.control}
                      name='ftps_port'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('FTPS port')}</FormLabel>
                          <FormControl>
                            <Input
                              type='number'
                              min={1}
                              max={65535}
                              value={field.value}
                              onChange={(event) =>
                                field.onChange(event.target.valueAsNumber)
                              }
                            />
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />

                    <FormField
                      control={form.control}
                      name='cert_sha256'
                      render={({ field }) => (
                        <FormItem className='sm:col-span-2'>
                          <FormLabel>
                            {t('Certificate SHA-256 fingerprint')}
                          </FormLabel>
                          <FormControl>
                            <Input
                              {...field}
                              onChange={(event) =>
                                field.onChange(event.target.value)
                              }
                            />
                          </FormControl>
                          <FormDescription>
                            {t(
                              '64 lowercase hex characters pinning the leaf certificate.'
                            )}
                          </FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  </div>
                </div>

                <div className='grid grid-cols-1 gap-4 sm:grid-cols-3'>
                  <FormField
                    control={form.control}
                    name='content_retention_days'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Content retention (days)')}</FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min={1}
                            max={3650}
                            value={field.value}
                            onChange={(event) =>
                              field.onChange(event.target.valueAsNumber)
                            }
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name='index_retention_days'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Index retention (days)')}</FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min={1}
                            max={3650}
                            value={field.value}
                            onChange={(event) =>
                              field.onChange(event.target.valueAsNumber)
                            }
                          />
                        </FormControl>
                        <FormDescription>
                          {t('Must be at least as long as content retention.')}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name='stats_retention_days'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Stats retention (days)')}</FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min={1}
                            max={3650}
                            value={field.value}
                            onChange={(event) =>
                              field.onChange(event.target.valueAsNumber)
                            }
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </div>

                <div className='grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4'>
                  <FormField
                    control={form.control}
                    name='spool_alert_percent'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Spool alert threshold (%)')}</FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min={1}
                            max={99}
                            value={field.value}
                            onChange={(event) =>
                              field.onChange(event.target.valueAsNumber)
                            }
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name='spool_stop_percent'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Spool stop threshold (%)')}</FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min={2}
                            max={100}
                            value={field.value}
                            onChange={(event) =>
                              field.onChange(event.target.valueAsNumber)
                            }
                          />
                        </FormControl>
                        <FormDescription>
                          {t('Must exceed the spool alert threshold.')}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name='inode_alert_percent'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Inode alert threshold (%)')}</FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min={1}
                            max={100}
                            value={field.value}
                            onChange={(event) =>
                              field.onChange(event.target.valueAsNumber)
                            }
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name='inode_recover_percent'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>
                          {t('Inode recover threshold (%)')}
                        </FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min={1}
                            max={99}
                            value={field.value}
                            onChange={(event) =>
                              field.onChange(event.target.valueAsNumber)
                            }
                          />
                        </FormControl>
                        <FormDescription>
                          {t('Must stay below the inode alert threshold.')}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </div>
              </section>

              <Separator />

              <section className='space-y-4'>
                <div>
                  <h4 className='text-sm font-medium'>
                    {t('Email notifications')}
                  </h4>
                  <p className='text-muted-foreground text-sm'>
                    {t(
                      'Choose which content-backup alerts are emailed to the root notification address. Unchecked items are still recorded, but no message is sent.'
                    )}
                  </p>
                </div>
                <div className='grid grid-cols-1 gap-3'>
                  {NOTIFY_OPTIONS.map((option) => (
                    <FormField
                      key={option.key}
                      control={form.control}
                      name={option.key}
                      render={({ field }) => (
                        <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                          <div className='space-y-0.5 pe-4'>
                            <FormLabel className='text-base'>
                              {t(option.labelKey)}
                            </FormLabel>
                            <FormDescription>
                              {t(option.descKey)}
                            </FormDescription>
                          </div>
                          <FormControl>
                            <Switch
                              checked={field.value}
                              onCheckedChange={field.onChange}
                            />
                          </FormControl>
                        </FormItem>
                      )}
                    />
                  ))}
                </div>
              </section>

              <Separator />

              <Collapsible open={advancedOpen} onOpenChange={setAdvancedOpen}>
                <CollapsibleTrigger asChild>
                  <Button
                    type='button'
                    variant='ghost'
                    className='flex items-center gap-2 px-0'
                  >
                    <ChevronDown
                      className={cn(
                        'size-4 transition-transform',
                        advancedOpen && 'rotate-180'
                      )}
                    />
                    {t('Advanced settings')}
                  </Button>
                </CollapsibleTrigger>
                <CollapsibleContent
                  forceMount
                  className='space-y-6 data-[state=open]:pt-4'
                >
                  {ADVANCED_FIELD_GROUPS.map((group) => (
                    <div key={group.titleKey} className='space-y-3'>
                      <h5 className='text-muted-foreground text-sm font-medium'>
                        {t(group.titleKey)}
                      </h5>
                      <div className='grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3'>
                        {group.fields.map((meta) => (
                          <FormField
                            key={meta.key}
                            control={form.control}
                            name={meta.key}
                            render={({ field }) => (
                              <FormItem>
                                <FormLabel>{t(meta.labelKey)}</FormLabel>
                                <FormControl>
                                  <Input
                                    type='number'
                                    min={meta.min}
                                    max={meta.max}
                                    disabled={meta.fixed !== undefined}
                                    value={field.value}
                                    onChange={(event) =>
                                      field.onChange(event.target.valueAsNumber)
                                    }
                                  />
                                </FormControl>
                                {meta.fixed !== undefined && (
                                  <FormDescription>
                                    {t(
                                      'Must stay {{value}} for SQLite deployments.',
                                      { value: meta.fixed }
                                    )}
                                  </FormDescription>
                                )}
                                <FormMessage />
                              </FormItem>
                            )}
                          />
                        ))}
                      </div>
                    </div>
                  ))}
                </CollapsibleContent>
              </Collapsible>

              <Separator />

              <div className='flex items-center gap-3'>
                <Button
                  type='submit'
                  disabled={saveMutation.isPending || !form.formState.isDirty}
                >
                  {saveMutation.isPending
                    ? t('Saving...')
                    : t('Save configuration')}
                </Button>
                <span className='text-muted-foreground text-sm'>
                  {t('Current version: {{version}}', {
                    version: configQuery.data.config.version,
                  })}
                </span>
              </div>

              {/* 结果区 1/3：保存结果，常驻展示直到下一次提交，不依赖 toast 的短暂可见性 */}
              {saveMutation.isSuccess && (
                <Alert>
                  <CheckCircle2 />
                  <AlertTitle>{t('Saved')}</AlertTitle>
                  <AlertDescription>
                    {t('Configuration saved as version {{version}}', {
                      version: saveMutation.data.version,
                    })}
                  </AlertDescription>
                </Alert>
              )}

              {saveMutation.isError && (
                <Alert variant='destructive'>
                  <XCircle />
                  <AlertTitle>
                    {apiErrorStatus(saveMutation.error) === 409
                      ? t('Save conflict')
                      : t('Save failed')}
                  </AlertTitle>
                  <AlertDescription>
                    <p>
                      {apiErrorStatus(saveMutation.error) === 409
                        ? t(
                            'Configuration changed elsewhere. Reload before saving again.'
                          )
                        : describeApiError(saveMutation.error) ||
                          t('Failed to save content backup configuration')}
                    </p>
                    {apiErrorStatus(saveMutation.error) === 409 && (
                      <Button
                        type='button'
                        variant='outline'
                        size='sm'
                        onClick={() => configQuery.refetch()}
                      >
                        {t('Reload latest configuration')}
                      </Button>
                    )}
                  </AlertDescription>
                </Alert>
              )}
            </form>
          </Form>
        )}

        <Separator />

        {/* 结果区 2/3：节点生效状态——与「保存结果」相互独立展示 */}
        <section className='space-y-3'>
          <h4 className='text-sm font-medium'>{t('Node rollout status')}</h4>
          {nodesQuery.data && nodesQuery.data.length > 0 ? (
            <ul className='space-y-2'>
              {nodesQuery.data.map((node, index) => {
                const latestVersion = configQuery.data?.config.version
                const applied =
                  typeof node.applied_config_version === 'number' &&
                  node.applied_config_version === latestVersion
                return (
                  <li
                    key={node.storage_node_id ?? node.site_id ?? index}
                    className='flex items-center justify-between rounded-lg border p-3 text-sm'
                  >
                    <span className='font-mono'>
                      {node.storage_node_id ?? t('Unknown node')}
                    </span>
                    <Badge variant={applied ? 'secondary' : 'outline'}>
                      {applied
                        ? t('Applied v{{version}}', {
                            version: node.applied_config_version,
                          })
                        : t(
                            'Pending (node on v{{applied}}, latest v{{latest}})',
                            {
                              applied: node.applied_config_version ?? '?',
                              latest: latestVersion ?? '?',
                            }
                          )}
                    </Badge>
                  </li>
                )
              })}
            </ul>
          ) : (
            <p className='text-muted-foreground text-sm'>
              {t('No storage nodes reporting yet')}
            </p>
          )}
        </section>

        <Separator />

        {/* 结果区 3/3：连接探针——200 也可能部分失败，逐个 stage 展示自己的 ok 状态 */}
        <section className='space-y-3'>
          <h4 className='text-sm font-medium'>{t('Connection test')}</h4>
          <Button
            type='button'
            variant='outline'
            onClick={() => testMutation.mutate()}
            disabled={testMutation.isPending}
          >
            {testMutation.isPending ? t('Testing...') : t('Test connection')}
          </Button>

          {testMutation.isSuccess && (
            <div className='space-y-1 rounded-lg border p-3'>
              {testMutation.data.stages.map((stage) => (
                <div
                  key={stage.name}
                  className='flex items-center gap-2 text-sm'
                >
                  {stage.ok ? (
                    <CheckCircle2 className='size-4 shrink-0 text-emerald-600' />
                  ) : (
                    <XCircle className='text-destructive size-4 shrink-0' />
                  )}
                  <span className='font-medium'>{stage.name}</span>
                  {stage.message && (
                    <span className='text-muted-foreground'>
                      {stage.message}
                    </span>
                  )}
                </div>
              ))}
            </div>
          )}

          {testMutation.isError && (
            <Alert variant='destructive'>
              <XCircle />
              <AlertTitle>{t('Connection test failed')}</AlertTitle>
              <AlertDescription>
                {describeApiError(testMutation.error) ||
                  t('Unable to reach the storage target')}
              </AlertDescription>
            </Alert>
          )}
        </section>
      </div>
    </div>
  )
}
