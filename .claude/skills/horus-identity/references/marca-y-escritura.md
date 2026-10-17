# Marca y escritura

## Logo

- Marca: ojo de Horus estilizado (almendra + pupila) en blanco sobre cuadrado redondeado
  **teal 700 `#0f766e`**, radio 7/32 del lado (`assets/logo-mark.svg`).
- Sobre fondo oscuro se mantiene el cuadrado teal 700 (no se cambia a 400).
- Tamaño mínimo 16 px; zona de respeto = 1/4 del lado.
- No rotar, no degradar, no cambiar el color del cuadrado por producto, no añadir sombras.
- Logotipo: "Horus" en la fuente del sistema, semibold, tracking −0,015 em, seguido del nombre
  del producto en peso regular y color atenuado: **Horus** Flow, **Horus** <Producto>.

## Productos del ecosistema

Todos comparten marca, color, materiales y estados. Lo que cambia es el nombre y el contenido.
Si un producto necesita identificarse a simple vista (p. ej. un selector de apps), usa el
nombre o un icono Lucide junto a la marca, no otro color.

## Voz

- Español neutro, *sentence case* (solo la primera letra en mayúscula).
- Frases que describen comportamiento observado, no culpa: "Escaneo del puerto 23 a 1 240 IPs
  en 5 min", no "Cliente malicioso".
- Inferencias con confianza y razón ("posible uso comercial · confianza media · …").
- Errores: qué pasó y qué hacer, sin códigos crudos como mensaje principal.
- Términos del sector en su forma habitual (flujo, IPFIX, ASN, handshake, PPPoE).

## Formato de datos

- Tasas en **bits/s** (kb/s, Mb/s, Gb/s); volumen en **bytes** (kB, MB, GB); SI decimal.
- Fechas `dd/mm/aaaa HH:mm`; relativas "hace 5 min", "desde 10:42".
- Miles con espacio fino o separador local; cifras tabulares.
- IPs, claves e IDs en monoespaciada. Las IPs de clientes nunca van en URLs.
- Huecos de datos se muestran como hueco, nunca como cero. Top N ≤ 10 + "Otros".
