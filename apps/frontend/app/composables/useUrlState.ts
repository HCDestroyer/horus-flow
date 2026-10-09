/**
 * Estado de la página en la query string (frontend.md §4: filtros, orden, rango y pestaña en
 * la URL, **nunca IPs de clientes**). El valor por defecto no se escribe en la URL.
 */
export function useQueryParam<T extends string>(name: string, fallback: T, allowed?: readonly T[]) {
  const route = useRoute()
  const router = useRouter()
  return computed<T>({
    get() {
      const raw = route.query[name]
      const value = (Array.isArray(raw) ? raw[0] : raw) as T | undefined
      if (!value || (allowed && !allowed.includes(value))) return fallback
      return value
    },
    set(value) {
      const rest = Object.fromEntries(Object.entries(route.query).filter(([k]) => k !== name))
      router.replace({ query: value === fallback ? rest : { ...rest, [name]: value } })
    },
  })
}
