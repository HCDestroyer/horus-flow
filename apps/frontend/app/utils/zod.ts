import * as z from 'zod'

/**
 * Zod configurado para la CSP de la consola (`script-src 'self'`, sin `'unsafe-eval'`): Zod 4
 * comprueba al primer uso si puede compilar validadores con `Function("")`, y aunque captura el
 * fallo, el navegador registra una violación de CSP en consola. `jitless` evita esa prueba; para
 * los esquemas pequeños de la consola no cambia nada. Importa `z` desde aquí, nunca de `zod`.
 */
z.config({ jitless: true })

export { z }
