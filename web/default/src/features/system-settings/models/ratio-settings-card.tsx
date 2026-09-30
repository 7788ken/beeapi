import { useCallback, useEffect, useRef, useState } from 'react'
import * as z from 'zod'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { surfaceClass } from '@/components/ui/card'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { getPendingPriceChanges } from '@/features/price-changes/api'
import { resetModelRatios } from '../api'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import { GroupRatioForm } from './group-ratio-form'
import { GroupRatioMatrixEditor } from './group-ratio-matrix-editor'
import { ModelRatioForm } from './model-ratio-form'
import {
  PENDING_PRICE_CHANGES_QUERY_KEY,
  PricePublishPanel,
} from './price-publish-panel'
import { ToolPriceSettings } from './tool-price-settings'
import { UpstreamRatioSync } from './upstream-ratio-sync'
import {
  formatJsonForTextarea,
  normalizeJsonString,
  validateJsonString,
} from './utils'

const modelSchema = z.object({
  ModelPrice: z.string().superRefine((value, ctx) => {
    const result = validateJsonString(value)
    if (!result.valid) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: result.message || 'Invalid JSON',
      })
    }
  }),
  ModelRatio: z.string().superRefine((value, ctx) => {
    const result = validateJsonString(value)
    if (!result.valid) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: result.message || 'Invalid JSON',
      })
    }
  }),
  CacheRatio: z.string().superRefine((value, ctx) => {
    const result = validateJsonString(value)
    if (!result.valid) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: result.message || 'Invalid JSON',
      })
    }
  }),
  CreateCacheRatio: z.string().superRefine((value, ctx) => {
    const result = validateJsonString(value)
    if (!result.valid) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: result.message || 'Invalid JSON',
      })
    }
  }),
  CompletionRatio: z.string().superRefine((value, ctx) => {
    const result = validateJsonString(value)
    if (!result.valid) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: result.message || 'Invalid JSON',
      })
    }
  }),
  ImageRatio: z.string().superRefine((value, ctx) => {
    const result = validateJsonString(value)
    if (!result.valid) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: result.message || 'Invalid JSON',
      })
    }
  }),
  AudioRatio: z.string().superRefine((value, ctx) => {
    const result = validateJsonString(value)
    if (!result.valid) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: result.message || 'Invalid JSON',
      })
    }
  }),
  AudioCompletionRatio: z.string().superRefine((value, ctx) => {
    const result = validateJsonString(value)
    if (!result.valid) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: result.message || 'Invalid JSON',
      })
    }
  }),
  ExposeRatioEnabled: z.boolean(),
  BillingMode: z.string().superRefine((value, ctx) => {
    const result = validateJsonString(value)
    if (!result.valid) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: result.message || 'Invalid JSON',
      })
    }
  }),
  BillingExpr: z.string().superRefine((value, ctx) => {
    const result = validateJsonString(value)
    if (!result.valid) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: result.message || 'Invalid JSON',
      })
    }
  }),
})

const groupSchema = z.object({
  GroupRatio: z.string().superRefine((value, ctx) => {
    const result = validateJsonString(value)
    if (!result.valid) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: result.message || 'Invalid JSON',
      })
    }
  }),
  TopupGroupRatio: z.string().superRefine((value, ctx) => {
    const result = validateJsonString(value)
    if (!result.valid) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: result.message || 'Invalid JSON',
      })
    }
  }),
  UserUsableGroups: z.string().superRefine((value, ctx) => {
    const result = validateJsonString(value)
    if (!result.valid) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: result.message || 'Invalid JSON',
      })
    }
  }),
  GroupGroupRatio: z.string().superRefine((value, ctx) => {
    const result = validateJsonString(value)
    if (!result.valid) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: result.message || 'Invalid JSON',
      })
    }
  }),
  AutoGroups: z.string().superRefine((value, ctx) => {
    const result = validateJsonString(value, {
      predicate: (parsed) =>
        Array.isArray(parsed) &&
        parsed.every((item) => typeof item === 'string'),
      predicateMessage: 'Expected a JSON array of group identifiers',
    })
    if (!result.valid) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: result.message || 'Invalid JSON array',
      })
    }
  }),
  DefaultUseAutoGroup: z.boolean(),
  GroupSpecialUsableGroup: z.string().superRefine((value, ctx) => {
    const result = validateJsonString(value)
    if (!result.valid) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: result.message || 'Invalid JSON',
      })
    }
  }),
})

