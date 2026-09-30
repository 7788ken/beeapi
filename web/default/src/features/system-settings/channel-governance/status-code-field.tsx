import type { Control, FieldPath, FieldValues } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { parseHttpStatusCodeRules } from '@/lib/http-status-code-rules'

export function StatusCodeField<T extends FieldValues>({
  control,
  name,
  label,
  description,
}: {
  control: Control<T>
  name: FieldPath<T>
  label: string
  description: string
}) {
  const { t } = useTranslation()

  return (
    <FormField
      control={control}
      name={name}
      render={({ field }) => {
        const value = typeof field.value === 'string' ? field.value : ''
        const parsed = parseHttpStatusCodeRules(value)
        return (
          <FormItem>
            <FormLabel>{label}</FormLabel>
            <FormControl>
              <Input
                placeholder={t('e.g. 401, 403, 429, 500-599')}
                value={value}
                onChange={(event) => field.onChange(event.target.value)}
              />
            </FormControl>
            <FormDescription>
              {description}{' '}
              {parsed.ok &&
                parsed.normalized &&
                parsed.normalized !== value.trim() && (
                  <span className='text-muted-foreground'>
                    {t('Normalized:')} {parsed.normalized}
                  </span>
                )}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )
      }}
    />
  )
}
