import type { ReactNode, Ref } from 'react'
import { MessageSquarePlus, SendIcon, SquareIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import {
  PromptInput,
  PromptInputFooter,
  PromptInputTextarea,
} from '@/components/ai-elements/prompt-input'
import {
  composerChip,
  composerFrame,
  composerPrimary,
} from '@/components/composer-styles'
import type { ParameterEnabled, PlaygroundConfig } from '../types'
import { PlaygroundParameterPanel } from './playground-parameter-panel'

interface PlaygroundInputProps {
  /** 草稿由父级持有，空态的示例提示要能填进来 */
  value: string
  onValueChange: (value: string) => void
  textareaRef: Ref<HTMLTextAreaElement>
  onSubmit: (text: string) => void
  onStop: () => void
  /** 有 key、有模型、没在生成 */
  canSend: boolean
  isGenerating: boolean
  /** 底栏左侧：API Key 与模型选择器 */
  selectors: ReactNode
  /** 有消息时提供：清空当前对话回到空态 */
  onNewChat?: () => void
  config: PlaygroundConfig
  parameterEnabled: ParameterEnabled
  onConfigChange: <K extends keyof PlaygroundConfig>(
    key: K,
    value: PlaygroundConfig[K]
  ) => void
  onParameterEnabledChange: (
    key: keyof ParameterEnabled,
    value: boolean
  ) => void
}

export function PlaygroundInput({
  value,
  onValueChange,
  textareaRef,
  onSubmit,
  onStop,
  canSend,
  isGenerating,
  selectors,
  onNewChat,
  config,
  parameterEnabled,
  onConfigChange,
  onParameterEnabledChange,
}: PlaygroundInputProps) {
  const { t } = useTranslation()
  const canSubmit = canSend && value.trim().length > 0

  return (
    <PromptInput
      groupClassName={composerFrame}
      // PromptInput 提交时会先清空表单再回调；发不出去时在捕获阶段拦下，草稿原样保留
      onSubmitCapture={(event) => {
        if (canSubmit) return
        event.preventDefault()
        event.stopPropagation()
      }}
      onSubmit={(message) => {
        onSubmit(message.text ?? '')
        onValueChange('')
      }}
    >
      <PromptInputTextarea
        ref={textareaRef}
        autoComplete='off'
        autoCorrect='off'
        autoCapitalize='off'
        spellCheck={false}
        className='px-4 md:text-base'
        onChange={(event) => onValueChange(event.target.value)}
        placeholder={t('Ask anything')}
        value={value}
      />

      <PromptInputFooter className='items-end gap-2 p-2.5'>
        <div className='flex min-w-0 flex-1 flex-wrap items-center gap-1.5'>
          {selectors}
        </div>

        <div className='flex shrink-0 items-center gap-1.5'>
          {onNewChat && (
            <button
              type='button'
              onClick={onNewChat}
              disabled={isGenerating}
              aria-label={t('New chat')}
              className={composerChip}
            >
              <MessageSquarePlus className='size-3.5 shrink-0' />
              <span className='max-sm:sr-only'>{t('New chat')}</span>
            </button>
          )}
          <PlaygroundParameterPanel
            config={config}
            disabled={isGenerating}
            onConfigChange={onConfigChange}
            onParameterEnabledChange={onParameterEnabledChange}
            parameterEnabled={parameterEnabled}
          />
          {isGenerating ? (
            <button type='button' onClick={onStop} className={composerChip}>
              <SquareIcon className='size-3.5 shrink-0 fill-current' />
              <span className='max-sm:sr-only'>{t('Stop')}</span>
            </button>
          ) : (
            <button
              type='submit'
              disabled={!canSubmit}
              className={composerPrimary}
            >
              <SendIcon className='size-3.5 shrink-0' />
              <span className='max-sm:sr-only'>{t('Send')}</span>
            </button>
          )}
        </div>
      </PromptInputFooter>
    </PromptInput>
  )
}
