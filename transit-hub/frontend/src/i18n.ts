import { createI18n } from 'vue-i18n'
import enUS from './locales/en-US'
import zhCN from './locales/zh-CN'

type MessageSchema = typeof enUS

export const i18n = createI18n<[MessageSchema], 'en-US' | 'zh-CN'>({
  legacy: false, // you must set `false`, to use Composition API
  locale: 'zh-CN', // default locale
  fallbackLocale: 'en-US',
  messages: {
    'en-US': enUS,
    'zh-CN': zhCN
  }
})
