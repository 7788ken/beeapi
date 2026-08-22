import { useState, useEffect, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { Download } from 'lucide-react'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { fetchAnomalyLogs, fetchAnomalySummary, getExportUrl, type AnomalyLog, type AnomalySummary } from '../api'
import { api } from '@/lib/api'

const TYPE_COLORS: Record<string, string> = {
  client_disconnect: 'bg-yellow-100 text-yellow-800',
  high_cost: 'bg-red-100 text-red-800',
  no_output_refund: 'bg-blue-100 text-blue-800',
  channel_demote: 'bg-orange-100 text-orange-800',
  channel_disable: 'bg-red-100 text-red-800',
  channel_upgrade: 'bg-green-100 text-green-800',
  channel_enable: 'bg-green-100 text-green-800',
}

interface Props {
  type: string
  timeParams: { start: number; end: number }
  username?: string
  modelName?: string
  channelId?: number
}

type SortDir = 'asc' | 'desc' | null

export function AnomalyTable({ type, timeParams, username, modelName, channelId }: Props) {
  const { t } = useTranslation()
  const [logs, setLogs] = useState<AnomalyLog[]>([])
  const [total, setTotal] = useState(0)
  const [pageQuotaSum, setPageQuotaSum] = useState(0)
  const [summary, setSummary] = useState<AnomalySummary | null>(null)
  const [summaryLoading, setSummaryLoading] = useState(false)
  const [page, setPage] = useState(1)
  const [loading, setLoading] = useState(false)
  const [costSort, setCostSort] = useState<SortDir>(null)
  const pageSize = 20

  const queryParams = useMemo(() => ({
    type,
    start_timestamp: timeParams.start,
    end_timestamp: timeParams.end,
    username: username || undefined,
    model_name: modelName || undefined,
    channel_id: channelId || undefined,
  }), [type, timeParams, username, modelName, channelId])

  useEffect(() => {
    setPage(1)
    setCostSort(null)
    setSummary(null)
  }, [type, timeParams, username, modelName, channelId])

  useEffect(() => {
    setLoading(true)
    fetchAnomalyLogs({
      ...queryParams,
      p: page,
      page_size: pageSize,
    }).then(res => {
      if (res.success) {
        setLogs(res.data || [])
        setTotal(res.total)
        setPageQuotaSum(res.page_quota_sum || 0)
      }
    }).finally(() => setLoading(false))
  }, [queryParams, page])

  // 异步加载全量统计
  useEffect(() => {
    setSummaryLoading(true)
    fetchAnomalySummary(queryParams).then(res => {
      if (res.success) setSummary(res.data)
    }).finally(() => setSummaryLoading(false))
  }, [queryParams])

  const sortedLogs = useMemo(() => {
    if (!costSort) return logs
    return [...logs].sort((a, b) =>
      costSort === 'asc' ? a.quota - b.quota : b.quota - a.quota
    )
  }, [logs, costSort])

  const toggleCostSort = () => {
    setCostSort(prev => {
      if (prev === null) return 'desc'
      if (prev === 'desc') return 'asc'
      return null
    })
  }

  const totalPages = summary ? Math.ceil(summary.total_count / pageSize) : Math.ceil(total / pageSize)
  const displayTotal = summary ? summary.total_count : total

  const handleExport = async () => {
    try {
      const res = await api.get(getExportUrl(queryParams), { responseType: 'blob' })
      const blob = new Blob([res.data as BlobPart])
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `anomaly_${type}.csv`
      a.click()
      URL.revokeObjectURL(url)
    } catch (e) {
      console.error('Export failed', e)
    }
  }

  return (
    <div className="space-y-3">
      {/* Summary bar */}
      <div className="flex items-center justify-between text-sm">
        <div className="flex items-center gap-4 text-muted-foreground">
          {summaryLoading ? (
            <span>{t('Loading...')}</span>
          ) : summary ? (
            <>
              <span>{t('Total')}: <strong className="text-foreground">{summary.total_count}</strong> {t('records')}</span>
              {summary.total_quota > 0 && (
                <span>{t('Total Quota')}: <strong className="text-foreground">${(summary.total_quota / 500000).toFixed(2)}</strong></span>
              )}
              {pageQuotaSum > 0 && (
                <span>{t('This page')}: ${(pageQuotaSum / 500000).toFixed(4)}</span>
              )}
            </>
          ) : null}
        </div>
        <Button variant="outline" size="sm" onClick={handleExport}>
          <Download className="size-3.5 mr-1" />
          {t('Export CSV')}
        </Button>
      </div>

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-[140px]">{t('Time')}</TableHead>
              <TableHead className="w-[120px]">{t('Type')}</TableHead>
              <TableHead>{t('Detail')}</TableHead>
              <TableHead className="w-[100px]">{t('User')}</TableHead>
              <TableHead className="w-[100px]">{t('Model')}</TableHead>
              <TableHead className="w-[90px]">{t('Channel')}</TableHead>
              <TableHead className="w-[90px] cursor-pointer select-none" onClick={toggleCostSort}>
                {t('Cost')} {costSort === 'desc' ? '↓' : costSort === 'asc' ? '↑' : ''}
              </TableHead>
              <TableHead className="w-[180px]">{t('Request ID')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              <TableRow>
                <TableCell colSpan={8} className="text-center py-8 text-muted-foreground">
                  {t('Loading...')}
                </TableCell>
              </TableRow>
            ) : sortedLogs.length === 0 ? (
              <TableRow>
                <TableCell colSpan={8} className="text-center py-8 text-muted-foreground">
                  {t('No anomalies found')}
                </TableCell>
              </TableRow>
            ) : (
              sortedLogs.map((log) => (
                <TableRow key={`${log.anomaly_type}-${log.id}`}>
                  <TableCell className="text-xs text-muted-foreground">
                    {new Date(log.created_at * 1000).toLocaleString()}
                  </TableCell>
                  <TableCell>
                    <Badge variant="secondary" className={TYPE_COLORS[log.anomaly_type] || ''}>
                      {log.anomaly_type}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-sm max-w-[400px] truncate" title={log.anomaly_detail}>
                    {log.anomaly_detail}
                  </TableCell>
                  <TableCell className="text-sm">
                    {log.username || log.user_id || '-'}
                  </TableCell>
                  <TableCell className="text-sm">
                    {log.model_name || '-'}
                  </TableCell>
                  <TableCell className="text-sm text-muted-foreground">
                    {log.channel_id > 0 ? `#${log.channel_id}` : '-'}
                  </TableCell>
                  <TableCell className="text-sm">
                    {log.quota > 0 ? `$${(log.quota / 500000).toFixed(4)}` : '-'}
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground font-mono truncate" title={log.request_id}>
                    {log.request_id || '-'}
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>

      {/* Pagination */}
      <div className="flex items-center justify-between">
        <span className="text-sm text-muted-foreground">
          {displayTotal > 0 && `${(page - 1) * pageSize + 1}-${Math.min(page * pageSize, displayTotal)} / ${displayTotal}`}
        </span>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" disabled={page <= 1} onClick={() => setPage(p => p - 1)}>
            {t('Previous')}
          </Button>
          <span className="flex items-center text-sm">{page} / {totalPages || 1}</span>
          <Button variant="outline" size="sm" disabled={page >= totalPages} onClick={() => setPage(p => p + 1)}>
            {t('Next')}
          </Button>
        </div>
      </div>
    </div>
  )
}
