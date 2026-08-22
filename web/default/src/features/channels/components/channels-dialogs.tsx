import { useChannels } from './channels-provider'
import { BalanceQueryDialog } from './dialogs/balance-query-dialog'
import { ChannelHealthDialog } from './dialogs/channel-health-dialog'
import { ChannelQualityDialog } from './dialogs/channel-quality-dialog'
import { ChannelRatioChangesDialog } from './dialogs/channel-ratio-changes-dialog'
import { ChannelTestDialog } from './dialogs/channel-test-dialog'
import { ChannelVerifyDialog } from './dialogs/channel-verify-dialog'
import { CopyChannelDialog } from './dialogs/copy-channel-dialog'
import { EditTagDialog } from './dialogs/edit-tag-dialog'
import { FetchModelsDialog } from './dialogs/fetch-models-dialog'
import { MultiKeyManageDialog } from './dialogs/multi-key-manage-dialog'
import { OllamaModelsDialog } from './dialogs/ollama-models-dialog'
import { TagBatchEditDialog } from './dialogs/tag-batch-edit-dialog'
import { UpstreamUpdateDialog } from './dialogs/upstream-update-dialog'
import { ChannelMutateDrawer } from './drawers/channel-mutate-drawer'
import { UpstreamSyncDialog } from './upstream-sync-dialog'

export function ChannelsDialogs() {
  const { open, setOpen, currentRow, upstream } = useChannels()

  return (
    <>
      {/* Channel Create/Update Drawer */}
      <ChannelMutateDrawer
        open={open === 'create-channel' || open === 'update-channel'}
        onOpenChange={(v) => !v && setOpen(null)}
        currentRow={open === 'update-channel' ? currentRow : null}
      />

      {/* Test Channel Dialog */}
      <ChannelTestDialog
        open={open === 'test-channel'}
        onOpenChange={(v) => !v && setOpen(null)}
      />

      {/* Balance Query Dialog */}
      <BalanceQueryDialog
        open={open === 'balance-query'}
        onOpenChange={(v) => !v && setOpen(null)}
      />

      {/* Fetch Models Dialog */}
      <FetchModelsDialog
        open={open === 'fetch-models'}
        onOpenChange={(v) => !v && setOpen(null)}
      />

      {/* Ollama Models Dialog */}
      <OllamaModelsDialog
        open={open === 'ollama-models'}
        onOpenChange={(v) => !v && setOpen(null)}
      />

      {/* Copy Channel Dialog */}
      <CopyChannelDialog
        open={open === 'copy-channel'}
        onOpenChange={(v) => !v && setOpen(null)}
      />

      {/* Multi-Key Management Dialog */}
      <MultiKeyManageDialog
        open={open === 'multi-key-manage'}
        onOpenChange={(v) => !v && setOpen(null)}
      />

      {/* Tag Batch Edit Dialog */}
      <TagBatchEditDialog
        open={open === 'tag-batch-edit'}
        onOpenChange={(v) => !v && setOpen(null)}
      />

      {/* Edit Tag Dialog */}
      <EditTagDialog
        open={open === 'edit-tag'}
        onOpenChange={(v) => !v && setOpen(null)}
      />

      {/* Channel Health Dialog */}
      <ChannelHealthDialog
        open={open === 'channel-health'}
        onOpenChange={(v) => !v && setOpen(null)}
      />

      {/* Channel Verify Dialog (外部测评) */}
      <ChannelVerifyDialog
        open={open === 'channel-verify'}
        onOpenChange={(v) => !v && setOpen(null)}
      />

      {/* Channel Quality History Dialog (质量历史报表) */}
      <ChannelQualityDialog
        open={open === 'channel-quality'}
        onOpenChange={(v) => !v && setOpen(null)}
      />

      {/* 上游分组倍率变更明细弹窗 */}
      <ChannelRatioChangesDialog
        open={open === 'channel-ratio-changes'}
        onOpenChange={(v) => !v && setOpen(null)}
      />

      {/* Upstream Model Update Dialog */}
      <UpstreamUpdateDialog
        open={upstream.showModal}
        addModels={upstream.addModels}
        removeModels={upstream.removeModels}
        preferredTab={upstream.preferredTab}
        confirmLoading={upstream.applyLoading}
        onConfirm={upstream.applyUpdates}
        onCancel={upstream.closeModal}
      />

      {/* Sub-Site Sync Dialog（分站同步） */}
      <UpstreamSyncDialog
        open={open === 'upstream-sync'}
        onOpenChange={(v) => !v && setOpen(null)}
      />
    </>
  )
}
