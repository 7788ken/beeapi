import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { isChatModel } from '@/lib/model-capabilities'
import { cn } from '@/lib/utils'
import { getRelayFailure, useRelayKey } from '@/hooks/use-relay-key'
import { composerChip } from '@/components/composer-styles'
import {
  KeyHasNoModelsNotice,
  KeyUnusableNotice,
  KeySelector,
  ModelSelector,
  NoUsableKeyNotice,
} from '@/components/key-model-selector'
import { PlaygroundChat } from './components/playground-chat'
import { PlaygroundInput } from './components/playground-input'
import { usePlaygroundState, useChatHandler } from './hooks'
import {
  chatColumn,
  createUserMessage,
  createLoadingAssistantMessage,
} from './lib'
import type { Message as MessageType } from './types'

// 空态的示例提示：点击只填进输入框，不直接发送
const EXAMPLE_PROMPTS = [
  'Explain this code step by step',
  'Draft a polite follow-up email',
  'Summarize the key points of an article',
  'Brainstorm 10 product name ideas',
]

export function Playground() {
  const { t } = useTranslation()
  const {
    config,
    parameterEnabled,
    messages,
    updateMessages,
    clearMessages,
    updateConfig,
    updateParameterEnabled,
  } = usePlaygroundState()

  // 先选 key，模型只列这把 key 能对话的
  const relay = useRelayKey(config.tokenId)
  const chatModels = useMemo(
    () => relay.models.filter(isChatModel).map((m) => ({ id: m.id })),
    [relay.models]
  )
  const hasModel = chatModels.some((m) => m.id === config.model)

  // 换 key 后当前模型不在新列表里，就切到第一个
  useEffect(() => {
    if (chatModels.length > 0 && !hasModel) {
      updateConfig('model', chatModels[0].id)
    }
  }, [chatModels, hasModel, updateConfig])

  const { sendChat, stopGeneration, isGenerating } = useChatHandler({
    config,
    parameterEnabled,
    secret: relay.secret,
    onMessageUpdate: updateMessages,
  })
  const canSend = !!relay.secret && hasModel && !isGenerating

  const [draft, setDraft] = useState('')
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  // Edit dialog state
  const [editingMessageKey, setEditingMessageKey] = useState<string | null>(
    null
  )

  const fillPrompt = (text: string) => {
    setDraft(text)
    textareaRef.current?.focus()
  }

  const handleSendMessage = (text: string) => {
    const newMessages = [
      ...messages,
      createUserMessage(text),
      createLoadingAssistantMessage(),
    ]
    updateMessages(newMessages)
    sendChat(newMessages)
  }

  const handleRegenerateMessage = (message: MessageType) => {
    // Find the message index and regenerate from there
    const messageIndex = messages.findIndex((m) => m.key === message.key)
    if (messageIndex === -1) return

    // Remove messages after this one and regenerate
    const messagesUpToHere = messages.slice(0, messageIndex)
    const loadingMessage = createLoadingAssistantMessage()
    const newMessages = [...messagesUpToHere, loadingMessage]

    updateMessages(newMessages)
    sendChat(newMessages)
  }

  const handleEditMessage = useCallback((message: MessageType) => {
    setEditingMessageKey(message.key)
  }, [])

  const handleEditOpenChange = useCallback((open: boolean) => {
    if (!open) setEditingMessageKey(null)
  }, [])

  // Apply edit and optionally re-submit from the edited user message
  const applyEdit = useCallback(
    (newContent: string, submit: boolean) => {
      if (!editingMessageKey) return
      const index = messages.findIndex((m) => m.key === editingMessageKey)
      if (index === -1) return

      const updated = messages.map((m) =>
        m.key === editingMessageKey
          ? { ...m, versions: [{ ...m.versions[0], content: newContent }] }
          : m
      )

      setEditingMessageKey(null)

      if (!submit || updated[index].from !== 'user') {
        updateMessages(updated)
        return
      }

      const toSubmit = [
        ...updated.slice(0, index + 1),
        createLoadingAssistantMessage(),
      ]
      updateMessages(toSubmit)
      sendChat(toSubmit)
    },
    [editingMessageKey, messages, updateMessages, sendChat]
  )

  const handleDeleteMessage = (message: MessageType) => {
    const newMessages = messages.filter((m) => m.key !== message.key)
    updateMessages(newMessages)
  }

  const hasMessages = messages.length > 0
  const failure = getRelayFailure(relay)
  const showNoKeyNotice =
    !relay.keysLoading && !relay.activeKey && !relay.keysError
  const showNoModelsNotice =
    !!relay.activeKey &&
    !relay.activeKeyUnusable &&
    !relay.modelsLoading &&
    !failure.error &&
    chatModels.length === 0

  const selectors = (
    <>
      <KeySelector
        keys={relay.keys}
        activeKey={relay.activeKey}
        onSelect={(id) => updateConfig('tokenId', id)}
        loading={relay.keysLoading}
        disabled={isGenerating}
      />
      <ModelSelector
        models={chatModels}
        value={config.model}
        onChange={(id) => updateConfig('model', id)}
        loading={relay.modelsLoading}
        error={failure.error}
        onRetry={failure.retry}
        disabled={isGenerating || (!relay.activeKey && !failure.error)}
        emptyText={t(
          'This key has no chat models. Pick another key or allow more models in the key settings.'
        )}
      />
    </>
  )

  return (
    <div className='relative flex size-full flex-col overflow-hidden'>
      {hasMessages && (
        <div className='flex min-h-0 flex-1 flex-col overflow-hidden'>
          <PlaygroundChat
            messages={messages}
            onRegenerateMessage={handleRegenerateMessage}
            onEditMessage={handleEditMessage}
            onDeleteMessage={handleDeleteMessage}
            isGenerating={isGenerating}
            canSend={canSend}
            editingKey={editingMessageKey}
            onCancelEdit={handleEditOpenChange}
            onSaveEdit={(newContent) => applyEdit(newContent, false)}
            onSaveEditAndSubmit={(newContent) => applyEdit(newContent, true)}
          />
        </div>
      )}

      {/* 空态整列垂直居中，有消息后输入卡吸底；输入卡始终挂在同一位置，发出第一条后焦点不丢 */}
      <div
        className={cn(
          // 消息滚动区（use-stick-to-bottom）两侧预留了滚动条位置，这里同样预留，输入卡与气泡左右对齐
          'flex flex-col [scrollbar-gutter:stable_both-edges]',
          hasMessages
            ? 'shrink-0 overflow-y-hidden'
            : 'min-h-0 flex-1 overflow-y-auto'
        )}
      >
        <div className={cn(chatColumn, hasMessages ? 'pb-4' : 'my-auto py-10')}>
          {!hasMessages && (
            <div className='text-center'>
              <h2 className='text-foreground text-2xl font-semibold'>
                {t('What can I help with?')}
              </h2>
              <p className='text-muted-foreground mt-2 text-sm text-balance'>
                {t(
                  'Pick one of your API keys and a model below. Usage is billed to that key.'
                )}
              </p>
            </div>
          )}

          <div className={cn(!hasMessages && 'mt-6')}>
            {showNoKeyNotice && (
              <div className='mb-3'>
                <NoUsableKeyNotice hasKeys={relay.keys.length > 0} />
              </div>
            )}
            {relay.activeKey && relay.activeKeyUnusable && (
              <div className='mb-3'>
                <KeyUnusableNotice
                  keyName={relay.activeKey.name}
                  reason={relay.activeKeyUnusable}
                />
              </div>
            )}
            {showNoModelsNotice && relay.activeKey && (
              <div className='mb-3'>
                <KeyHasNoModelsNotice
                  keyName={relay.activeKey.name}
                  kind='chat'
                />
              </div>
            )}
            <PlaygroundInput
              value={draft}
              onValueChange={setDraft}
              textareaRef={textareaRef}
              onSubmit={handleSendMessage}
              onStop={stopGeneration}
              canSend={canSend}
              isGenerating={isGenerating}
              selectors={selectors}
              onNewChat={hasMessages ? clearMessages : undefined}
              config={config}
              parameterEnabled={parameterEnabled}
              onConfigChange={updateConfig}
              onParameterEnabledChange={updateParameterEnabled}
            />
          </div>

          {!hasMessages && (
            <div className='mt-3 flex flex-wrap justify-center gap-2'>
              {EXAMPLE_PROMPTS.map((prompt) => (
                <button
                  key={prompt}
                  type='button'
                  className={composerChip}
                  onClick={() => fillPrompt(t(prompt))}
                >
                  {t(prompt)}
                </button>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
