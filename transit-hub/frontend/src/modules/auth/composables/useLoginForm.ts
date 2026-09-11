import { computed, ref } from 'vue'

export type LoginMode = 'password' | 'key'

export function useLoginForm() {
  const mode = ref<LoginMode>('password')
  const account = ref('')
  const password = ref('')
  const apiKey = ref('')

  const payload = computed(() =>
    mode.value === 'password'
      ? {
          mode: 'password' as const,
          account: account.value,
          password: password.value,
        }
      : {
          mode: 'key' as const,
          apiKey: apiKey.value,
        },
  )

  const submit = () => payload.value

  return {
    mode,
    account,
    password,
    apiKey,
    payload,
    submit,
  }
}
