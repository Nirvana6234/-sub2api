import { createApp, watch } from 'vue'
import App from './App.vue'
import './styles/globals.css'
import { i18n } from './i18n'
import { router } from './router'

const app = createApp(App)
const localeRef = i18n.global.locale as unknown as { value: string }

watch(
  () => localeRef.value,
  (currentLocale) => {
    document.documentElement.lang = currentLocale
  },
  { immediate: true },
)

const themeColor = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')
const syncThemeColor = () => {
  themeColor?.setAttribute('content', document.documentElement.classList.contains('dark') ? '#121212' : '#fafafa')
}
const themeObserver = new MutationObserver(syncThemeColor)
themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
syncThemeColor()

app.use(i18n)
app.use(router)
app.mount('#app')

