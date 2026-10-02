import { SlidersHorizontalIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Input } from '@/components/ui/input'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { Switch } from '@/components/ui/switch'
import { composerChip } from '@/components/composer-styles'
import type { ParameterEnabled, PlaygroundConfig } from '../types'

type ParameterKey = keyof ParameterEnabled

type Props = {
  config: PlaygroundConfig
  disabled?: boolean
  parameterEnabled: ParameterEnabled
  onConfigChange: <K extends keyof PlaygroundConfig>(
    key: K,
    value: PlaygroundConfig[K]
  ) => void
  onParameterEnabledChange: (key: ParameterKey, value: boolean) => void
}

const controls: Array<{
  key: ParameterKey
  label: string
  min: number
  max: number
  step: number
}> = [
  { key: 'temperature', label: 'Temperature', min: 0.1, max: 1, step: 0.1 },
  { key: 'top_p', label: 'Top P', min: 0.1, max: 1, step: 0.1 },
  {
    key: 'frequency_penalty',
    label: 'Frequency Penalty',
    min: -2,
    max: 2,
    step: 0.1,
  },
  {
    key: 'presence_penalty',
    label: 'Presence Penalty',
    min: -2,
    max: 2,
    step: 0.1,
  },
  { key: 'max_tokens', label: 'Max Tokens', min: 0, max: 200000, step: 1 },
  { key: 'seed', label: 'Seed', min: 0, max: 2147483647, step: 1 },
]

function normalizeValue(
  control: (typeof controls)[number],
  raw: string
): number | null {
  if (raw === '') return control.key === 'seed' ? null : control.min
  const parsed = Number(raw)
  if (!Number.isFinite(parsed))
    return control.key === 'seed' ? null : control.min
  const clamped = Math.max(control.min, Math.min(control.max, parsed))
  return control.step >= 1 ? Math.trunc(clamped) : clamped
}

export function PlaygroundParameterPanel({
  config,
  disabled,
  parameterEnabled,
  onConfigChange,
  onParameterEnabledChange,
}: Props) {
  const { t } = useTranslation()

  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type='button'
          aria-label={t('Parameters')}
          className={composerChip}
          disabled={disabled}
        >
          <SlidersHorizontalIcon className='size-3.5 shrink-0' />
          <span className='hidden sm:inline'>{t('Parameters')}</span>
        </button>
      </PopoverTrigger>
      <PopoverContent
        align='end'
        className='w-[22rem] max-w-[calc(100vw-2rem)]'
        side='top'
      >
        <div className='mb-3'>
          <div className='text-sm font-semibold'>{t('Parameter settings')}</div>
          <div className='text-muted-foreground text-xs'>
            {t('Only enabled parameters are sent with the request.')}
          </div>
        </div>
        <div className='grid max-h-[min(28rem,calc(100vh-10rem))] gap-3 overflow-y-auto pr-1'>
          {controls.map((control) => {
            const enabled = parameterEnabled[control.key]
            const value = config[control.key]
            return (
              <div
                className='border-border grid gap-2 rounded-lg border p-3'
                key={control.key}
              >
                <div className='flex items-center justify-between gap-3'>
                  <label
                    className='text-sm font-medium'
                    htmlFor={`playground-${control.key}`}
                  >
                    {t(control.label)}
                  </label>
                  <Switch
                    checked={enabled}
                    disabled={disabled}
                    onCheckedChange={(checked) =>
                      onParameterEnabledChange(control.key, checked)
                    }
                  />
                </div>
                <Input
                  disabled={disabled || !enabled}
                  id={`playground-${control.key}`}
                  max={control.max}
                  min={control.min}
                  onChange={(event) => {
                    const next = normalizeValue(control, event.target.value)
                    if (control.key === 'seed') {
                      onConfigChange('seed', next)
                    } else {
                      onConfigChange(control.key, next ?? control.min)
                    }
                  }}
                  step={control.step}
                  type='number'
                  value={value ?? ''}
                />
              </div>
            )
          })}
        </div>
      </PopoverContent>
    </Popover>
  )
}
