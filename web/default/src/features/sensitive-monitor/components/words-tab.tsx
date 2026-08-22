import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Pencil, Plus, Search, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import dayjs from '@/lib/dayjs'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { deleteWord, listWords, toggleWord } from '../api'
import type { SensitiveWord } from '../types'
import { WordFormDialog } from './word-form-dialog'

const PAGE_SIZE = 20

function fmtTime(ts?: number) {
  if (!ts) return '-'
  return dayjs(ts * 1000).format('YYYY-MM-DD HH:mm:ss')
}

export function WordsTab() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [page, setPage] = useState(1)
  const [keyword, setKeyword] = useState('')
  const [debouncedKeyword, setDebouncedKeyword] = useState('')
  const [editing, setEditing] = useState<SensitiveWord | null>(null)
  const [editOpen, setEditOpen] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState<SensitiveWord | null>(null)

  const query = useQuery({
    queryKey: ['sensitive', 'words', page, debouncedKeyword] as const,
    queryFn: async () => {
      const res = await listWords({
        page,
        pageSize: PAGE_SIZE,
        keyword: debouncedKeyword || undefined,
      })
      return res?.data ?? { items: [], total: 0 }
    },
  })

  const toggleMutation = useMutation({
    mutationFn: (id: number) => toggleWord(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['sensitive', 'words'] })
    },
    onError: () => toast.error(t('Save failed')),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: number) => deleteWord(id),
    onSuccess: () => {
      toast.success(t('Deleted'))
      queryClient.invalidateQueries({ queryKey: ['sensitive', 'words'] })
      setConfirmDelete(null)
    },
    onError: () => toast.error(t('Delete failed')),
  })

  const submitSearch = (e: React.FormEvent) => {
    e.preventDefault()
    setPage(1)
    setDebouncedKeyword(keyword.trim())
  }

  const items = query.data?.items ?? []
  const total = query.data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  return (
    <div className='space-y-3'>
      <div className='flex flex-wrap items-center gap-2'>
        <form onSubmit={submitSearch} className='flex items-center gap-2'>
          <div className='relative'>
            <Search className='text-muted-foreground absolute top-1/2 left-2 size-3.5 -translate-y-1/2' />
            <Input
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
              placeholder={t('Search keyword or description')}
              className='h-9 w-64 ps-7'
            />
          </div>
          <Button type='submit' variant='outline' size='sm'>
            {t('Search')}
          </Button>
        </form>
        <div className='ms-auto flex items-center gap-2'>
          <Button
            size='sm'
            onClick={() => {
              setEditing(null)
              setEditOpen(true)
            }}
          >
            <Plus className='size-4' />
            {t('New keyword')}
          </Button>
        </div>
      </div>

      <div className='rounded-lg border'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className='w-12'>{t('ID')}</TableHead>
              <TableHead>{t('Keyword')}</TableHead>
              <TableHead className='w-20'>{t('Regex')}</TableHead>
              <TableHead className='w-32'>{t('On hit')}</TableHead>
              <TableHead className='w-20'>{t('Enabled')}</TableHead>
              <TableHead className='w-20'>{t('Hits')}</TableHead>
              <TableHead className='w-44'>{t('Last hit')}</TableHead>
              <TableHead className='w-44'>{t('Updated at')}</TableHead>
              <TableHead className='w-24 text-right'>{t('Actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {query.isLoading ? (
              <TableRow>
                <TableCell colSpan={9} className='text-center'>
                  {t('Loading...')}
                </TableCell>
              </TableRow>
            ) : items.length === 0 ? (
              <TableRow>
                <TableCell colSpan={9} className='text-muted-foreground text-center'>
                  {t('No keywords yet')}
                </TableCell>
              </TableRow>
            ) : (
              items.map((row) => (
                <TableRow key={row.id}>
                  <TableCell className='tabular-nums'>{row.id}</TableCell>
                  <TableCell className='max-w-[280px]'>
                    <div className='truncate font-medium'>{row.pattern}</div>
                    {row.description && (
                      <div className='text-muted-foreground truncate text-xs'>
                        {row.description}
                      </div>
                    )}
                  </TableCell>
                  <TableCell>
                    {row.is_regex ? (
                      <Badge variant='secondary'>{t('Regex')}</Badge>
                    ) : (
                      <span className='text-muted-foreground text-xs'>
                        {t('Substring')}
                      </span>
                    )}
                  </TableCell>
                  <TableCell>
                    {row.action === 1 ? (
                      <Badge variant='destructive'>
                        {t('Record + freeze token')}
                      </Badge>
                    ) : (
                      <Badge variant='secondary'>{t('Record only')}</Badge>
                    )}
                  </TableCell>
                  <TableCell>
                    <Switch
                      checked={row.enabled}
                      onCheckedChange={() => toggleMutation.mutate(row.id)}
                    />
                  </TableCell>
                  <TableCell className='tabular-nums'>{row.hit_count}</TableCell>
                  <TableCell className='tabular-nums'>
                    {fmtTime(row.last_hit_at)}
                  </TableCell>
                  <TableCell className='tabular-nums'>
                    {fmtTime(row.updated_at)}
                  </TableCell>
                  <TableCell className='text-right'>
                    <div className='flex justify-end gap-1'>
                      <Button
                        variant='ghost'
                        size='icon'
                        className='size-7'
                        onClick={() => {
                          setEditing(row)
                          setEditOpen(true)
                        }}
                        aria-label={t('Edit')}
                      >
                        <Pencil className='size-3.5' />
                      </Button>
                      <Button
                        variant='ghost'
                        size='icon'
                        className='size-7 text-rose-500 hover:text-rose-500'
                        onClick={() => setConfirmDelete(row)}
                        aria-label={t('Delete')}
                      >
                        <Trash2 className='size-3.5' />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>

      {totalPages > 1 && (
        <div className='flex items-center justify-end gap-2'>
          <span className='text-muted-foreground text-xs'>
            {t('Page {{page}} / {{total}}', { page, total: totalPages })}
          </span>
          <Button
            variant='outline'
            size='sm'
            disabled={page <= 1}
            onClick={() => setPage((p) => p - 1)}
          >
            {t('Prev')}
          </Button>
          <Button
            variant='outline'
            size='sm'
            disabled={page >= totalPages}
            onClick={() => setPage((p) => p + 1)}
          >
            {t('Next')}
          </Button>
        </div>
      )}

      <WordFormDialog
        open={editOpen}
        onOpenChange={setEditOpen}
        editing={editing}
      />

      <AlertDialog
        open={!!confirmDelete}
        onOpenChange={(o) => !o && setConfirmDelete(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Delete keyword?')}</AlertDialogTitle>
            <AlertDialogDescription>
              {confirmDelete?.pattern}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('Cancel')}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() =>
                confirmDelete && deleteMutation.mutate(confirmDelete.id)
              }
            >
              {t('Delete')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
