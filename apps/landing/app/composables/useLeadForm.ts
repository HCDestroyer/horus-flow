// Lógica común de los formularios: validación zod traducida, antispam (honeypot + tiempo de
// llenado), envío y estados (enviando, error, éxito).
import type { FormError } from '@nuxt/ui'
import type { z } from 'zod'
import { COUNTRY_CODES, type SubmitError, type SubmitOk } from '#shared/schemas'

export function useCountryOptions() {
  const { t, locale } = useI18n()
  return computed(() => {
    let names: Intl.DisplayNames | null = null
    try {
      names = new Intl.DisplayNames([locale.value], { type: 'region' })
    } catch {
      names = null
    }
    const items = COUNTRY_CODES.filter((c) => c !== 'ZZ').map((code) => ({
      value: code,
      label: names?.of(code) ?? code,
    }))
    const [gt, ...rest] = items
    rest.sort((a, b) => a.label.localeCompare(b.label, locale.value))
    return [gt!, ...rest, { value: 'ZZ', label: t('form.otherCountry') }]
  })
}

export function useFormSubmission<S extends z.ZodType>(endpoint: string, schema: S) {
  const { t } = useI18n()
  const config = useRuntimeConfig()
  const startedAt = ref<number | undefined>()
  const honeypot = ref('')
  const submitting = ref(false)
  const error = ref<string | null>(null)
  const serverFields = ref<Record<string, string>>({})
  const result = ref<SubmitOk | null>(null)

  onMounted(() => {
    startedAt.value = Date.now()
  })

  function validate(state: unknown): FormError[] {
    const out: FormError[] = []
    const parsed = schema.safeParse(state)
    const seen = new Set<string>()
    if (!parsed.success) {
      for (const issue of parsed.error.issues) {
        const name = String(issue.path[0] ?? '')
        if (seen.has(name)) continue
        seen.add(name)
        const key = /^[a-zA-Z]+$/.test(issue.message) ? issue.message : 'invalid'
        out.push({ name, message: t(`errors.${key}`) })
      }
    }
    for (const [name, key] of Object.entries(serverFields.value)) {
      if (!seen.has(name)) out.push({ name, message: t(`errors.${key}`) })
    }
    return out
  }

  async function submit(data: Record<string, unknown>) {
    error.value = null
    serverFields.value = {}
    submitting.value = true
    try {
      const res = await $fetch<SubmitOk>(endpoint, {
        method: 'POST',
        body: { ...data, website: honeypot.value, startedAt: startedAt.value },
      })
      result.value = res
      return res
    } catch (err: unknown) {
      const data = (err as { data?: SubmitError }).data
      const email = config.public.salesEmail
      switch (data?.code) {
        case 'VALIDATION':
          serverFields.value = data.fields ?? {}
          error.value = t('errors.fixFields')
          break
        case 'TOO_FAST':
          error.value = t('errors.tooFast')
          break
        case 'RATE_LIMITED':
          error.value = t('errors.rateLimited')
          break
        case 'UNAVAILABLE':
          error.value = t('errors.unavailable', { email })
          break
        default:
          error.value = t('errors.server', { email })
      }
      return null
    } finally {
      submitting.value = false
    }
  }

  function reset() {
    result.value = null
    error.value = null
    startedAt.value = Date.now()
  }

  return { honeypot, submitting, error, result, validate, submit, reset }
}
