import { useEffect, useState, useCallback } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import { RefreshCw } from 'lucide-react'
import {
  type SortingState,
  type VisibilityState,
  flexRender,
  getCoreRowModel,
  getFacetedRowModel,
  getFacetedUniqueValues,
  getFilteredRowModel,
  getPaginationRowModel,
  getSortedRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { useMediaQuery } from '@/hooks'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { useTableUrlState } from '@/hooks/use-table-url-state'
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
  DataTablePagination,
  DataTableToolbar,
  TableSkeleton,
  TableEmpty,
  MobileCardList,
} from '@/components/data-table'
import { PageFooterPortal } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { getUsers, searchUsers, recomputeUserMetrics } from '../api'
import {
  USER_STATUS,
  getUserStatusOptions,
  getUserRoleOptions,
  isUserDeleted,
} from '../constants'
import type { User } from '../types'
import { DataTableBulkActions } from './data-table-bulk-actions'
import { useUsersColumns } from './users-columns'
import { useUsers } from './users-provider'

const route = getRouteApi('/_authenticated/users/')

// 后端 ORDER BY 字段白名单（必须和 model.allowedUserOrderColumns 一致）。
// 前端把 tanstack sorting 状态翻译成 (order_by, order) 时只对白名单字段生效；
// 其他列点击列头视作纯前端 sort 状态变化但不带到 API（保持当前页数据）。
const BACKEND_SORTABLE_USER_COLUMNS = new Set([
  'id',
  'created_at',
  'last_login_at',
  'used_quota',
  'request_count',
  'rpm_24h',
  'quota',
])

function isDisabledUserRow(user: User) {
  return isUserDeleted(user) || user.status === USER_STATUS.DISABLED
}

