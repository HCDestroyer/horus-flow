// Formatos de fecha (frontend.md §13.4: `dd/mm/aaaa HH:mm`). Los mensajes viven en locales/es.json.
export default defineI18nConfig(() => ({
  legacy: false,
  fallbackLocale: 'es',
  datetimeFormats: {
    es: {
      short: { day: '2-digit', month: '2-digit', year: 'numeric' },
      long: {
        day: '2-digit',
        month: '2-digit',
        year: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false,
        timeZoneName: 'short',
      },
      time: { hour: '2-digit', minute: '2-digit', hour12: false },
    },
  },
  numberFormats: {
    es: {
      integer: { maximumFractionDigits: 0 },
    },
  },
}))
