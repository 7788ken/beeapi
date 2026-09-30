import { useState } from 'react'
import { z } from 'zod'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Pencil, Plus, Save, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogCancel,
  AlertDialogAction,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { surfaceClass } from '@/components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogHeader,
  DialogFooter,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableHeader,
  TableHead,
  TableBody,
  TableRow,
  TableCell,
} from '@/components/ui/table'
import {
  deleteIQModel,
  getIQModels,
  iqKeys,
  saveIQModel,
  type IQModel,
} from '../api'
import { iqErrorMessage } from '../lib'
import { IQIconButton, IQPagination, IQQueryState } from './shared'

const schema = z.object({
  model_name: z.string().trim().min(1).max(128),
  baseline_score: z.number().int().min(0).max(100),
  enabled: z.boolean(),
})
type Values = z.infer<typeof schema>

function ModelForm(props: { model: IQModel | null; close: () => void }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: props.model ?? {
      model_name: '',
      baseline_score: 70,
      enabled: true,
    },
  })
  const save = useMutation({
    mutationFn: saveIQModel,
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: iqKeys.models })
      toast.success(t('Saved successfully'))
      props.close()
    },
  })
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !save.isPending) props.close()
      }}
    >
      <DialogContent className='sm:max-w-md'>
        <DialogHeader>
          <DialogTitle>
            {props.model ? t('IQ Edit model') : t('IQ Add model')}
          </DialogTitle>
        </DialogHeader>
        <form
          className='space-y-5'
          onSubmit={form.handleSubmit((values) =>
            save.mutate({
              ...values,
              id: props.model?.id,
              version: props.model?.version,
            })
          )}
        >
          <div className='space-y-2'>
            <Label htmlFor='iq-model-name'>{t('Model')}</Label>
            <Input
              id='iq-model-name'
              autoFocus
              maxLength={128}
              {...form.register('model_name')}
            />
            {form.formState.errors.model_name && (
              <p role='alert' className='text-destructive text-xs'>
                {t('IQ Model required')}
              </p>
            )}
          </div>
          <div className='space-y-2'>
            <Label htmlFor='iq-model-baseline'>{t('IQ Baseline')}</Label>
            <Input
              id='iq-model-baseline'
              type='number'
              min={0}
              max={100}
              step={1}
              {...form.register('baseline_score', { valueAsNumber: true })}
            />
            {form.formState.errors.baseline_score && (
              <p role='alert' className='text-destructive text-xs'>
                {t('IQ Value range', { min: 0, max: 100 })}
              </p>
            )}
          </div>
          <div className='flex items-center justify-between'>
            <Label htmlFor='iq-model-enabled'>{t('Enabled')}</Label>
            <Switch
              id='iq-model-enabled'
              checked={form.watch('enabled')}
              onCheckedChange={(enabled) => form.setValue('enabled', enabled)}
            />
          </div>
          {save.error && (
            <p role='alert' className='text-destructive text-sm break-words'>
              {iqErrorMessage(save.error)}
            </p>
          )}
          <DialogFooter>
            <Button
              variant='outline'
              type='button'
              onClick={props.close}
              disabled={save.isPending}
            >
              {t('Cancel')}
            </Button>
            <Button type='submit' disabled={save.isPending}>
              <Save className='size-4' />
              {t('Save')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export function IQModels(props: { editable: boolean }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [page, setPage] = useState(1)
  const [editing, setEditing] = useState<IQModel | null | undefined>()
  const [deleting, setDeleting] = useState<IQModel | null>(null)
  const query = useQuery({
    queryKey: [...iqKeys.models, page],
    queryFn: () => getIQModels(page),
  })
  const remove = useMutation({
    mutationFn: deleteIQModel,
    onSuccess: () => {
      setDeleting(null)
      if (query.data?.items.length === 1 && page > 1) setPage(page - 1)
      void client.invalidateQueries({ queryKey: iqKeys.models })
      void client.invalidateQueries({ queryKey: ['channels'] })
      toast.success(t('Deleted successfully'))
    },
  })
  const rows = query.data?.items ?? []
  return (
    <div className='space-y-4'>
      {props.editable && (
        <div className='flex justify-end'>
          <Button size='sm' onClick={() => setEditing(null)}>
            <Plus className='size-4' />
            {t('IQ Add model')}
          </Button>
        </div>
      )}
      <IQQueryState
        loading={query.isPending}
        error={query.error}
        empty={!rows.length}
        retry={() => void query.refetch()}
      />
      {!query.isError && rows.length > 0 && (
        <div className={cn(surfaceClass, 'overflow-hidden')}>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Model')}</TableHead>
                <TableHead>{t('IQ Baseline')}</TableHead>
                <TableHead>{t('Status')}</TableHead>
                {props.editable && (
                  <TableHead className='text-right'>{t('Actions')}</TableHead>
                )}
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((model) => (
                <TableRow key={model.id}>
                  <TableCell
                    className='max-w-80 truncate font-mono'
                    title={model.model_name}
                  >
                    {model.model_name}
                  </TableCell>
                  <TableCell className='tabular-nums'>
                    {model.baseline_score}
                  </TableCell>
                  <TableCell>
                    {model.enabled ? t('Enabled') : t('Disabled')}
                  </TableCell>
                  {props.editable && (
                    <TableCell>
                      <div className='flex justify-end gap-1'>
                        <IQIconButton
                          label={t('Edit')}
                          onClick={() => setEditing(model)}
                        >
                          <Pencil className='size-4' />
                        </IQIconButton>
                        <IQIconButton
                          label={t('Delete')}
                          onClick={() => setDeleting(model)}
                        >
                          <Trash2 className='text-destructive size-4' />
                        </IQIconButton>
                      </div>
                    </TableCell>
                  )}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      <IQPagination
        page={page}
        total={query.data?.total ?? 0}
        setPage={setPage}
        fetching={query.isFetching}
      />
      {editing !== undefined && (
        <ModelForm model={editing} close={() => setEditing(undefined)} />
      )}
      <AlertDialog
        open={deleting !== null}
        onOpenChange={(open) => {
          if (!open && !remove.isPending) setDeleting(null)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('IQ Delete model')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t('IQ Delete model confirmation', {
                model: deleting?.model_name,
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          {remove.error && (
            <p role='alert' className='text-destructive text-sm'>
              {iqErrorMessage(remove.error)}
            </p>
          )}
          <AlertDialogFooter>
            <AlertDialogCancel disabled={remove.isPending}>
              {t('Cancel')}
            </AlertDialogCancel>
            <AlertDialogAction
              disabled={remove.isPending}
              onClick={(event) => {
                event.preventDefault()
                if (deleting) remove.mutate(deleting)
              }}
            >
              {t('Delete')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
