import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import {
  Branch,
  BranchMessages,
  BranchNext,
  BranchPage,
  BranchPrevious,
  BranchSelector,
} from '@/components/ai-elements/branch'
import {
  Conversation,
  ConversationContent,
  ConversationScrollButton,
} from '@/components/ai-elements/conversation'
import { Loader } from '@/components/ai-elements/loader'
import {
  Reasoning,
  ReasoningContent,
  ReasoningTrigger,
} from '@/components/ai-elements/reasoning'
import { Response } from '@/components/ai-elements/response'
import { Shimmer } from '@/components/ai-elements/shimmer'
import {
  Source,
  Sources,
  SourcesContent,
  SourcesTrigger,
} from '@/components/ai-elements/sources'
import { MESSAGE_ROLES } from '../constants'
import { assistantBubble, chatColumn, userBubble } from '../lib/message-styles'
import { parseThinkTags } from '../lib/message-utils'
import type { Message as MessageType, MessageVersion } from '../types'
import { MessageActions } from './message-actions'
import { MessageError } from './message-error'

interface PlaygroundChatProps {
  messages: MessageType[]
  onCopyMessage?: (message: MessageType) => void
  onRegenerateMessage?: (message: MessageType) => void
  onEditMessage?: (message: MessageType) => void
  onDeleteMessage?: (message: MessageType) => void
  isGenerating?: boolean
  /** 有 key、有模型、没在生成：重新生成、保存并提交才可用 */
  canSend?: boolean
  editingKey?: string | null
  onSaveEdit?: (newContent: string) => void
  onCancelEdit?: (open: boolean) => void
  onSaveEditAndSubmit?: (newContent: string) => void
}

/**
 * 气泡式消息流：我的消息主色气泡靠右，模型回复白卡片气泡靠左，两边都按 Markdown 渲染。
 * 版式参考 Telegram / iMessage 一类消息应用：消息列铺满可用宽度，气泡宽度按百分比自适应。
 */
export function PlaygroundChat({
  messages,
  onCopyMessage,
  onRegenerateMessage,
  onEditMessage,
  onDeleteMessage,
  isGenerating = false,
  canSend = true,
  editingKey,
  onSaveEdit,
  onCancelEdit,
  onSaveEditAndSubmit,
}: PlaygroundChatProps) {
  const { t } = useTranslation()
  const [editText, setEditText] = useState('')
  const [originalText, setOriginalText] = useState('')

  useEffect(() => {
    if (!editingKey) return
    const message = messages.find((m) => m.key === editingKey)
    const content = message?.versions?.[0]?.content || ''
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setEditText(content)

    setOriginalText(content)
  }, [editingKey, messages])

  const isEmpty = useMemo(() => !editText.trim(), [editText])
  const isChanged = useMemo(
    () => editText !== originalText,
    [editText, originalText]
  )

  return (
    <Conversation>
      <ConversationContent className='p-0'>
        <div className={cn(chatColumn, 'flex flex-col gap-4 py-6')}>
          {messages.map((message, messageIndex) => {
            const { versions = [] } = message
            const isUser = message.from === MESSAGE_ROLES.USER
            const isLastAssistantMessage =
              messageIndex === messages.length - 1 && !isUser
            return (
              <Branch defaultBranch={0} key={message.key}>
                <BranchMessages>
                  {versions.map((version, versionIndex) => (
                    <div
                      key={`${message.key}-${version.id}-${versionIndex}`}
                      className={cn(
                        'group flex w-full flex-col gap-1',
                        isUser ? 'items-end' : 'items-start'
                      )}
                    >
                      {editingKey === message.key ? (
                        <div className='w-full space-y-2 sm:max-w-[88%]'>
                          <Textarea
                            value={editText}
                            onChange={(e) => setEditText(e.target.value)}
                            className='font-mono text-sm'
                            rows={8}
                          />
                          <div
                            className={cn(
                              'flex gap-2',
                              isUser && 'justify-end'
                            )}
                          >
                            {/* Save & Submit only makes sense for user messages */}
                            {isUser && (
                              <Button
                                size='sm'
                                onClick={() => onSaveEditAndSubmit?.(editText)}
                                disabled={isEmpty || !isChanged || !canSend}
                              >
                                {t('Save & Submit')}
                              </Button>
                            )}
                            <Button
                              size='sm'
                              onClick={() => onSaveEdit?.(editText)}
                              disabled={isEmpty || !isChanged}
                            >
                              {t('Save')}
                            </Button>
                            <Button
                              size='sm'
                              variant='outline'
                              onClick={() => onCancelEdit?.(false)}
                            >
                              {t('Cancel')}
                            </Button>
                          </div>
                        </div>
                      ) : (
                        <>
                          {message.status === 'error' ? (
                            <MessageError
                              message={message}
                              className='w-full sm:max-w-[88%]'
                            />
                          ) : isUser ? (
                            <div className={userBubble}>
                              <Response>{version.content}</Response>
                            </div>
                          ) : (
                            <AssistantBubble
                              message={message}
                              version={version}
                            />
                          )}
                          <MessageActions
                            message={message}
                            onCopy={onCopyMessage}
                            onRegenerate={onRegenerateMessage}
                            onEdit={onEditMessage}
                            onDelete={onDeleteMessage}
                            isGenerating={isGenerating}
                            canRegenerate={canSend}
                            alwaysVisible={isLastAssistantMessage}
                          />
                        </>
                      )}
                    </div>
                  ))}
                </BranchMessages>

                {/* Branch selector for multiple versions */}
                {versions.length > 1 && (
                  <BranchSelector className='px-0' from={message.from}>
                    <BranchPrevious />
                    <BranchPage />
                    <BranchNext />
                  </BranchSelector>
                )}
              </Branch>
            )
          })}
        </div>
      </ConversationContent>
      <ConversationScrollButton className='rounded-lg' />
    </Conversation>
  )
}

/** 模型回复气泡：引用来源、思考过程、等待态、正文都收在同一张卡里 */
function AssistantBubble({
  message,
  version,
}: {
  message: MessageType
  version: MessageVersion
}) {
  const { t } = useTranslation()
  const hasSources = !!message.sources?.length
  const showReasoning = !!message.reasoning?.content
  const showLoader =
    !message.isReasoningStreaming &&
    (message.status === 'loading' ||
      (message.status === 'streaming' && !version.content))
  const showContent = !message.isReasoningStreaming && !!version.content

  return (
    <div className={assistantBubble}>
      {hasSources && (
        <Sources>
          <SourcesTrigger count={message.sources!.length} />
          <SourcesContent>
            {message.sources!.map((source, sourceIndex) => (
              <Source
                href={source.href}
                key={`${message.key}-source-${sourceIndex}`}
                title={source.title}
              />
            ))}
          </SourcesContent>
        </Sources>
      )}

      {showReasoning && (
        <Reasoning
          defaultOpen={true}
          isStreaming={message.isReasoningStreaming}
        >
          <ReasoningTrigger />
          <ReasoningContent>{message.reasoning!.content}</ReasoningContent>
        </Reasoning>
      )}

      {showLoader && (
        <div className='flex items-center gap-2 py-1'>
          <Loader />
          <Shimmer className='text-sm' duration={1}>
            {t('Responding...')}
          </Shimmer>
        </div>
      )}

      {/* 去掉 <think> 标签，只显示正文 */}
      {showContent && (
        <Response>{parseThinkTags(version.content).visibleContent}</Response>
      )}
    </div>
  )
}
