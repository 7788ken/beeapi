import { useState, useMemo, useEffect, useCallback } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import { RefreshCw } from 'lucide-react'
import {
  flexRender,
  getCoreRowModel,
  useReactTable,
  getExpandedRowModel,
  type SortingState,
  type VisibilityState,
  type ExpandedState,
  type Row,
} from '@tanstack/react-table'
import { useDebounce, useMediaQuery } from '@/hooks'
import { useTranslation } from 'react-i18next'
import { getLobeIcon } from '@/lib/lobe-icon'
import { cn } from '@/lib/utils'
import { useTableUrlState } from '@/hooks/use-table-url-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  DISABLED_ROW_DESKTOP,
  DISABLED_ROW_MOBILE,
  DataTableToolbar,
  TableSkeleton,
  TableEmpty,
  MobileCardList,
} from '@/components/data-table'
import { DataTablePagination } from '@/components/data-table/pagination'
import { PageFooterPortal } from '@/components/layout'
import { getChannels, searchChannels, getGroups } from '../api'
import {
  DEFAULT_PAGE_SIZE,
  CHANNEL_STATUS,
  CHANNEL_STATUS_OPTIONS,
} from '../constants'
import {
  channelsQueryKeys,
  aggregateChannelsByTag,
  isTagAggregateRow,
  getChannelTypeIcon,
  getChannelTypeLabel,
} from '../lib'
import type { Channel } from '../types'
import { ChannelGroupPricingDialog } from './channel-group-pricing-dialog'
import { useChannelsColumns } from './channels-columns'
import { useChannels } from './channels-provider'
import { DataTableBulkActions } from './data-table-bulk-actions'

const route = getRouteApi('/_authenticated/channels/')

// 后端 ORDER BY 字段白名单：列 id → GetAllChannels 的 order_by 值。
// rpm_24h 走 Go 层按 Redis 实时值排序；balance 列（「已使用 / 剩余」）按 used_quota 排，
// 与单元格左侧「已使用」数字同源。不在表内的列点击列头只改前端 sort 状态，不带到 API。
const BACKEND_SORTABLE_CHANNEL_COLUMNS: Record<string, string> = {
  rpm_24h: 'rpm_24h',
  balance: 'used_quota',
}

function isDisabledChannelRow(channel: Channel) {
  return (
    !isTagAggregateRow(channel) && channel.status !== CHANNEL_STATUS.ENABLED
  )
}

