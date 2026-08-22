import {
  memo,
  useCallback,
  useMemo,
  useRef,
  useState,
  type DragEvent,
} from 'react'
import { GripVertical, Lock, Pencil, Plus, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { type UseFormReturn } from 'react-hook-form'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
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
import { Switch } from '@/components/ui/switch'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { safeJsonParse } from '../utils/json-parser'

type GroupFormValues = {
  GroupRatio: string
  TopupGroupRatio: string
  UserUsableGroups: string
  GroupGroupRatio: string
  AutoGroups: string
  DefaultUseAutoGroup: boolean
  GroupSpecialUsableGroup: string
}

type GroupRatioMatrixEditorProps = {
  form: UseFormReturn<GroupFormValues>
}

type RatioMap = Record<string, number>
type GroupGroupMap = Record<string, RatioMap>

// 产品分组元信息：description + 是否对用户可选
type UsableGroupRawValue =
  | string
  | { description?: string; user_selectable?: boolean }
type UsableGroupsMap = Record<
  string,
  { description: string; user_selectable: boolean }
>

/**
 * 把 UserUsableGroups 的 JSON 字符串（可能是旧 string 值 / 新 object 值）
 * 规范化成统一对象格式，方便增删改和读取。
 */
function normalizeUsableGroups(jsonStr: string): UsableGroupsMap {
  const raw = safeJsonParse<Record<string, UsableGroupRawValue>>(jsonStr, {
    fallback: {},
    silent: true,
  })
  const out: UsableGroupsMap = {}
  for (const [k, v] of Object.entries(raw)) {
    if (typeof v === 'string') {
      out[k] = { description: v, user_selectable: true }
    } else {
      out[k] = {
        description: v?.description ?? '',
        // 字段缺失时默认 true，与新建分组默认值一致
        user_selectable: v?.user_selectable !== false,
      }
    }
  }
  return out
}

const ROW_ORDER_STORAGE_KEY = 'newapi:matrix-row-order'

function loadRowOrder(): string[] {
  if (typeof window === 'undefined') return []
  try {
    const raw = window.localStorage.getItem(ROW_ORDER_STORAGE_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw)
    return Array.isArray(parsed)
      ? parsed.filter((x): x is string => typeof x === 'string')
      : []
  } catch {
    return []
  }
}

function persistRowOrderToStorage(order: string[]) {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(ROW_ORDER_STORAGE_KEY, JSON.stringify(order))
  } catch {
    /* quota / privacy mode */
  }
}

function parseRatio(v: string): number | null {
  const t = v.trim()
  if (!t) return null
  const n = Number(t)
  if (!Number.isFinite(n)) return null
  return n
}

