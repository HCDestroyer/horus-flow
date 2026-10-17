import type { Customer, CustomerDetail } from '~~/types/api'

/**
 * Confirmar o descartar una sugerencia comercial del scoring (I2, frontend.md §8.1). Reutiliza
 * `POST /customers/{id}/set-kind`: confirmar fija `commercial` y descartar fija `residential`,
 * ambos a mano (candado: el scoring ya no cambia el tipo). El motivo obligatorio se compone con
 * la puntuación y los códigos de las razones principales. Ante `412` (el scoring cambió el tipo
 * mientras se revisaba) avisa y devuelve `null` para que la vista se refresque.
 */
export function useKindDecision() {
  const { t } = useI18n()
  const { $api } = useNuxtApp()
  const toast = useToast()
  const busy = ref<string | null>(null)

  async function decide(
    c: Customer,
    decision: 'confirm' | 'discard',
  ): Promise<CustomerDetail | null> {
    const kind = decision === 'confirm' ? 'commercial' : 'residential'
    const reason = decisionReason(decision, c, {
      confirm: t('clients.candidates.confirmReason'),
      discard: t('clients.candidates.discardReason'),
    })
    busy.value = c.id
    try {
      const updated = await unwrap(
        $api.POST('/customers/{customer_id}/set-kind', {
          params: { path: { customer_id: c.id }, header: { 'If-Match': ifMatch(c.version) } },
          body: { kind, reason },
        }),
      )
      toast.add({
        title: t(
          decision === 'confirm' ? 'clients.candidates.confirmed' : 'clients.candidates.discarded',
        ),
        icon: decision === 'confirm' ? 'i-lucide-building-2' : 'i-lucide-house',
        color: 'success',
      })
      return updated
    } catch (e) {
      if (e instanceof ApiError && e.status === 412) {
        toast.add({
          title: t('clients.candidates.conflict'),
          icon: 'i-lucide-git-compare',
          color: 'warning',
        })
        return null
      }
      throw e
    } finally {
      busy.value = null
    }
  }

  return { decide, busy }
}
