import { useState } from 'react'
import { Download } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { exportLogsCsv } from '../api'
import { buildApiParams } from '../lib/utils'

interface ExportLogsButtonProps {
  isAdmin: boolean
  searchParams: Record<string, unknown>
  columnFilters: Array<{ id: string; value: unknown }>
}

export function ExportLogsButton({
  isAdmin,
  searchParams,
  columnFilters,
}: ExportLogsButtonProps) {
  const { t } = useTranslation()
  const [exporting, setExporting] = useState(false)

  const handleExport = async () => {
    setExporting(true)
    try {
      const params = buildApiParams({
        page: 1,
        pageSize: 20,
        searchParams,
        columnFilters,
        isAdmin,
      })
      await exportLogsCsv(params, isAdmin)
    } catch (e) {
      // HTTP 层错误(401/403/500)已由 axios 拦截器统一 toast（并处理 401 鉴权重置），
      // 此处只弹自抛的业务错误（如超行数上限），避免重复弹窗
      const isAxiosError = !!(
        e &&
        typeof e === 'object' &&
        (e as { isAxiosError?: boolean }).isAxiosError
      )
      if (!isAxiosError) {
        toast.error(e instanceof Error ? e.message : t('Export failed'))
      }
    } finally {
      setExporting(false)
    }
  }

  return (
    <Button
      variant='outline'
      size='sm'
      onClick={handleExport}
      disabled={exporting}
    >
      <Download className='h-4 w-4' />
      {exporting ? t('Exporting...') : t('Export CSV')}
    </Button>
  )
}