type ModelFormValues = z.infer<typeof modelSchema>
type GroupFormValues = z.infer<typeof groupSchema>

function groupDefaultsSnapshot(defaults: GroupFormValues): string {
  return JSON.stringify({
    GroupRatio: normalizeJsonString(defaults.GroupRatio),
    TopupGroupRatio: normalizeJsonString(defaults.TopupGroupRatio),
    UserUsableGroups: normalizeJsonString(defaults.UserUsableGroups),
    GroupGroupRatio: normalizeJsonString(defaults.GroupGroupRatio),
    AutoGroups: normalizeJsonString(defaults.AutoGroups),
    DefaultUseAutoGroup: defaults.DefaultUseAutoGroup,
    GroupSpecialUsableGroup: normalizeJsonString(
      defaults.GroupSpecialUsableGroup
    ),
  })
}

function modelDefaultsSnapshot(defaults: ModelFormValues): string {
  return JSON.stringify({
    ModelPrice: normalizeJsonString(defaults.ModelPrice),
    ModelRatio: normalizeJsonString(defaults.ModelRatio),
    CacheRatio: normalizeJsonString(defaults.CacheRatio),
    CreateCacheRatio: normalizeJsonString(defaults.CreateCacheRatio),
    CompletionRatio: normalizeJsonString(defaults.CompletionRatio),
    ImageRatio: normalizeJsonString(defaults.ImageRatio),
    AudioRatio: normalizeJsonString(defaults.AudioRatio),
    AudioCompletionRatio: normalizeJsonString(defaults.AudioCompletionRatio),
    ExposeRatioEnabled: defaults.ExposeRatioEnabled,
    BillingMode: normalizeJsonString(defaults.BillingMode),
    BillingExpr: normalizeJsonString(defaults.BillingExpr),
  })
}

type RatioSettingsCardProps = {
  modelDefaults: ModelFormValues
  groupDefaults: GroupFormValues
  toolPricesDefault: string
}

