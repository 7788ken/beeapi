import { useState, useEffect, useCallback, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { SectionPageLayout } from '@/components/layout'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Combobox, type ComboboxOption } from '@/components/ui/combobox'
import { AnomalyTable } from './components/anomaly-table'
import { fetchAnomalyStats, fetchChannelOptions, type AnomalyStats, type ChannelOption } from './api'

type TimeRange = 'today' | 'yesterday' | '7d' | 'custom'

function getTimeRange(range: TimeRange): { start: number; end: number } {
  const now = Math.floor(Date.now() / 1000)
  const todayStart = now - (now % 86400) - new Date().getTimezoneOffset() * 60
  switch (range) {
    case 'today':
      return { start: todayStart, end: now }
    case 'yesterday':
      return { start: todayStart - 86400, end: todayStart }
    case '7d':
      return { start: now - 7 * 86400, end: now }
    default:
      return { start: now - 86400, end: now }
  }
}

function toLocalDatetime(ts: number): string {
  const d = new Date(ts * 1000)
  const pad = (n: number) => n.toString().padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function fromLocalDatetime(str: string): number {
  return Math.floor(new Date(str).getTime() / 1000)
}

export function AnomalyMonitor() {
  const { t } = useTranslation()
  const [stats, setStats] = useState<AnomalyStats | null>(null)
  const [timeRange, setTimeRange] = useState<TimeRange>('today')
  const [timeParams, setTimeParams] = useState(getTimeRange('today'))
  const [customStart, setCustomStart] = useState('')
  const [customEnd, setCustomEnd] = useState('')
  const [username, setUsername] = useState('')
  const [modelName, setModelName] = useState('')
  const [channelId, setChannelId] = useState(0)
  const [searchUsername, setSearchUsername] = useState('')
  const [searchModelName, setSearchModelName] = useState('')
  const [searchChannelId, setSearchChannelId] = useState(0)
  const [channels, setChannels] = useState<ChannelOption[]>([])

  useEffect(() => {
    fetchChannelOptions().then(setChannels)
  }, [])

  const channelOptions = useMemo<ComboboxOption[]>(() => {
    return [
      { value: '0', label: t('All Channels') },
      ...channels.map((ch) => ({
        value: String(ch.id),
        // 同时把 id 拼进 label，方便 Combobox 内部按 label+value 模糊搜索
        label: `#${ch.id} ${ch.name}`,
      })),
    ]
  }, [channels, t])

  const handleTimeChange = useCallback((range: TimeRange) => {
    setTimeRange(range)
    setTimeParams(getTimeRange(range))
  }, [])

  const handleCustomApply = useCallback(() => {
    if (customStart && customEnd) {
      const start = fromLocalDatetime(customStart)
      const end = fromLocalDatetime(customEnd)
      if (start < end) {
        setTimeRange('custom')
        setTimeParams({ start, end })
      }
    }
  }, [customStart, customEnd])

  const handleSearch = useCallback(() => {
    setSearchUsername(username)
    setSearchModelName(modelName)
    setSearchChannelId(channelId)
  }, [username, modelName, channelId])

  useEffect(() => {
    fetchAnomalyStats({
      start_timestamp: timeParams.start,
      end_timestamp: timeParams.end,
    }).then(res => {
      if (res.success) setStats(res.data)
    })
  }, [timeParams])

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Anomaly Monitor')}</SectionPageLayout.Title>
      <SectionPageLayout.Description>
        {t('Monitor abnormal billing, client disconnections, and channel health events.')}
      </SectionPageLayout.Description>
      <SectionPageLayout.Content>
        <div className="space-y-4">
          {/* Time selector */}
          <div className="flex flex-wrap items-center gap-2">
            <Button
              variant={timeRange === 'today' ? 'default' : 'outline'}
              size="sm"
              onClick={() => handleTimeChange('today')}
            >
              {t('Today')}
            </Button>
            <Button
              variant={timeRange === 'yesterday' ? 'default' : 'outline'}
              size="sm"
              onClick={() => handleTimeChange('yesterday')}
            >
              {t('Yesterday')}
            </Button>
            <Button
              variant={timeRange === '7d' ? 'default' : 'outline'}
              size="sm"
              onClick={() => handleTimeChange('7d')}
            >
              {t('Last 7 days')}
            </Button>
            <div className="flex items-center gap-1 ml-2">
              <input
                type="datetime-local"
                className="h-8 rounded-md border border-input bg-background px-2 text-xs"
                value={customStart || toLocalDatetime(timeParams.start)}
                onChange={e => setCustomStart(e.target.value)}
              />
              <span className="text-xs text-muted-foreground">—</span>
              <input
                type="datetime-local"
                className="h-8 rounded-md border border-input bg-background px-2 text-xs"
                value={customEnd || toLocalDatetime(timeParams.end)}
                onChange={e => setCustomEnd(e.target.value)}
              />
              <Button size="sm" variant="outline" onClick={handleCustomApply}>
                {t('Apply')}
              </Button>
            </div>
          </div>

          {/* Search filters */}
          <div className="flex flex-wrap items-center gap-2">
            <Input
              className="h-8 w-40 text-sm"
              placeholder={t('Search username')}
              value={username}
              onChange={e => setUsername(e.target.value)}
              onKeyDown={e => e.key === 'Enter' && handleSearch()}
            />
            <Input
              className="h-8 w-40 text-sm"
              placeholder={t('Search model')}
              value={modelName}
              onChange={e => setModelName(e.target.value)}
              onKeyDown={e => e.key === 'Enter' && handleSearch()}
            />
            <Combobox
              className="h-8 w-56 text-sm"
              options={channelOptions}
              value={String(channelId)}
              onValueChange={(v) => setChannelId(Number(v) || 0)}
              placeholder={t('All Channels')}
              searchPlaceholder={t('Search channel by id/name')}
              emptyText={t('No channel found')}
            />
            <Button size="sm" onClick={handleSearch}>
              {t('Search')}
            </Button>
          </div>

          {/* Stats cards - compact row */}
          {stats && (
            <div className="grid grid-cols-3 gap-3 sm:grid-cols-4 lg:grid-cols-7">
              <CompactStatCard title={t('Total Count')} value={stats.total_count} />
              <CompactStatCard title={t('Total Quota')} value={`$${(stats.total_quota / 500000).toFixed(2)}`} />
              <CompactStatCard title={t('High Cost')} value={stats.high_cost} variant="red" />
              <CompactStatCard title={t('Client Disconnect')} value={stats.client_disconnect === -1 ? '—' : stats.client_disconnect} variant="yellow" />
              <CompactStatCard title={t('Channel Demote')} value={stats.channel_demote} variant="orange" />
              <CompactStatCard title={t('Channel Disable')} value={stats.channel_disable} variant="red" />
              <CompactStatCard title={t('Channel Upgrade')} value={stats.channel_upgrade} variant="green" />
            </div>
          )}

          <Tabs defaultValue="channel_health">
            <TabsList>
              <TabsTrigger value="channel_health">{t('Channel Health')}</TabsTrigger>
              <TabsTrigger value="client_disconnect">{t('Client Disconnect')}</TabsTrigger>
              <TabsTrigger value="high_cost">{t('High Cost')}</TabsTrigger>
              <TabsTrigger value="no_output_refund">{t('No Output Refund')}</TabsTrigger>
            </TabsList>
            <TabsContent value="channel_health" className="mt-3">
              <AnomalyTable type="channel_health" timeParams={timeParams} username={searchUsername} modelName={searchModelName} channelId={searchChannelId} />
            </TabsContent>
            <TabsContent value="client_disconnect" className="mt-3">
              <AnomalyTable type="client_disconnect" timeParams={timeParams} username={searchUsername} modelName={searchModelName} channelId={searchChannelId} />
            </TabsContent>
            <TabsContent value="high_cost" className="mt-3">
              <AnomalyTable type="high_cost" timeParams={timeParams} username={searchUsername} modelName={searchModelName} channelId={searchChannelId} />
            </TabsContent>
            <TabsContent value="no_output_refund" className="mt-3">
              <AnomalyTable type="no_output_refund" timeParams={timeParams} username={searchUsername} modelName={searchModelName} channelId={searchChannelId} />
            </TabsContent>
          </Tabs>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

interface CompactStatCardProps {
  title: string
  value: number | string
  variant?: 'red' | 'yellow' | 'orange' | 'green'
}

const variantColors: Record<string, string> = {
  red: 'text-red-600',
  yellow: 'text-yellow-600',
  orange: 'text-orange-600',
  green: 'text-green-600',
}

function CompactStatCard({ title, value, variant }: CompactStatCardProps) {
  return (
    <Card className="p-0">
      <CardContent className="px-3 py-2">
        <p className="text-xs text-muted-foreground truncate">{title}</p>
        <p className={`text-lg font-semibold ${variant ? variantColors[variant] : ''}`}>
          {value}
        </p>
      </CardContent>
    </Card>
  )
}