export function UsersTable() {
  const { t } = useTranslation()
  const columns = useUsersColumns()
  const { refreshTrigger, triggerRefresh } = useUsers()
  const queryClient = useQueryClient()
  const isMobile = useMediaQuery('(max-width: 640px)')
  const [rowSelection, setRowSelection] = useState({})
  const [sorting, setSorting] = useState<SortingState>([])
  const [columnVisibility, setColumnVisibility] = useState<VisibilityState>({})

  // 把 tanstack sorting 翻译成后端 order_by / order；只对白名单字段生效。
  const firstSort = sorting[0]
  const backendOrderBy =
    firstSort && BACKEND_SORTABLE_USER_COLUMNS.has(firstSort.id)
      ? firstSort.id
      : undefined
  const backendOrder = firstSort?.desc ? 'desc' : 'asc'

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
    pagination: { defaultPage: 1, defaultPageSize: isMobile ? 10 : 20 },
    globalFilter: { enabled: true, key: 'filter' },
    columnFilters: [
      { columnId: 'status', searchKey: 'status', type: 'array' },
      { columnId: 'role', searchKey: 'role', type: 'array' },
      { columnId: 'group', searchKey: 'group', type: 'string' },
    ],
  })

  // Fetch data with React Query
  const { data, isLoading, isFetching } = useQuery({
    queryKey: [
      'users',
      pagination.pageIndex + 1,
      pagination.pageSize,
      globalFilter,
      backendOrderBy,
      backendOrder,
      refreshTrigger,
    ],
    queryFn: async () => {
      const hasFilter = globalFilter?.trim()
      const params: {
        p: number
        page_size: number
        order_by?: string
        order?: 'asc' | 'desc'
      } = {
        p: pagination.pageIndex + 1,
        page_size: pagination.pageSize,
      }
      // search 端目前不支持 ORDER BY；只在普通 list 路径带过去
      if (backendOrderBy && !hasFilter) {
        params.order_by = backendOrderBy
        params.order = backendOrder
      }

      const result = hasFilter
        ? await searchUsers({ ...params, keyword: globalFilter })
        : await getUsers(params)

      if (!result.success) {
        toast.error(
          result.message || `Failed to ${hasFilter ? 'search' : 'load'} users`
        )
        return { items: [], total: 0 }
      }

      return {
        items: result.data?.items || [],
        total: result.data?.total || 0,
      }
    },
    placeholderData: (previousData) => previousData,
  })

  const users = data?.items || []

  const table = useReactTable({
    data: users,
    columns,
    state: {
      sorting,
      columnVisibility,
      rowSelection,
      columnFilters,
      globalFilter,
      pagination,
    },
    enableRowSelection: true,
    onRowSelectionChange: setRowSelection,
    onSortingChange: setSorting,
    onColumnVisibilityChange: setColumnVisibility,
    globalFilterFn: (row, _columnId, filterValue) => {
      const searchValue = String(filterValue).toLowerCase()
      const fields = [
        row.getValue('username'),
        row.original.display_name,
        row.original.email,
      ]
      return fields.some((field) =>
        String(field || '')
          .toLowerCase()
          .includes(searchValue)
      )
    },
    getCoreRowModel: getCoreRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getFacetedRowModel: getFacetedRowModel(),
    getFacetedUniqueValues: getFacetedUniqueValues(),
    onPaginationChange,
    onGlobalFilterChange,
    onColumnFiltersChange,
    manualPagination: !globalFilter,
    // 后端 ORDER BY：白名单字段的排序由后端 SQL 完成；前端不再二次排序当前页。
    manualSorting: !!backendOrderBy,
    pageCount: Math.ceil((data?.total || 0) / pagination.pageSize),
  })

  // 刷新按钮：调用 admin 重算接口 + 立刻 invalidate 列表 query 触发重拉。
  const handleRefresh = useCallback(async () => {
    try {
      await recomputeUserMetrics()
    } catch {
      // 静默：重算是 best-effort，列表 invalidate 总要发
    }
    queryClient.invalidateQueries({ queryKey: ['users'] })
    triggerRefresh()
  }, [queryClient, triggerRefresh])

  const pageCount = table.getPageCount()
  useEffect(() => {
    ensurePageInRange(pageCount)
  }, [pageCount, ensurePageInRange])

  return (
    <>
      <div className='space-y-3 sm:space-y-4'>
        <div className='flex items-center gap-2'>
          <div className='flex-1 min-w-0'>
            <DataTableToolbar
              table={table}
              searchPlaceholder={t('Filter by username, name or email...')}
              filters={[
                {
                  columnId: 'status',
                  title: t('Status'),
                  options: getUserStatusOptions(t),
                },
                {
                  columnId: 'role',
                  title: t('Role'),
                  options: getUserRoleOptions(t),
                },
              ]}
            />
          </div>
          <Button
            variant='outline'
            size='sm'
            onClick={handleRefresh}
            disabled={isFetching}
            aria-label={t('Refresh')}
            title={t('Refresh user list and recompute RPM')}
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
            emptyTitle={t('No Users Found')}
            emptyDescription={t(
              'No users available. Try adjusting your search or filters.'
            )}
            getRowClassName={(row) =>
              isDisabledUserRow(row.original) ? DISABLED_ROW_MOBILE : undefined
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
                      {headerGroup.headers.map((header) => (
                        <TableHead key={header.id} colSpan={header.colSpan}>
                          {header.isPlaceholder
                            ? null
                            : flexRender(
                                header.column.columnDef.header,
                                header.getContext()
                              )}
                        </TableHead>
                      ))}
                    </TableRow>
                  ))}
                </TableHeader>
                <TableBody>
                  {isLoading ? (
                    <TableSkeleton table={table} keyPrefix='users-skeleton' />
                  ) : table.getRowModel().rows.length === 0 ? (
                    <TableEmpty
                      colSpan={columns.length}
                      title={t('No Users Found')}
                      description={t(
                        'No users available. Try adjusting your search or filters.'
                      )}
                    />
                  ) : (
                    table.getRowModel().rows.map((row) => {
                      const user = row.original

                      return (
                        <TableRow
                          key={row.id}
                          data-state={row.getIsSelected() && 'selected'}
                          className={cn(
                            isDisabledUserRow(user) && DISABLED_ROW_DESKTOP
                          )}
                        >
                          {row.getVisibleCells().map((cell) => (
                            <TableCell key={cell.id}>
                              {flexRender(
                                cell.column.columnDef.cell,
                                cell.getContext()
                              )}
                            </TableCell>
                          ))}
                        </TableRow>
                      )
                    })
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
