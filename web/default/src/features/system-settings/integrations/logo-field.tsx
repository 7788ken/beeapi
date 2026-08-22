import { type ChangeEvent, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

interface Props {
  value: string
  onChange: (value: string) => void
  label?: string
}

// 平台 logo 配置：可直接填图片 URL，或上传图片转 base64 dataURL 内嵌。
export function LogoField({ value, onChange, label }: Props) {
  const { t } = useTranslation()
  const fileRef = useRef<HTMLInputElement | null>(null)

  const handleFile = (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0]
    if (!file) return
    const maxSize = 100 * 1024
    if (file.size > maxSize) {
      toast.error(t('Icon file must be 100 KB or smaller'))
      event.target.value = ''
      return
    }
    const reader = new FileReader()
    reader.onload = (e) => {
      onChange(typeof e.target?.result === 'string' ? e.target.result : '')
    }
    reader.readAsDataURL(file)
    event.target.value = ''
  }

  return (
    <div className='grid gap-2'>
      <Label>{label ?? t('Logo')}</Label>
      <div className='flex items-center gap-3'>
        {value ? (
          <img
            src={value}
            alt={label ?? 'logo'}
            className='h-10 w-10 rounded border object-contain p-1'
          />
        ) : (
          <div className='bg-muted text-muted-foreground flex h-10 w-10 items-center justify-center rounded border text-xs'>
            {t('Logo')}
          </div>
        )}
        <Input
          value={value.startsWith('data:') ? '' : value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={t('Image URL, or upload below')}
          className='flex-1'
        />
        <input
          ref={fileRef}
          type='file'
          accept='image/png,image/jpeg,image/svg+xml,image/webp'
          className='hidden'
          onChange={handleFile}
        />
        <Button
          type='button'
          variant='outline'
          onClick={() => fileRef.current?.click()}
        >
          {t('Upload')}
        </Button>
        {value ? (
          <Button type='button' variant='outline' onClick={() => onChange('')}>
            {t('Clear')}
          </Button>
        ) : null}
      </div>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Enter an image URL, or upload an image (≤100 KB) to embed as base64. Shown as the payment method card logo.'
        )}
      </p>
    </div>
  )
}
