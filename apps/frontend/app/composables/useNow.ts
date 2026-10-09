/**
 * Reloj compartido (un solo intervalo por pestaña, aunque lo usen 10 widgets): alimenta las
 * etiquetas "hace N s" y el reloj de `noc_header`. Se para cuando nadie lo usa.
 */
const now = ref(Date.now())
let users = 0
let timer: ReturnType<typeof setInterval> | undefined

export function useNow() {
  onMounted(() => {
    users++
    now.value = Date.now()
    timer ??= setInterval(() => {
      now.value = Date.now()
    }, 1000)
  })
  onBeforeUnmount(() => {
    users--
    if (users <= 0 && timer) {
      clearInterval(timer)
      timer = undefined
      users = 0
    }
  })
  return readonly(now)
}