export function ChannelsTable() {
  const { t } = useTranslation()
  const { enableTagMode, idSort } = useChannels()
  const isMobile = useMediaQuery('(max-width: 640px)')
  const queryClient = useQueryClient()

  // 刷新按钮：invalidate 渠道列表 query，立即重拉当前页（含 Redis 实时 RPM 覆盖值）。
  const handleRefresh = useCallback(() => {
    queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
  }, [queryClient])

  // Table state
  const [sorting, setSorting] = useState<SortingState>([])
  const [columnVisibility, setColumnVisibility] = useState<VisibilityState>({
    models: false,
    tag: false,
    // 渠道健康度列默认显示：本部署常开 channel_health_setting.enabled，运营需要快速看到 L0/L1/L2/Disabled/Locked 状态。
    // 没开被动开关的部署所有渠道恒 L0，可在 column visibility 菜单手动隐藏。
    degrade_level: true,
  })
  const [rowSelection, setRowSelection] = useState({})
  const [expanded, setExpanded] = useState<ExpandedState>({})

  // URL state management
  const {
    globalFilter,
    onGlobalFilterChange,
    columnFilters,
    onColumnFiltersChange,
    pagination,
    onPaginationChange,
    ensurePageInRange,
  } = useTableUrlState({
    search: route.useSearch(),
    navigate: route.useNavigate(),
    pagination: {
      defaultPage: 1,
      defaultPageSize: isMobile ? 10 : DEFAULT_PAGE_SIZE,
    },
    globalFilter: { enabled: true, key: 'filter' },
    columnFilters: [
      { columnId: 'status', searchKey: 'status', type: 'array' },
      { columnId: 'type', searchKey: 'type', type: 'array' },
      { columnId: 'group', searchKey: 'group', type: 'array' },
      { columnId: 'model', searchKey: 'model', type: 'string' },
    ],
  })

  // Extract filters from column filters
  const statusFilter =
    (columnFilters.find((f) => f.id === 'status')?.value as string[]) || []
  const typeFilter =
    (columnFilters.find((f) => f.id === 'type')?.value as string[]) || []
  const groupFilter =
    (columnFilters.find((f) => f.id === 'group')?.value as string[]) || []
  const modelFilterFromUrl =
    (columnFilters.find((f) => f.id === 'model')?.value as string) || ''

  // Local state for immediate input feedback
  const [modelFilterInput, setModelFilterInput] = useState(modelFilterFromUrl)
  const debouncedModelFilter = useDebounce(modelFilterInput, 500)

  // Sync local input with URL when URL changes (e.g., from back/forward navigation)
  useEffect(() => {
    setModelFilterInput(modelFilterFromUrl)
  }, [modelFilterFromUrl])

  // Update URL when debounced value changes
  useEffect(() => {
    if (debouncedModelFilter !== modelFilterFromUrl) {
      onColumnFiltersChange((prev) => {
        const filtered = prev.filter((f) => f.id !== 'model')
        return debouncedModelFilter
          ? [...filtered, { id: 'model', value: debouncedModelFilter }]
          : filtered
      })
    }
  }, [debouncedModelFilter, modelFilterFromUrl, onColumnFiltersChange])

  const modelFilter = modelFilterFromUrl

  // Determine whether to use search or regular list API
  // /api/channel/ (GetAllChannels) only supports status/type filters; group filtering
  // is exclusive to /api/channel/search (SearchChannels). Route any non-empty filter
  // through the search endpoint so URL-driven filters (e.g. deep link from usage logs)
  // actually narrow the result set.
  const shouldSearch = Boolean(
    globalFilter?.trim() ||
      modelFilter.trim() ||
      (groupFilter.length > 0 && !groupFilter.includes('all')) ||
      (statusFilter.length > 0 && !statusFilter.includes('all')) ||
      (typeFilter.length > 0 && !typeFilter.includes('all'))
  )

  // 把 tanstack sorting 翻译成后端 order_by / order；仅白名单字段生效。
  // search 端与 tag_mode 路径不支持 ORDER BY，这两种模式下不带排序参数。
  const firstSort = sorting[0]
  const backendOrderBy =
    firstSort && !shouldSearch && !enableTagMode
      ? BACKEND_SORTABLE_CHANNEL_COLUMNS[firstSort.id]
      : undefined
  const backendOrder: 'asc' | 'desc' | undefined = backendOrderBy
    ? firstSort?.desc
      ? 'desc'
      : 'asc'
    : undefined

  // Fetch groups for filter
  const { data: groupsData } = useQuery({
    queryKey: ['groups'],
    queryFn: getGroups,
  })

  const groupOptions = useMemo(
    () =>
      (groupsData?.data || []).map((g) => ({
        label: g,
        value: g,
      })),
    [groupsData]
  )

  // Fetch channels data
  // eslint-disable-next-line @tanstack/query/exhaustive-deps
  const { data, isLoading, isFetching } = useQuery({
    queryKey: channelsQueryKeys.list({
      keyword: globalFilter,
      model: modelFilter,
      group:
        groupFilter.length > 0 && !groupFilter.includes('all')
          ? groupFilter[0]
          : undefined,
      status:
        statusFilter.length > 0 && !statusFilter.includes('all')
          ? statusFilter[0]
          : undefined,
      type:
        typeFilter.length > 0 && !typeFilter.includes('all')
          ? Number(typeFilter[0])
          : undefined,
      tag_mode: enableTagMode,
      id_sort: idSort,
      order_by: backendOrderBy,
      order: backendOrder,
      p: pagination.pageIndex + 1,
      page_size: pagination.pageSize,
    }),
    queryFn: async () => {
      if (shouldSearch) {
        return searchChannels({
          keyword: globalFilter,
          model: modelFilter,
          group:
            groupFilter.length > 0 && !groupFilter.includes('all')
              ? groupFilter[0]
              : undefined,
          status:
            statusFilter.length > 0 && !statusFilter.includes('all')
              ? statusFilter[0]
              : undefined,
          type:
            typeFilter.length > 0 && !typeFilter.includes('all')
              ? Number(typeFilter[0])
              : undefined,
          tag_mode: enableTagMode,
          id_sort: idSort,
          p: pagination.pageIndex + 1,
          page_size: pagination.pageSize,
        })
      } else {
        return getChannels({
          group:
            groupFilter.length > 0 && !groupFilter.includes('all')
              ? groupFilter[0]
              : undefined,
          status:
            statusFilter.length > 0 && !statusFilter.includes('all')
              ? statusFilter[0]
              : undefined,
          type:
            typeFilter.length > 0 && !typeFilter.includes('all')
              ? Number(typeFilter[0])
              : undefined,
          tag_mode: enableTagMode,
          id_sort: idSort,
          order_by: backendOrderBy,
          order: backendOrder,
          p: pagination.pageIndex + 1,
          page_size: pagination.pageSize,
        })
      }
    },
    placeholderData: (previousData) => previousData,
  })

  // Apply tag aggregation if tag mode is enabled
  const channels = useMemo(() => {
    const rawChannels = data?.data?.items || []

    if (enableTagMode && rawChannels.length > 0) {
      return aggregateChannelsByTag(rawChannels)
    }

    return rawChannels
  }, [data, enableTagMode])

  const totalCount = data?.data?.total || 0
  const typeCounts = data?.data?.type_counts

  // Columns configuration
  const columns = useChannelsColumns()

  // React Table instance
  const table = useReactTable({
    data: channels,
    columns,
    pageCount: Math.ceil(totalCount / pagination.pageSize),
    state: {
      sorting,
      columnFilters,
      columnVisibility,
      rowSelection,
      pagination,
      expanded,
      globalFilter,
    },
    enableRowSelection: (row: Row<Channel>) => !isTagAggregateRow(row.original),
    onRowSelectionChange: setRowSelection,
    onSortingChange: setSorting,
    onColumnFiltersChange,
    onColumnVisibilityChange: setColumnVisibility,
    onPaginationChange,
    onExpandedChange: setExpanded,
    onGlobalFilterChange,
    getCoreRowModel: getCoreRowModel(),
    getExpandedRowModel: getExpandedRowModel(),
    getSubRows: (row: Channel & { children?: Channel[] }) => row.children,
    manualPagination: true,
    manualSorting: true,
    manualFiltering: true,
  })

  // Ensure page is in range when total count changes
  const pageCount = table.getPageCount()
  useEffect(() => {
    ensurePageInRange(pageCount)
  }, [pageCount, ensurePageInRange])

  // Prepare filter options from existing channel types only.
  const typeFilterOptions = useMemo(() => {
    const counts = typeCounts || {}
    const typeIds = Object.entries(counts)
      .map(([type, count]) => ({
        type: Number(type),
        count: Number(count) || 0,
      }))
      .filter((item) => item.type > 0 && item.count > 0)
      .sort((a, b) => {
        const labelA = t(getChannelTypeLabel(a.type))
        const labelB = t(getChannelTypeLabel(b.type))
        return labelA.localeCompare(labelB)
      })

    const selectedType = typeFilter.find((value) => value !== 'all')
    if (selectedType) {
      const selectedTypeId = Number(selectedType)
      const alreadyIncluded = typeIds.some(
        (item) => item.type === selectedTypeId
      )
      if (selectedTypeId > 0 && !alreadyIncluded) {
        typeIds.push({
          type: selectedTypeId,
          count: Number(counts[selectedType]) || 0,
        })
      }
    }

    const totalTypes = Object.values(counts).reduce(
      (sum, count) => sum + (Number(count) || 0),
      0
    )

    return [
      {
        label: 'All Types',
        value: 'all',
        count: totalTypes,
      },
      ...typeIds.map((item) => {
        const iconName = getChannelTypeIcon(item.type)
        return {
          label: getChannelTypeLabel(item.type),
          value: String(item.type),
          count: item.count,
          iconNode: getLobeIcon(`${iconName}.Color`, 16),
        }
      }),
    ]
  }, [t, typeCounts, typeFilter])

  const groupFilterOptions = [
    { label: t('All Groups'), value: 'all' },
    ...groupOptions,
  ]

  return (
    <>
      <div className='space-y-3 sm:space-y-4'>
        <div className='flex items-start gap-2'>
          <div className='min-w-0 flex-1'>
            <DataTableToolbar
              table={table}
              searchPlaceholder={t('Filter by name, ID, or key...')}
              additionalSearch={
                <Input
                  placeholder={t('Filter by model...')}
                  value={modelFilterInput}
                  onChange={(e) => setModelFilterInput(e.target.value)}
                  className='h-8 w-full sm:w-[150px] lg:w-[200px]'
                />
              }
              filters={[
                {
                  columnId: 'status',
                  title: t('Status'),
                  options: [...CHANNEL_STATUS_OPTIONS],
                  singleSelect: true,
                },
                {
                  columnId: 'type',
                  title: t('Type'),
                  options: typeFilterOptions,
                  singleSelect: true,
                },
                {
                  columnId: 'group',
                  title: t('Group'),
                  options: groupFilterOptions,
                  singleSelect: true,
                },
              ]}
              extraActions={
                groupFilter.length > 0 && !groupFilter.includes('all') ? (
                  <ChannelGroupPricingDialog group={groupFilter[0]} />
                ) : null
              }
            />
          </div>
          <Button
            variant='outline'
            size='sm'
            onClick={handleRefresh}
            disabled={isFetching}
            aria-label={t('Refresh')}
            title={t('Refresh channel list')}
          >
            <RefreshCw
              className={cn('h-4 w-4', isFetching && 'animate-spin')}
            />
            <span className='hidden sm:inline ml-1.5'>{t('Refresh')}</span>
          </Button>
        </div>

        {isMobile ? (
          <MobileCardList
            table={table}
            isLoading={isLoading}
            emptyTitle='No Channels Found'
            emptyDescription='No channels available. Create your first channel to get started.'
            getRowClassName={(row) =>
              isDisabledChannelRow(row.original) ? DISABLED_ROW_MOBILE : undefined
            }
          />
        ) : (
          <>
            <div
              className={cn(
                'overflow-hidden rounded-md border transition-opacity duration-150',
                isFetching && !isLoading && 'pointer-events-none opacity-50'
              )}
            >
              <Table>
                <TableHeader>
                  {table.getHeaderGroups().map((headerGroup) => (
                    <TableRow key={headerGroup.id}>
                      {headerGroup.headers.map((header) => {
                        const isActions = header.column.id === 'actions'
                        return (
                          <TableHead
                            key={header.id}
                            style={{ width: header.getSize() }}
                            // actions 列固定在右侧，横向滚动时不被其它列推走
                            className={cn(
                              isActions &&
                                'sticky right-0 z-20 border-l bg-background'
                            )}
                          >
                            {header.isPlaceholder
                              ? null
                              : flexRender(
                                  header.column.columnDef.header,
                                  header.getContext()
                                )}
                          </TableHead>
                        )
                      })}
                    </TableRow>
                  ))}
                </TableHeader>
                <TableBody>
                  {isLoading ? (
                    <TableSkeleton table={table} keyPrefix='channel-skeleton' />
                  ) : table.getRowModel().rows.length === 0 ? (
                    <TableEmpty
                      colSpan={columns.length}
                      title={t('No Channels Found')}
                      description={t(
                        'No channels available. Create your first channel to get started.'
                      )}
                    />
                  ) : (
                    table.getRowModel().rows.map((row) => (
                      <TableRow
                        key={row.id}
                        data-state={row.getIsSelected() && 'selected'}
                        // group 让 sticky cell 能跟随行 hover 染色
                        className={cn(
                          'group',
                          isDisabledChannelRow(row.original) &&
                            DISABLED_ROW_DESKTOP
                        )}
                      >
                        {row.getVisibleCells().map((cell) => {
                          const isActions = cell.column.id === 'actions'
                          return (
                            <TableCell
                              key={cell.id}
                              className={cn(
                                isActions &&
                                  'sticky right-0 z-10 border-l bg-background group-hover:bg-muted/50 group-data-[state=selected]:bg-muted'
                              )}
                            >
                              {flexRender(
                                cell.column.columnDef.cell,
                                cell.getContext()
                              )}
                            </TableCell>
                          )
                        })}
                      </TableRow>
                    ))
                  )}
                </TableBody>
              </Table>
            </div>

            <DataTableBulkActions table={table} />
          </>
        )}
      </div>
      <PageFooterPortal>
        <DataTablePagination table={table} />
      </PageFooterPortal>
    </>
  )
}
