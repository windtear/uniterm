import { reactive, ref, watch, h } from 'vue'
import { ElMessage } from 'element-plus'
import { msg } from '../services/message'
import { CheckForUpdate, GetAppInfo } from '../../bindings/github.com/ys-ll/uniterm/app'
import { useI18n, locale } from '../i18n'
import { useSettingsStore } from '../stores/settingsStore'
import { Browser } from '@wailsio/runtime'
import type { UpdateInfo } from '../types/settings'

function showUpdateNotification(info: UpdateInfo) {
  const { t } = useI18n()
  ElMessage({
    message: h('div', null, [
      `${t('settings.foundNewVersion')}: ${info.latest} `,
      h('a', {
        href: '#',
        style: 'color:inherit;text-decoration:underline;',
        onClick: (e: Event) => {
          e.preventDefault()
          Browser.OpenURL(info.releaseUrl)
        },
      }, t('settings.openRelease')),
    ]),
    type: 'success',
    duration: 0,
    showClose: true,
    offset: 50,
  })
}

const CHECK_TIMEOUT = 15000

const updateInfo = ref<UpdateInfo | null>(null)
const checking = ref(false)
const autoCheck = ref(true)

let timer: ReturnType<typeof setInterval> | null = null
let initialCheckTimer: ReturnType<typeof setTimeout> | null = null
let settingsWatchStop: (() => void) | null = null

function startTimer() {
  if (timer !== null) {
    clearInterval(timer)
  }
  timer = setInterval(() => {
    checkForUpdate()
  }, 24 * 60 * 60 * 1000)
}

function stopTimer() {
  if (initialCheckTimer !== null) {
    clearTimeout(initialCheckTimer)
    initialCheckTimer = null
  }
  if (timer !== null) {
    clearInterval(timer)
    timer = null
  }
}

async function checkForUpdate(showStatus = false): Promise<UpdateInfo | null> {
  checking.value = true
  const updateSource = locale.value === 'zh-CN' ? 'gitee' : 'github'
  try {
    const info = await Promise.race([
      CheckForUpdate(updateSource),
      new Promise<never>((_, reject) =>
        setTimeout(() => reject(new Error('timeout')), CHECK_TIMEOUT)
      ),
    ])
    updateInfo.value = info
    if (info.hasUpdate) {
      showUpdateNotification(info)
    } else if (showStatus) {
      const { t } = useI18n()
      msg.success(t('settings.upToDate'))
    }
    return info
  } catch {
    if (showStatus) {
      const { t } = useI18n()
      msg.error(t('settings.checkUpdateFailed'))
    }
    return null
  } finally {
    checking.value = false
  }
}

// Persist changes made through the UI. Store-driven updates already contain
// the same value and must not be written back.
watch(autoCheck, (enabled) => {
  const settings = useSettingsStore()
  if (settings.settings.autoCheckUpdate === enabled) return
  settings.settings.autoCheckUpdate = enabled
  settings.save()
})

function initAutoCheck() {
  checking.value = false
  stopTimer()
  settingsWatchStop?.()
  settingsWatchStop = null

  // Fetch current version immediately so About page shows it
  GetAppInfo().then(info => {
    if (!updateInfo.value) {
      updateInfo.value = { hasUpdate: false, current: info.version, latest: '', releaseUrl: '' }
    }
  }).catch(() => {})
  // Wait for persisted settings before scheduling network requests. Reading
  // the temporary default here would ignore a stored `false`.
  const settings = useSettingsStore()
  let initialized = false
  settingsWatchStop = watch(
    [() => settings.loaded, () => settings.settings.autoCheckUpdate],
    ([loaded, persisted]) => {
      if (!loaded) return
      const enabled = persisted ?? true
      autoCheck.value = enabled
      if (enabled) {
        if (!initialized) {
          initialCheckTimer = setTimeout(() => {
            initialCheckTimer = null
            checkForUpdate()
          }, 5000)
        }
        startTimer()
      } else {
        stopTimer()
      }
      initialized = true
    },
    { immediate: true },
  )
}

const state = reactive({
  updateInfo,
  checking,
  autoCheck,
  checkForUpdate,
  initAutoCheck,
  dispose,
})

function dispose() {
  stopTimer()
  settingsWatchStop?.()
  settingsWatchStop = null
}

if (typeof window !== 'undefined') {
  window.addEventListener('beforeunload', dispose)
}

export function useUpdateCheck() {
  return state
}
