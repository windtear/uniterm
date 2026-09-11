<template>
  <el-dialog
    :model-value="updateCheck.updateDialogVisible"
    :title="t('settings.updateDialogTitle')"
    width="540px"
    :close-on-click-modal="false"
    :close-on-press-escape="!locked"
    :show-close="!locked"
    @update:model-value="(v: boolean) => { if (!v) updateCheck.closeUpdateDialog() }"
  >
    <div class="update-dialog">
      <div class="update-dialog-version">
        {{ t('settings.version') }} {{ updateCheck.updateInfo?.current || '...' }} → {{ updateCheck.updateInfo?.latest || '...' }}
      </div>

      <div v-if="updateCheck.channel === 'package'" class="update-dialog-hint">
        {{ t('settings.updatePackageManager') }}
      </div>

      <div v-if="changelogText" class="update-dialog-changelog">
        <pre>{{ changelogText }}</pre>
      </div>

      <div v-if="updateCheck.updatePhase === 'error'" class="update-dialog-error">
        {{ t('settings.updateFailed') }}: {{ updateCheck.updateError }}
      </div>

      <div v-if="updateCheck.updatePhase === 'downloading' || updateCheck.updatePhase === 'verifying'" class="update-dialog-progress">
        <el-progress :percentage="updateCheck.downloadProgress.percent" :stroke-width="10" :indeterminate="updateCheck.downloadProgress.total <= 0" />
        <div class="update-dialog-progress-label">
          {{ updateCheck.updatePhase === 'verifying' ? t('settings.updateVerifying') : t('settings.updateDownloading') }}
          <span v-if="updateCheck.updatePhase === 'downloading' && updateCheck.downloadProgress.received > 0" class="update-dialog-progress-size">
            ({{ fmtSize(updateCheck.downloadProgress.received) }}<template v-if="updateCheck.downloadProgress.total > 0"> / {{ fmtSize(updateCheck.downloadProgress.total) }}</template>)
          </span>
        </div>
      </div>

      <div v-if="updateCheck.updatePhase === 'applying'" class="update-dialog-progress-label">
        {{ t('settings.updateApplying') }}
      </div>
      <div v-if="updateCheck.updatePhase === 'restarting'" class="update-dialog-progress-label">
        {{ t('settings.updateRestarting') }}
      </div>
    </div>
    <template #footer>
      <el-button v-if="updateCheck.updatePhase === 'idle' || updateCheck.updatePhase === 'error'" @click="updateCheck.closeUpdateDialog()">
        {{ t('settings.updateLater') }}
      </el-button>
      <el-button
        v-if="(updateCheck.updatePhase === 'idle' || updateCheck.updatePhase === 'error') && updateCheck.channel !== 'package'"
        type="primary"
        @click="updateCheck.startUpdate()"
      >
        {{ t('settings.updateInstall') }}
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useUpdateCheck } from '../composables/useUpdateCheck'
import { useI18n } from '../i18n'

const { t } = useI18n()
const updateCheck = useUpdateCheck()

// Cannot dismiss the dialog while the binary is being replaced.
const locked = computed(() =>
  updateCheck.updatePhase === 'applying' || updateCheck.updatePhase === 'restarting'
)

// Release notes are markdown; render as plain text with a sane cap.
const changelogText = computed(() => {
  const body = updateCheck.updateInfo?.changelog || ''
  if (!body) return ''
  return body.length > 6000 ? body.slice(0, 6000) + '\n…' : body
})

function fmtSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB']
  let v = bytes
  let i = -1
  do {
    v /= 1024
    i++
  } while (v >= 1024 && i < units.length - 1)
  return `${v.toFixed(1)} ${units[i]}`
}
</script>

<style scoped>
.update-dialog-version {
  font-size: 14px;
  font-weight: 600;
  font-family: var(--font-ui);
  margin-bottom: 10px;
}
.update-dialog-hint {
  font-size: 13px;
  color: var(--text-muted, #8a8a8a);
  margin-bottom: 10px;
}
.update-dialog-changelog {
  max-height: 260px;
  overflow-y: auto;
  background: var(--bg-overlay);
  border: 1px solid var(--border-subtle);
  border-radius: 6px;
  padding: 10px 12px;
  margin-bottom: 12px;
}
.update-dialog-changelog pre {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-size: 12px;
  line-height: 1.55;
  font-family: var(--font-ui);
}
.update-dialog-error {
  color: #f56c6c;
  font-size: 13px;
  margin-bottom: 10px;
  word-break: break-word;
}
.update-dialog-progress-label {
  font-size: 13px;
  margin-top: 8px;
  color: var(--text-muted, #8a8a8a);
}
.update-dialog-progress-size {
  font-family: var(--font-mono);
}
</style>