export const GroupRatioMatrixEditor = memo(function GroupRatioMatrixEditor({
  form,
}: GroupRatioMatrixEditorProps) {
  const { t } = useTranslation()

  const groupRatioStr = form.watch('GroupRatio')
  const groupGroupRatioStr = form.watch('GroupGroupRatio')
  const userUsableGroupsStr = form.watch('UserUsableGroups')

  const groupRatio = useMemo<RatioMap>(
    () =>
      safeJsonParse<RatioMap>(groupRatioStr, {
        fallback: {},
        context: 'group ratios',
      }),
    [groupRatioStr]
  )

  const groupGroupRatio = useMemo<GroupGroupMap>(
    () =>
      safeJsonParse<GroupGroupMap>(groupGroupRatioStr, {
        fallback: {},
        context: 'group-group ratios',
      }),
    [groupGroupRatioStr]
  )

  const usableGroups = useMemo<UsableGroupsMap>(
    () => normalizeUsableGroups(userUsableGroupsStr),
    [userUsableGroupsStr]
  )

  // 同时写 UserUsableGroups——内部一律存新对象格式，后端读取时会自动兼容
  const writeUsableGroups = useCallback(
    (next: UsableGroupsMap) => {
      form.setValue('UserUsableGroups', JSON.stringify(next, null, 2), {
        shouldValidate: true,
        shouldDirty: true,
      })
    },
    [form]
  )

  const [rowOrder, setRowOrder] = useState<string[]>(() => loadRowOrder())

  const updateRowOrder = useCallback((next: string[]) => {
    setRowOrder(next)
    persistRowOrderToStorage(next)
  }, [])

  const rows = useMemo(() => {
    const allSet = new Set<string>(Object.keys(groupRatio))
    for (const ug of Object.keys(groupGroupRatio)) {
      for (const tg of Object.keys(groupGroupRatio[ug] || {})) {
        allSet.add(tg)
      }
    }
    const all = Array.from(allSet)
    if (rowOrder.length === 0) return all
    const orderIdx = new Map<string, number>()
    rowOrder.forEach((k, i) => orderIdx.set(k, i))
    const naturalIdx = new Map<string, number>()
    all.forEach((k, i) => naturalIdx.set(k, i))
    return all.slice().sort((a, b) => {
      const ai = orderIdx.has(a) ? (orderIdx.get(a) as number) : Number.POSITIVE_INFINITY
      const bi = orderIdx.has(b) ? (orderIdx.get(b) as number) : Number.POSITIVE_INFINITY
      if (ai !== bi) return ai - bi
      return (naturalIdx.get(a) as number) - (naturalIdx.get(b) as number)
    })
  }, [groupRatio, groupGroupRatio, rowOrder])

  const userCols = useMemo(() => Object.keys(groupGroupRatio), [groupGroupRatio])

  const setFormJson = useCallback(
    (field: keyof GroupFormValues, value: unknown) => {
      form.setValue(field, JSON.stringify(value, null, 2), {
        shouldValidate: true,
        shouldDirty: true,
      })
    },
    [form]
  )

  // commit handlers read form.getValues() — react-hook-form keeps its internal
  // store synchronously up to date even before the next React render, so rapid
  // consecutive edits (Tab/click across cells in the same event tick) cannot
  // overwrite each other.
  const commitDefault = useCallback(
    (row: string, raw: string) => {
      const current = safeJsonParse<RatioMap>(form.getValues('GroupRatio'), {
        fallback: {},
        silent: true,
      })
      const next = { ...current }
      const n = parseRatio(raw)
      if (n === null) {
        delete next[row]
      } else {
        next[row] = n
      }
      setFormJson('GroupRatio', next)
    },
    [form, setFormJson]
  )

  const commitOverride = useCallback(
    (row: string, col: string, raw: string) => {
      const current = safeJsonParse<GroupGroupMap>(
        form.getValues('GroupGroupRatio'),
        { fallback: {}, silent: true }
      )
      const next: GroupGroupMap = { ...current }
      const colMap: RatioMap = { ...(next[col] || {}) }
      const n = parseRatio(raw)
      if (n === null) {
        delete colMap[row]
      } else {
        colMap[row] = n
      }
      // Keep the column key even if empty so the user-added column stays visible.
      next[col] = colMap
      setFormJson('GroupGroupRatio', next)
    },
    [form, setFormJson]
  )

  const [addRowOpen, setAddRowOpen] = useState(false)
  const [addColOpen, setAddColOpen] = useState(false)
  const [newRowName, setNewRowName] = useState('')
  const [newRowValue, setNewRowValue] = useState('1')
  const [newRowDescription, setNewRowDescription] = useState('')
  const [newRowUserSelectable, setNewRowUserSelectable] = useState(true)
  const [newColName, setNewColName] = useState('')

  // 编辑产品分组对话框：可改描述 + user_selectable + ratio；不允许改 name
  const [editRowOpen, setEditRowOpen] = useState(false)
  const [editRowName, setEditRowName] = useState('')
  const [editRowValue, setEditRowValue] = useState('1')
  const [editRowDescription, setEditRowDescription] = useState('')
  const [editRowUserSelectable, setEditRowUserSelectable] = useState(true)

  const handleAddRow = useCallback(() => {
    const name = newRowName.trim()
    if (!name) return
    if (Object.prototype.hasOwnProperty.call(groupRatio, name)) {
      toast.warning(t('Product "{{name}}" already exists', { name }))
      return
    }
    const n = parseRatio(newRowValue) ?? 1
    setFormJson('GroupRatio', { ...groupRatio, [name]: n })
    // 同步写一份到 UserUsableGroups——产品分组的元信息
    writeUsableGroups({
      ...usableGroups,
      [name]: {
        description: newRowDescription.trim(),
        user_selectable: newRowUserSelectable,
      },
    })
    if (!rowOrder.includes(name)) updateRowOrder([...rowOrder, name])
    setNewRowName('')
    setNewRowValue('1')
    setNewRowDescription('')
    setNewRowUserSelectable(true)
    setAddRowOpen(false)
  }, [
    newRowName,
    newRowValue,
    newRowDescription,
    newRowUserSelectable,
    groupRatio,
    usableGroups,
    rowOrder,
    setFormJson,
    writeUsableGroups,
    updateRowOrder,
    t,
  ])

  const openEditRow = useCallback(
    (row: string) => {
      const meta = usableGroups[row]
      setEditRowName(row)
      setEditRowValue(
        groupRatio[row] === undefined ? '' : String(groupRatio[row])
      )
      setEditRowDescription(meta?.description ?? '')
      setEditRowUserSelectable(meta?.user_selectable !== false)
      setEditRowOpen(true)
    },
    [usableGroups, groupRatio]
  )

  const handleSaveEditRow = useCallback(() => {
    const name = editRowName
    if (!name) return
    // ratio
    const n = parseRatio(editRowValue)
    const nextRatio = { ...groupRatio }
    if (n === null) {
      delete nextRatio[name]
    } else {
      nextRatio[name] = n
    }
    setFormJson('GroupRatio', nextRatio)
    // 元信息
    writeUsableGroups({
      ...usableGroups,
      [name]: {
        description: editRowDescription.trim(),
        user_selectable: editRowUserSelectable,
      },
    })
    setEditRowOpen(false)
  }, [
    editRowName,
    editRowValue,
    editRowDescription,
    editRowUserSelectable,
    groupRatio,
    usableGroups,
    setFormJson,
    writeUsableGroups,
  ])

  const handleAddCol = useCallback(() => {
    const name = newColName.trim()
    if (!name) return
    if (Object.prototype.hasOwnProperty.call(groupGroupRatio, name)) {
      toast.warning(t('User tier "{{name}}" already exists', { name }))
      return
    }
    setFormJson('GroupGroupRatio', {
      ...groupGroupRatio,
      [name]: {},
    })
    setNewColName('')
    setAddColOpen(false)
  }, [newColName, groupGroupRatio, setFormJson, t])

  const handleDeleteRow = useCallback(
    (row: string) => {
      const nextDefault = { ...groupRatio }
      delete nextDefault[row]
      setFormJson('GroupRatio', nextDefault)

      const nextGG: GroupGroupMap = {}
      for (const ug of Object.keys(groupGroupRatio)) {
        const colMap = { ...(groupGroupRatio[ug] || {}) }
        delete colMap[row]
        if (Object.keys(colMap).length > 0) {
          nextGG[ug] = colMap
        }
      }
      setFormJson('GroupGroupRatio', nextGG)

      // 同步清理 UserUsableGroups 里的元信息，避免悬空记录
      if (Object.prototype.hasOwnProperty.call(usableGroups, row)) {
        const nextUG = { ...usableGroups }
        delete nextUG[row]
        writeUsableGroups(nextUG)
      }

      if (rowOrder.includes(row)) {
        updateRowOrder(rowOrder.filter((k) => k !== row))
      }
    },
    [
      groupRatio,
      groupGroupRatio,
      usableGroups,
      rowOrder,
      setFormJson,
      writeUsableGroups,
      updateRowOrder,
    ]
  )

  const handleDeleteCol = useCallback(
    (col: string) => {
      const nextGG = { ...groupGroupRatio }
      delete nextGG[col]
      setFormJson('GroupGroupRatio', nextGG)
    },
    [groupGroupRatio, setFormJson]
  )

  const reorderObjectKeys = useCallback(
    <T,>(obj: Record<string, T>, order: string[]): Record<string, T> => {
      const out: Record<string, T> = {}
      for (const k of order) {
        if (Object.prototype.hasOwnProperty.call(obj, k)) out[k] = obj[k]
      }
      for (const k of Object.keys(obj)) {
        if (!Object.prototype.hasOwnProperty.call(out, k)) out[k] = obj[k]
      }
      return out
    },
    []
  )

  const reorderRow = useCallback(
    (from: string, to: string) => {
      if (from === to) return
      const order = [...rows]
      const fromIdx = order.indexOf(from)
      const toIdx = order.indexOf(to)
      if (fromIdx < 0 || toIdx < 0) return
      order.splice(fromIdx, 1)
      order.splice(toIdx, 0, from)

      // Persist explicit order — drives the rows useMemo sort, so override-only
      // rows can be placed anywhere (including before rows that have a default).
      updateRowOrder(order)

      // Also reorder the underlying maps so the JSON / Group ratios visual tabs
      // see a matching insertion order.
      setFormJson('GroupRatio', reorderObjectKeys(groupRatio, order))

      const nextGG: GroupGroupMap = {}
      for (const ug of Object.keys(groupGroupRatio)) {
        nextGG[ug] = reorderObjectKeys(groupGroupRatio[ug] || {}, order)
      }
      setFormJson('GroupGroupRatio', nextGG)
    },
    [rows, groupRatio, groupGroupRatio, reorderObjectKeys, setFormJson, updateRowOrder]
  )

  const reorderCol = useCallback(
    (from: string, to: string) => {
      if (from === to) return
      const order = [...userCols]
      const fromIdx = order.indexOf(from)
      const toIdx = order.indexOf(to)
      if (fromIdx < 0 || toIdx < 0) return
      order.splice(fromIdx, 1)
      order.splice(toIdx, 0, from)

      setFormJson('GroupGroupRatio', reorderObjectKeys(groupGroupRatio, order))
    },
    [userCols, groupGroupRatio, reorderObjectKeys, setFormJson]
  )

  // Per-row override slice with stable identity:
  // when only one row's overrides change, other rows keep the same array reference,
  // so MatrixRow memo skips re-render for them.
  type RowSlice = ReadonlyArray<number | undefined>
  const rowSliceCacheRef = useRef<Map<string, RowSlice>>(new Map())
  const rowSlices = useMemo(() => {
    const next = new Map<string, RowSlice>()
    for (const row of rows) {
      const slice: Array<number | undefined> = new Array(userCols.length)
      for (let i = 0; i < userCols.length; i++) {
        slice[i] = groupGroupRatio[userCols[i]]?.[row]
      }
      const prev = rowSliceCacheRef.current.get(row)
      if (
        prev &&
        prev.length === slice.length &&
        prev.every((v, i) => v === slice[i])
      ) {
        next.set(row, prev)
      } else {
        next.set(row, slice)
      }
    }
    rowSliceCacheRef.current = next
    return next
  }, [rows, userCols, groupGroupRatio])

  const dragKindRef = useRef<'row' | 'col' | null>(null)
  const dragKeyRef = useRef<string | null>(null)
  const [dragOverKey, setDragOverKey] = useState<string | null>(null)

  const startDrag = useCallback(
    (kind: 'row' | 'col', key: string, e: DragEvent) => {
      dragKindRef.current = kind
      dragKeyRef.current = key
      e.dataTransfer.effectAllowed = 'move'
      try {
        e.dataTransfer.setData('text/plain', key)
      } catch {
        /* some browsers throw without writable */
      }
    },
    []
  )

  const allowDrop = useCallback(
    (kind: 'row' | 'col', key: string, e: DragEvent) => {
      if (dragKindRef.current !== kind) return
      e.preventDefault()
      e.dataTransfer.dropEffect = 'move'
      if (dragOverKey !== key) setDragOverKey(key)
    },
    [dragOverKey]
  )

  const finishDrop = useCallback(
    (kind: 'row' | 'col', target: string, e: DragEvent) => {
      if (dragKindRef.current !== kind) return
      e.preventDefault()
      const source = dragKeyRef.current
      dragKindRef.current = null
      dragKeyRef.current = null
      setDragOverKey(null)
      if (!source || source === target) return
      if (kind === 'row') reorderRow(source, target)
      else reorderCol(source, target)
    },
    [reorderRow, reorderCol]
  )

  const cancelDrag = useCallback(() => {
    dragKindRef.current = null
    dragKeyRef.current = null
    setDragOverKey(null)
  }, [])

  return (
    <Card>
      <CardHeader>
        <div className='flex items-start justify-between gap-4'>
          <div>
            <CardTitle>{t('Pricing matrix')}</CardTitle>
            <CardDescription>
              {t(
                'Rows = products (channel groups). Columns = inter-group ratio overrides (GroupGroupRatio outer keys). The first column is the default ratio (GroupRatio). Leave a cell empty to fall back to the default. Click the pencil icon on any row to edit its description and toggle whether end-users can select it.'
              )}
            </CardDescription>
          </div>
          <div className='flex gap-2'>
            <Button size='sm' variant='outline' onClick={() => setAddRowOpen(true)}>
              <Plus className='mr-2 h-4 w-4' />
              {t('Add product')}
            </Button>
            <Button size='sm' variant='outline' onClick={() => setAddColOpen(true)}>
              <Plus className='mr-2 h-4 w-4' />
              {t('Add user tier')}
            </Button>
          </div>
        </div>
      </CardHeader>
      <CardContent>
        <div className='overflow-auto rounded-md border'>
          <table className='w-full border-collapse text-sm'>
            <thead className='bg-muted/50'>
              <tr>
                <th className='sticky left-0 z-20 min-w-[220px] border-b border-r bg-muted/80 p-2 text-left font-medium'>
                  {t('Product / User tier')}
                </th>
                <th className='min-w-[110px] border-b border-r bg-muted/80 p-2 text-center font-medium'>
                  {t('Default')}
                </th>
                {userCols.map((col) => (
                  <th
                    key={col}
                    draggable
                    onDragStart={(e) => startDrag('col', col, e)}
                    onDragOver={(e) => allowDrop('col', col, e)}
                    onDrop={(e) => finishDrop('col', col, e)}
                    onDragEnd={cancelDrag}
                    className={
                      'min-w-[140px] border-b border-r p-2 text-center font-medium transition-colors ' +
                      (dragOverKey === col && dragKindRef.current === 'col'
                        ? 'bg-blue-100'
                        : 'bg-muted/80')
                    }
                  >
                    <div className='flex items-center justify-center gap-1'>
                      <GripVertical
                        className='text-muted-foreground h-3 w-3 cursor-grab shrink-0'
                        aria-hidden
                      />
                      <span className='truncate' title={col}>
                        {col}
                      </span>
                      <Button
                        variant='ghost'
                        size='sm'
                        className='h-6 w-6 p-0'
                        onClick={() => handleDeleteCol(col)}
                        title={t('Delete user tier') as string}
                      >
                        <Trash2 className='h-3 w-3' />
                      </Button>
                    </div>
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.length === 0 && (
                <tr>
                  <td
                    colSpan={2 + userCols.length}
                    className='p-6 text-center text-muted-foreground'
                  >
                    {t('No products yet. Click "Add product" to start.')}
                  </td>
                </tr>
              )}
              {rows.map((row) => {
                const meta = usableGroups[row]
                return (
                  <MatrixRow
                    key={row}
                    row={row}
                    defaultValue={groupRatio[row]}
                    description={meta?.description ?? ''}
                    userSelectable={meta?.user_selectable !== false}
                    userCols={userCols}
                    rowSlice={rowSlices.get(row) || EMPTY_SLICE}
                    onCommitDefault={commitDefault}
                    onCommitOverride={commitOverride}
                    onEdit={openEditRow}
                    onDelete={handleDeleteRow}
                    editLabel={t('Edit product group') as string}
                    deleteLabel={t('Delete product') as string}
                    adminOnlyLabel={t('Admin only') as string}
                    isDragOver={dragOverKey === row && dragKindRef.current === 'row'}
                    onDragStart={(e) => startDrag('row', row, e)}
                    onDragOver={(e) => allowDrop('row', row, e)}
                    onDrop={(e) => finishDrop('row', row, e)}
                    onDragEnd={cancelDrag}
                  />
                )
              })}
            </tbody>
          </table>
        </div>
      </CardContent>

      <Dialog open={addRowOpen} onOpenChange={setAddRowOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('Add product (channel group)')}</DialogTitle>
            <DialogDescription>
              {t('Add a new row. Name should match the group used on channels.')}
            </DialogDescription>
          </DialogHeader>
          <div className='space-y-4 py-2'>
            <div className='space-y-2'>
              <Label>{t('Group name')}</Label>
              <Input
                value={newRowName}
                onChange={(e) => setNewRowName(e.target.value)}
                placeholder='🚀 Claude - Max2'
              />
            </div>
            <div className='space-y-2'>
              <Label>{t('Default ratio')}</Label>
              <Input
                value={newRowValue}
                onChange={(e) => setNewRowValue(e.target.value)}
                placeholder='1'
              />
            </div>
            <div className='space-y-2'>
              <Label>{t('Description')}</Label>
              <Input
                value={newRowDescription}
                onChange={(e) => setNewRowDescription(e.target.value)}
                placeholder={t('VIP users with premium access')}
              />
            </div>
            <div className='flex items-center justify-between rounded-lg border p-3'>
              <div className='space-y-0.5'>
                <Label className='text-sm'>{t('User selectable')}</Label>
                <p className='text-xs text-muted-foreground'>
                  {t(
                    'When off, this group is admin-only — hidden from /api/group/user.'
                  )}
                </p>
              </div>
              <Switch
                checked={newRowUserSelectable}
                onCheckedChange={setNewRowUserSelectable}
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant='outline' onClick={() => setAddRowOpen(false)}>
              {t('Cancel')}
            </Button>
            <Button onClick={handleAddRow}>{t('Add')}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={editRowOpen} onOpenChange={setEditRowOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('Edit product group')}</DialogTitle>
            <DialogDescription>
              {t(
                'Configure a product group. Toggle "User selectable" off to hide it from end-user API key creation while keeping it usable by admin.'
              )}
            </DialogDescription>
          </DialogHeader>
          <div className='space-y-4 py-2'>
            <div className='space-y-2'>
              <Label>{t('Group name')}</Label>
              <Input value={editRowName} disabled />
            </div>
            <div className='space-y-2'>
              <Label>{t('Default ratio')}</Label>
              <Input
                value={editRowValue}
                onChange={(e) => setEditRowValue(e.target.value)}
                placeholder='1'
              />
            </div>
            <div className='space-y-2'>
              <Label>{t('Description')}</Label>
              <Input
                value={editRowDescription}
                onChange={(e) => setEditRowDescription(e.target.value)}
                placeholder={t('VIP users with premium access')}
              />
            </div>
            <div className='flex items-center justify-between rounded-lg border p-3'>
              <div className='space-y-0.5'>
                <Label className='text-sm'>{t('User selectable')}</Label>
                <p className='text-xs text-muted-foreground'>
                  {t(
                    'When off, this group is admin-only — hidden from /api/group/user.'
                  )}
                </p>
              </div>
              <Switch
                checked={editRowUserSelectable}
                onCheckedChange={setEditRowUserSelectable}
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant='outline' onClick={() => setEditRowOpen(false)}>
              {t('Cancel')}
            </Button>
            <Button onClick={handleSaveEditRow}>{t('Update')}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={addColOpen} onOpenChange={setAddColOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('Add user tier')}</DialogTitle>
            <DialogDescription>
              {t(
                'Add a new column for inter-group ratio overrides. The name must match a user group.'
              )}
            </DialogDescription>
          </DialogHeader>
          <div className='space-y-4 py-2'>
            <div className='space-y-2'>
              <Label>{t('User tier (group) name')}</Label>
              <Input
                value={newColName}
                onChange={(e) => setNewColName(e.target.value)}
                placeholder='👑 企业用户-T0'
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant='outline' onClick={() => setAddColOpen(false)}>
              {t('Cancel')}
            </Button>
            <Button onClick={handleAddCol}>{t('Add')}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  )
})

const EMPTY_SLICE: ReadonlyArray<number | undefined> = []

type MatrixRowProps = {
  row: string
  defaultValue: number | undefined
  description: string
  userSelectable: boolean
  userCols: string[]
  rowSlice: ReadonlyArray<number | undefined>
  onCommitDefault: (row: string, raw: string) => void
  onCommitOverride: (row: string, col: string, raw: string) => void
  onEdit: (row: string) => void
  onDelete: (row: string) => void
  editLabel: string
  deleteLabel: string
  adminOnlyLabel: string
  isDragOver: boolean
  onDragStart: (e: DragEvent) => void
  onDragOver: (e: DragEvent) => void
  onDrop: (e: DragEvent) => void
  onDragEnd: () => void
}

const MatrixRow = memo(function MatrixRow({
  row,
  defaultValue,
  description,
  userSelectable,
  userCols,
  rowSlice,
  onCommitDefault,
  onCommitOverride,
  onEdit,
  onDelete,
  editLabel,
  deleteLabel,
  adminOnlyLabel,
  isDragOver,
  onDragStart,
  onDragOver,
  onDrop,
  onDragEnd,
}: MatrixRowProps) {
  return (
    <tr
      onDragOver={onDragOver}
      onDrop={onDrop}
      className={cn(
        'hover:bg-muted/30',
        isDragOver && 'bg-blue-50',
        // 用户不可选的产品分组整行降低视觉权重
        !userSelectable && 'text-muted-foreground'
      )}
    >
      <td
        draggable
        onDragStart={onDragStart}
        onDragEnd={onDragEnd}
        className={cn(
          'sticky left-0 z-10 border-b border-r p-2 font-medium',
          userSelectable ? 'bg-background' : 'bg-muted/40'
        )}
      >
        <div className='flex items-center justify-between gap-2'>
          <GripVertical
            className='text-muted-foreground h-3 w-3 cursor-grab shrink-0'
            aria-hidden
          />
          {!userSelectable ? (
            <TooltipProvider delayDuration={120}>
              <Tooltip>
                <TooltipTrigger asChild>
                  <Lock className='h-3 w-3 shrink-0' aria-label={adminOnlyLabel} />
                </TooltipTrigger>
                <TooltipContent>{adminOnlyLabel}</TooltipContent>
              </Tooltip>
            </TooltipProvider>
          ) : null}
          <span
            className='truncate flex-1'
            title={description ? `${row} — ${description}` : row}
          >
            {row}
          </span>
          {description ? (
            <Badge variant='outline' className='hidden lg:inline-flex max-w-[180px] truncate'>
              <span className='truncate' title={description}>
                {description}
              </span>
            </Badge>
          ) : null}
          <Button
            variant='ghost'
            size='sm'
            className='h-6 w-6 shrink-0 p-0'
            onClick={() => onEdit(row)}
            title={editLabel}
          >
            <Pencil className='h-3 w-3' />
          </Button>
          <Button
            variant='ghost'
            size='sm'
            className='h-6 w-6 shrink-0 p-0'
            onClick={() => onDelete(row)}
            title={deleteLabel}
          >
            <Trash2 className='h-3 w-3' />
          </Button>
        </div>
      </td>
      <CellInput
        key={`${row}-__default__-${defaultValue ?? ''}`}
        initial={defaultValue === undefined ? '' : String(defaultValue)}
        onCommit={(v) => onCommitDefault(row, v)}
        highlight={false}
      />
      {userCols.map((col, i) => {
        const v = rowSlice[i]
        return (
          <CellInput
            key={`${row}-${col}-${v ?? ''}`}
            initial={v === undefined ? '' : String(v)}
            onCommit={(raw) => onCommitOverride(row, col, raw)}
            highlight={v !== undefined}
          />
        )
      })}
    </tr>
  )
})

type CellInputProps = {
  initial: string
  onCommit: (raw: string) => void
  highlight: boolean
}

function CellInput({ initial, onCommit, highlight }: CellInputProps) {
  const [val, setVal] = useState(initial)
  return (
    <td className='border-b border-r p-0'>
      <Input
        value={val}
        onChange={(e) => setVal(e.target.value)}
        onBlur={() => {
          if (val !== initial) onCommit(val)
        }}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            ;(e.target as HTMLInputElement).blur()
          }
        }}
        className={
          'h-8 rounded-none border-0 text-center text-sm focus-visible:ring-1 ' +
          (highlight ? 'bg-amber-50 font-semibold text-amber-900' : '')
        }
        placeholder=''
      />
    </td>
  )
}