export function RatioSettingsCard({
  modelDefaults,
  groupDefaults,
  toolPricesDefault,
}: RatioSettingsCardProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const queryClient = useQueryClient()
  const [confirmOpen, setConfirmOpen] = useState(false)

  // Unpublished price change hint (red dot on the publish tab)
  const pendingPriceChangesQuery = useQuery({
    queryKey: PENDING_PRICE_CHANGES_QUERY_KEY,
    queryFn: getPendingPriceChanges,
    retry: false,
    staleTime: 30 * 1000,
  })
  const hasPendingPriceChanges =
    pendingPriceChangesQuery.data?.has_changes === true

  const resetMutation = useMutation({
    mutationFn: resetModelRatios,
    onSuccess: (data) => {
      if (data.success) {
        toast.success(t('Model ratios reset successfully'))
        queryClient.invalidateQueries({ queryKey: ['system-options'] })
        setConfirmOpen(false)
      } else {
        toast.error(data.message || t('Failed to reset model ratios'))
      }
    },
    onError: (error: Error) => {
      toast.error(error.message || t('Failed to reset model ratios'))
    },
  })

  const modelNormalizedDefaults = useRef({
    ModelPrice: normalizeJsonString(modelDefaults.ModelPrice),
    ModelRatio: normalizeJsonString(modelDefaults.ModelRatio),
    CacheRatio: normalizeJsonString(modelDefaults.CacheRatio),
    CreateCacheRatio: normalizeJsonString(modelDefaults.CreateCacheRatio),
    CompletionRatio: normalizeJsonString(modelDefaults.CompletionRatio),
    ImageRatio: normalizeJsonString(modelDefaults.ImageRatio),
    AudioRatio: normalizeJsonString(modelDefaults.AudioRatio),
    AudioCompletionRatio: normalizeJsonString(
      modelDefaults.AudioCompletionRatio
    ),
    ExposeRatioEnabled: modelDefaults.ExposeRatioEnabled,
    BillingMode: normalizeJsonString(modelDefaults.BillingMode),
    BillingExpr: normalizeJsonString(modelDefaults.BillingExpr),
  })

  const groupNormalizedDefaults = useRef({
    GroupRatio: normalizeJsonString(groupDefaults.GroupRatio),
    TopupGroupRatio: normalizeJsonString(groupDefaults.TopupGroupRatio),
    UserUsableGroups: normalizeJsonString(groupDefaults.UserUsableGroups),
    GroupGroupRatio: normalizeJsonString(groupDefaults.GroupGroupRatio),
    AutoGroups: normalizeJsonString(groupDefaults.AutoGroups),
    DefaultUseAutoGroup: groupDefaults.DefaultUseAutoGroup,
    GroupSpecialUsableGroup: normalizeJsonString(
      groupDefaults.GroupSpecialUsableGroup
    ),
  })

  const modelForm = useForm<ModelFormValues>({
    resolver: zodResolver(modelSchema),
    mode: 'onChange',
    defaultValues: {
      ...modelDefaults,
      ModelPrice: formatJsonForTextarea(modelDefaults.ModelPrice),
      ModelRatio: formatJsonForTextarea(modelDefaults.ModelRatio),
      CacheRatio: formatJsonForTextarea(modelDefaults.CacheRatio),
      CreateCacheRatio: formatJsonForTextarea(modelDefaults.CreateCacheRatio),
      CompletionRatio: formatJsonForTextarea(modelDefaults.CompletionRatio),
      ImageRatio: formatJsonForTextarea(modelDefaults.ImageRatio),
      AudioRatio: formatJsonForTextarea(modelDefaults.AudioRatio),
      AudioCompletionRatio: formatJsonForTextarea(
        modelDefaults.AudioCompletionRatio
      ),
      BillingMode: formatJsonForTextarea(modelDefaults.BillingMode),
      BillingExpr: formatJsonForTextarea(modelDefaults.BillingExpr),
    },
  })

  const groupForm = useForm<GroupFormValues>({
    resolver: zodResolver(groupSchema),
    mode: 'onChange',
    defaultValues: {
      ...groupDefaults,
      GroupRatio: formatJsonForTextarea(groupDefaults.GroupRatio),
      TopupGroupRatio: formatJsonForTextarea(groupDefaults.TopupGroupRatio),
      UserUsableGroups: formatJsonForTextarea(groupDefaults.UserUsableGroups),
      GroupGroupRatio: formatJsonForTextarea(groupDefaults.GroupGroupRatio),
      AutoGroups: formatJsonForTextarea(groupDefaults.AutoGroups),
      GroupSpecialUsableGroup: formatJsonForTextarea(
        groupDefaults.GroupSpecialUsableGroup
      ),
    },
  })

  const lastModelSnapshotRef = useRef(modelDefaultsSnapshot(modelDefaults))
  const lastGroupSnapshotRef = useRef(groupDefaultsSnapshot(groupDefaults))

  useEffect(() => {
    const snapshot = modelDefaultsSnapshot(modelDefaults)
    if (snapshot === lastModelSnapshotRef.current) return
    lastModelSnapshotRef.current = snapshot

    modelNormalizedDefaults.current = {
      ModelPrice: normalizeJsonString(modelDefaults.ModelPrice),
      ModelRatio: normalizeJsonString(modelDefaults.ModelRatio),
      CacheRatio: normalizeJsonString(modelDefaults.CacheRatio),
      CreateCacheRatio: normalizeJsonString(modelDefaults.CreateCacheRatio),
      CompletionRatio: normalizeJsonString(modelDefaults.CompletionRatio),
      ImageRatio: normalizeJsonString(modelDefaults.ImageRatio),
      AudioRatio: normalizeJsonString(modelDefaults.AudioRatio),
      AudioCompletionRatio: normalizeJsonString(
        modelDefaults.AudioCompletionRatio
      ),
      ExposeRatioEnabled: modelDefaults.ExposeRatioEnabled,
      BillingMode: normalizeJsonString(modelDefaults.BillingMode),
      BillingExpr: normalizeJsonString(modelDefaults.BillingExpr),
    }

    modelForm.reset({
      ...modelDefaults,
      ModelPrice: formatJsonForTextarea(modelDefaults.ModelPrice),
      ModelRatio: formatJsonForTextarea(modelDefaults.ModelRatio),
      CacheRatio: formatJsonForTextarea(modelDefaults.CacheRatio),
      CreateCacheRatio: formatJsonForTextarea(modelDefaults.CreateCacheRatio),
      CompletionRatio: formatJsonForTextarea(modelDefaults.CompletionRatio),
      ImageRatio: formatJsonForTextarea(modelDefaults.ImageRatio),
      AudioRatio: formatJsonForTextarea(modelDefaults.AudioRatio),
      AudioCompletionRatio: formatJsonForTextarea(
        modelDefaults.AudioCompletionRatio
      ),
      BillingMode: formatJsonForTextarea(modelDefaults.BillingMode),
      BillingExpr: formatJsonForTextarea(modelDefaults.BillingExpr),
    })
  }, [modelDefaults, modelForm])

  useEffect(() => {
    const snapshot = groupDefaultsSnapshot(groupDefaults)
    if (snapshot === lastGroupSnapshotRef.current) return
    lastGroupSnapshotRef.current = snapshot

    groupNormalizedDefaults.current = {
      GroupRatio: normalizeJsonString(groupDefaults.GroupRatio),
      TopupGroupRatio: normalizeJsonString(groupDefaults.TopupGroupRatio),
      UserUsableGroups: normalizeJsonString(groupDefaults.UserUsableGroups),
      GroupGroupRatio: normalizeJsonString(groupDefaults.GroupGroupRatio),
      AutoGroups: normalizeJsonString(groupDefaults.AutoGroups),
      DefaultUseAutoGroup: groupDefaults.DefaultUseAutoGroup,
      GroupSpecialUsableGroup: normalizeJsonString(
        groupDefaults.GroupSpecialUsableGroup
      ),
    }

    groupForm.reset({
      ...groupDefaults,
      GroupRatio: formatJsonForTextarea(groupDefaults.GroupRatio),
      TopupGroupRatio: formatJsonForTextarea(groupDefaults.TopupGroupRatio),
      UserUsableGroups: formatJsonForTextarea(groupDefaults.UserUsableGroups),
      GroupGroupRatio: formatJsonForTextarea(groupDefaults.GroupGroupRatio),
      AutoGroups: formatJsonForTextarea(groupDefaults.AutoGroups),
      GroupSpecialUsableGroup: formatJsonForTextarea(
        groupDefaults.GroupSpecialUsableGroup
      ),
    })
  }, [groupDefaults, groupForm])

  const saveModelRatios = useCallback(
    async (values: ModelFormValues) => {
      const normalized = {
        ModelPrice: normalizeJsonString(values.ModelPrice),
        ModelRatio: normalizeJsonString(values.ModelRatio),
        CacheRatio: normalizeJsonString(values.CacheRatio),
        CreateCacheRatio: normalizeJsonString(values.CreateCacheRatio),
        CompletionRatio: normalizeJsonString(values.CompletionRatio),
        ImageRatio: normalizeJsonString(values.ImageRatio),
        AudioRatio: normalizeJsonString(values.AudioRatio),
        AudioCompletionRatio: normalizeJsonString(values.AudioCompletionRatio),
        ExposeRatioEnabled: values.ExposeRatioEnabled,
        BillingMode: normalizeJsonString(values.BillingMode),
        BillingExpr: normalizeJsonString(values.BillingExpr),
      }

      const apiKeyMap: Record<string, string> = {
        BillingMode: 'billing_setting.billing_mode',
        BillingExpr: 'billing_setting.billing_expr',
      }

      const updates = (
        Object.keys(normalized) as Array<keyof ModelFormValues>
      ).filter(
        (key) => normalized[key] !== modelNormalizedDefaults.current[key]
      )

      for (const key of updates) {
        const apiKey = apiKeyMap[key as string] || (key as string)
        await updateOption.mutateAsync({ key: apiKey, value: normalized[key] })
      }

      if (updates.length > 0) {
        queryClient.invalidateQueries({
          queryKey: PENDING_PRICE_CHANGES_QUERY_KEY,
        })
      }
    },
    [updateOption, queryClient]
  )

  const saveGroupRatios = useCallback(
    async (values: GroupFormValues) => {
      const normalized = {
        GroupRatio: normalizeJsonString(values.GroupRatio),
        TopupGroupRatio: normalizeJsonString(values.TopupGroupRatio),
        UserUsableGroups: normalizeJsonString(values.UserUsableGroups),
        GroupGroupRatio: normalizeJsonString(values.GroupGroupRatio),
        AutoGroups: normalizeJsonString(values.AutoGroups),
        DefaultUseAutoGroup: values.DefaultUseAutoGroup,
        GroupSpecialUsableGroup: normalizeJsonString(
          values.GroupSpecialUsableGroup
        ),
      }

      // Map form field names to API keys (most are 1:1, except GroupSpecialUsableGroup)
      const apiKeyMap: Record<string, string> = {
        GroupSpecialUsableGroup:
          'group_ratio_setting.group_special_usable_group',
      }

      const updates = (
        Object.keys(normalized) as Array<keyof typeof normalized>
      ).filter(
        (key) => normalized[key] !== groupNormalizedDefaults.current[key]
      )

      for (const key of updates) {
        const apiKey = apiKeyMap[key] || key
        await updateOption.mutateAsync({ key: apiKey, value: normalized[key] })
      }

      if (updates.length > 0) {
        queryClient.invalidateQueries({
          queryKey: PENDING_PRICE_CHANGES_QUERY_KEY,
        })
      }
    },
    [updateOption, queryClient]
  )

  const handleResetRatios = useCallback(() => {
    setConfirmOpen(true)
  }, [])

  const { mutate: resetMutate } = resetMutation
  const handleConfirmReset = useCallback(() => {
    resetMutate()
  }, [resetMutate])

  return (
    <SettingsSection
      title={t('Pricing Ratios')}
      description={t(
        'Configure model, caching, and group ratios used for billing'
      )}
      surface={false}
    >
      <Tabs defaultValue='matrix' className='space-y-6'>
        <TabsList className='grid h-auto w-full grid-cols-3 lg:grid-cols-6'>
          <TabsTrigger value='models'>{t('Model ratios')}</TabsTrigger>
          <TabsTrigger value='groups'>{t('Group ratios')}</TabsTrigger>
          <TabsTrigger value='matrix'>{t('Pricing matrix')}</TabsTrigger>
          <TabsTrigger value='tool-prices'>{t('Tool prices')}</TabsTrigger>
          <TabsTrigger value='upstream-sync'>
            {t('Upstream price sync')}
          </TabsTrigger>
          <TabsTrigger value='price-publish' className='relative'>
            {t('Price publish')}
            {hasPendingPriceChanges && (
              <span
                aria-hidden
                className='group-data-[state=active]/tab:bg-primary-foreground absolute top-1 right-1 size-1.5 rounded-full bg-red-500'
              />
            )}
          </TabsTrigger>
        </TabsList>

        <TabsContent value='models' className={cn(surfaceClass, 'p-6')}>
          <ModelRatioForm
            form={modelForm}
            onSave={saveModelRatios}
            onReset={handleResetRatios}
            isSaving={updateOption.isPending}
            isResetting={resetMutation.isPending}
          />
        </TabsContent>

        <TabsContent value='groups'>
          <GroupRatioForm
            form={groupForm}
            onSave={saveGroupRatios}
            isSaving={updateOption.isPending}
          />
        </TabsContent>

        <TabsContent value='matrix'>
          <div className='space-y-4'>
            <GroupRatioMatrixEditor form={groupForm} />
            <Button
              type='button'
              onClick={groupForm.handleSubmit(saveGroupRatios)}
              disabled={updateOption.isPending}
            >
              {updateOption.isPending ? t('Saving...') : t('Save group ratios')}
            </Button>
          </div>
        </TabsContent>

        <TabsContent value='tool-prices' className={cn(surfaceClass, 'p-6')}>
          <ToolPriceSettings defaultValue={toolPricesDefault} />
        </TabsContent>

        <TabsContent value='upstream-sync' className={cn(surfaceClass, 'p-6')}>
          <UpstreamRatioSync
            modelRatios={{
              ModelPrice: modelDefaults.ModelPrice,
              ModelRatio: modelDefaults.ModelRatio,
              CompletionRatio: modelDefaults.CompletionRatio,
              CacheRatio: modelDefaults.CacheRatio,
              CreateCacheRatio: modelDefaults.CreateCacheRatio,
              ImageRatio: modelDefaults.ImageRatio,
              AudioRatio: modelDefaults.AudioRatio,
              AudioCompletionRatio: modelDefaults.AudioCompletionRatio,
              'billing_setting.billing_mode': modelDefaults.BillingMode,
              'billing_setting.billing_expr': modelDefaults.BillingExpr,
            }}
          />
        </TabsContent>

        <TabsContent value='price-publish' className={cn(surfaceClass, 'p-6')}>
          <PricePublishPanel />
        </TabsContent>
      </Tabs>

      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t('Reset all model ratios?')}
        desc={t(
          'This will clear custom pricing ratios and revert to upstream defaults.'
        )}
        destructive
        isLoading={resetMutation.isPending}
        handleConfirm={handleConfirmReset}
        confirmText={t('Reset')}
      />
    </SettingsSection>
  )
}
