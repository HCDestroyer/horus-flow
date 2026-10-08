// @vitest-environment nuxt
import { mountSuspended } from '@nuxt/test-utils/runtime'
import { describe, expect, it } from 'vitest'
import CapabilityBadge from '~/components/status/CapabilityBadge.vue'
import ErrorState from '~/components/states/ErrorState.vue'
import TenantHeader from '~/components/tenant/TenantHeader.vue'
import { ApiError } from '~/utils/api-client'

describe('TenantHeader', () => {
  it('muestra el nombre del ISP con su monograma', async () => {
    const wrapper = await mountSuspended(TenantHeader, { props: { name: 'Fibra Norte' } })
    expect(wrapper.text()).toContain('FN')
    expect(wrapper.text()).toContain('Fibra Norte')
  })

  it('plegada conserva el nombre para lectores de pantalla', async () => {
    const wrapper = await mountSuspended(TenantHeader, {
      props: { name: 'Valle Conecta', collapsed: true },
    })
    expect(wrapper.find('.sr-only').text()).toContain('Valle Conecta')
  })
})

describe('CapabilityBadge', () => {
  it('expresa el estado con texto, no solo con color', async () => {
    const ok = await mountSuspended(CapabilityBadge, { props: { state: 'ok' } })
    const down = await mountSuspended(CapabilityBadge, { props: { state: 'unavailable' } })
    expect(ok.text()).toBe('Funciona')
    expect(down.text()).toBe('No disponible')
  })
})

describe('ErrorState', () => {
  it('muestra el mensaje, el detalle técnico plegado y emite retry', async () => {
    const error = new ApiError({
      status: 503,
      code: 'SERVICE_UNAVAILABLE',
      title: 'x',
      trace_id: 'abc123',
    })
    const wrapper = await mountSuspended(ErrorState, { props: { error } })
    expect(wrapper.text()).toContain('El servicio no está disponible')
    expect(wrapper.find('details').text()).toContain('abc123')
    await wrapper.find('button').trigger('click')
    expect(wrapper.emitted('retry')).toHaveLength(1)
  })
})
