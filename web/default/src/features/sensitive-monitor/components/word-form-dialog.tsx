import { useEffect, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
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
import { Textarea } from '@/components/ui/textarea'
import { createWord, updateWord, type WordPayload } from '../api'
import type { SensitiveWord } from '../types'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  editing?: SensitiveWord | null
}

const ACTION_BLOCK = 1
const ACTION_MONITOR = 2

const empty: WordPayload = {
  pattern: '',
  description: '',
  is_regex: false,
  action: ACTION_MONITOR,
  enabled: true,
}

export function WordFormDialog({ open, onOpenChange, editing }: Props) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [form, setForm] = useState<WordPayload>(empty)

  useEffect(() => {
    if (open) {
      setForm(
        editing
          ? {
              pattern: editing.pattern ?? '',
              description: editing.description ?? '',
              is_regex: !!editing.is_regex,
              action: editing.action ?? ACTION_MONITOR,
              enabled: editing.enabled !== false,
            }
          : empty
      )
    }
  }, [open, editing])

  const mutation = useMutation({
    mutationFn: async (payload: WordPayload) => {
      if (editing) return updateWord(editing.id, payload)
      return createWord(payload)
    },
    onSuccess: (res) => {
      if (res?.success) {
        toast.success(editing ? t('Updated successfully') : t('Created successfully'))
        queryClient.invalidateQueries({ queryKey: ['sensitive', 'words'] })
        onOpenChange(false)
      } else {
        toast.error(res?.message ?? t('Save failed'))
      }
    },
    onError: () => toast.error(t('Save failed')),
  })

  const submit = () => {
    const pattern = String(form.pattern ?? '').trim()
    if (!pattern) {
      toast.error(t('Keyword cannot be empty'))
      return
    }
    mutation.mutate({ ...form, pattern })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='sm:max-w-md'>
        <DialogHeader>
          <DialogTitle>
            {editing ? t('Edit keyword') : t('New keyword')}
          </DialogTitle>
          <DialogDescription>
            {t('Plain text or regex; case-insensitive substring match by default.')}
          </DialogDescription>
        </DialogHeader>
        <div className='space-y-3'>
          <div className='space-y-1.5'>
            <Label htmlFor='pattern'>{t('Keyword')}</Label>
            <Input
              id='pattern'
              value={form.pattern}
              onChange={(e) =>
                setForm((f) => ({ ...f, pattern: e.target.value }))
              }
              placeholder={t('Plain text or regex')}
            />
          </div>
          <div className='space-y-1.5'>
            <Label htmlFor='desc'>{t('Description')}</Label>
            <Textarea
              id='desc'
              value={form.description ?? ''}
              onChange={(e) =>
                setForm((f) => ({ ...f, description: e.target.value }))
              }
              rows={2}
            />
          </div>
          <div className='flex items-center justify-between'>
            <Label htmlFor='regex'>{t('Regex match')}</Label>
            <Switch
              id='regex'
              checked={form.is_regex}
              onCheckedChange={(v) =>
                setForm((f) => ({ ...f, is_regex: v }))
              }
            />
          </div>
          <div className='flex items-center justify-between'>
            <Label htmlFor='freeze'>{t('Freeze token on hit')}</Label>
            <Switch
              id='freeze'
              checked={form.action === ACTION_BLOCK}
              onCheckedChange={(v) =>
                setForm((f) => ({
                  ...f,
                  action: v ? ACTION_BLOCK : ACTION_MONITOR,
                }))
              }
            />
          </div>
          <div className='flex items-center justify-between'>
            <Label htmlFor='enabled'>{t('Enabled')}</Label>
            <Switch
              id='enabled'
              checked={form.enabled}
              onCheckedChange={(v) => setForm((f) => ({ ...f, enabled: v }))}
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant='outline' onClick={() => onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button onClick={submit} disabled={mutation.isPending}>
            {t('Save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
