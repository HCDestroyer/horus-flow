/*
 * Pantalla NOC ligera (kiosk-lite.html) para navegadores de TV antiguos: JavaScript ES5 sin
 * módulos, sin fetch ni Promise (XMLHttpRequest y callbacks). Mismo flujo que /kiosk:
 *   1. Sin credencial: código de 8 caracteres (o #code=… del enlace) → POST /kiosk/enroll
 *      (cookie de dispositivo __Secure-hf_kiosk).
 *   2. POST /kiosk/token → JWT de kiosco (10 min); GET /kiosk/config cada 60 s.
 *   3. Rota los dashboards de la lista con la misma distribución que la consola: cada widget
 *      ocupa su posición {x, y, w, h} de la rejilla de 12 columnas. Pantallas medianas
 *      reacomodan a 2 columnas y las estrechas o verticales apilan en una con scroll.
 *      Cada widget pide sus datos cada 30 s; los tipos conocidos tienen su propio dibujo
 *      (tráfico, clientes, hallazgos, exportadores, serie, tops, feed) y el resto uno genérico.
 * Sin datos personales extra: el servidor ya enmascara según show_personal_data.
 */
/* eslint-disable */
;(function () {
  'use strict'
  var API = '/api/v1'
  var SVGNS = 'http://www.w3.org/2000/svg'
  var DOWN = '#4f8ff7'
  var UP = '#f0743b'
  var token = null
  var tokenExpires = 0
  var config = null
  var dashboards = {}
  var index = 0
  var rotateTimer = null
  var dataTimer = null
  var failingSince = null
  var cards = []
  var mode = ''
  var slideStart = 0
  var slideMs = 0
  var lastUpdate = 0
  var liveFlip = false
  var reduceMotion = false
  try {
    reduceMotion = !!(window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches)
  } catch (e) {
    reduceMotion = false
  }

  function $(id) {
    return document.getElementById(id)
  }
  function show(id) {
    var ids = ['loading', 'enroll', 'player', 'empty']
    for (var i = 0; i < ids.length; i++) {
      var e = $(ids[i])
      if (ids[i] === id) e.className = e.className.replace(/\s*hidden/g, '')
      else if (e.className.indexOf('hidden') < 0) e.className += ' hidden'
    }
    if (id !== 'player') document.body.className = ''
  }
  function text(e, s) {
    e.textContent = s == null ? '' : String(s)
  }
  function el(tag, cls, content) {
    var e = document.createElement(tag)
    if (cls) e.className = cls
    if (content != null) text(e, content)
    return e
  }
  function add(parent, child) {
    parent.appendChild(child)
    return child
  }
  function svg(tag, attrs) {
    var e = document.createElementNS(SVGNS, tag)
    for (var k in attrs) if (attrs.hasOwnProperty(k)) e.setAttribute(k, attrs[k])
    return e
  }

  function request(method, path, body, cb) {
    var x = new XMLHttpRequest()
    x.open(method, API + path, true)
    x.withCredentials = true
    x.setRequestHeader('Accept', 'application/json')
    x.setRequestHeader('X-Requested-With', 'horus')
    if (token && path.indexOf('/kiosk/enroll') !== 0 && path.indexOf('/kiosk/token') !== 0) {
      x.setRequestHeader('Authorization', 'Bearer ' + token)
    }
    if (body) x.setRequestHeader('Content-Type', 'application/json')
    x.timeout = 20000
    x.onreadystatechange = function () {
      if (x.readyState !== 4) return
      var data = null
      try {
        data = x.responseText ? JSON.parse(x.responseText) : null
      } catch (e) {
        data = null
      }
      cb(x.status, data)
    }
    x.send(body ? JSON.stringify(body) : null)
  }

  function networkDown() {
    if (failingSince === null) failingSince = new Date()
    $('live').className = 'off'
    if (new Date() - failingSince > 60000) {
      var off = $('offline')
      text(off, 'Sin conexión desde las ' + hhmm(failingSince) + ' · mostrando los últimos datos')
      off.className = 'offline'
    }
  }
  function networkUp() {
    failingSince = null
    $('live').className = ''
    var off = $('offline')
    if (off.className.indexOf('hidden') < 0) off.className = 'offline hidden'
  }

  // --- Enrolamiento --------------------------------------------------------------------------
  function enroll(code) {
    var clean = String(code || '')
      .toUpperCase()
      .replace(/[^A-Z0-9]/g, '')
    if (clean.length !== 8) {
      text($('enroll-error'), 'El código tiene 8 caracteres')
      return
    }
    text($('enroll-error'), '')
    request('POST', '/kiosk/enroll', { code: clean }, function (status) {
      if (status === 204 || status === 200) {
        $('code').value = ''
        show('loading')
        sync()
      } else if (status === 429) {
        text($('enroll-error'), 'Demasiados intentos. Espera un minuto y genera un código nuevo.')
      } else if (status === 0) {
        text($('enroll-error'), 'No se puede conectar con Horus Flow. Revisa la red de esta pantalla.')
      } else {
        text($('enroll-error'), 'Ese código no es válido o caducó. Genera otro en Pantallas NOC.')
      }
    })
  }

  // --- Token y configuración ----------------------------------------------------------------
  function issueToken(cb) {
    request('POST', '/kiosk/token', null, function (status, data) {
      if (status === 401 || status === 403) return cb('revoked')
      if (status !== 200 || !data || !data.access_token) return cb('error')
      token = data.access_token
      tokenExpires = data.expires_at ? Date.parse(data.expires_at) : new Date().getTime() + 9 * 60000
      cb('ok')
    })
  }

  function sync() {
    if (!token || tokenExpires - new Date().getTime() < 2 * 60000) {
      issueToken(function (r) {
        if (r === 'revoked') return toEnroll()
        if (r === 'error') {
          networkDown()
          return later(sync, config ? 30000 : 5000)
        }
        loadConfig()
      })
    } else loadConfig()
  }

  function toEnroll() {
    token = null
    config = null
    clearTimeout(rotateTimer)
    clearInterval(dataTimer)
    show('enroll')
    $('code').focus()
  }

  function loadConfig() {
    request('GET', '/kiosk/config', null, function (status, data) {
      if (status === 401) {
        token = null
        return sync()
      }
      if (status !== 200 || !data) {
        networkDown()
        return later(sync, 30000)
      }
      networkUp()
      var first = config === null
      config = data
      text($('tenant'), data.tenant_name || '')
      loadDashboards(data.items || [], 0, function () {
        if (first) start()
        later(sync, 60000)
      })
    })
  }

  function loadDashboards(items, i, done) {
    if (i >= items.length) return done()
    var id = items[i].dashboard_id
    if (dashboards[id]) return loadDashboards(items, i + 1, done)
    request('GET', '/dashboards/' + id, null, function (status, data) {
      if (status === 200 && data) dashboards[id] = data
      loadDashboards(items, i + 1, done)
    })
  }

  function playable() {
    var out = []
    var items = (config && config.items) || []
    for (var i = 0; i < items.length; i++) {
      if (dashboards[items[i].dashboard_id]) out.push(items[i])
    }
    return out
  }

  // --- Reproductor -----------------------------------------------------------------------------
  function start() {
    var items = playable()
    if (!items.length) return show('empty')
    show('player')
    index = 0
    render()
  }

  function next() {
    var items = playable()
    index = (index + 1) % Math.max(1, items.length)
    if (reduceMotion) return render()
    var grid = $('grid')
    grid.className = 'grid fading'
    setTimeout(function () {
      render()
      grid.className = 'grid'
    }, 300)
  }

  function render() {
    var items = playable()
    if (!items.length) return show('empty')
    var item = items[index % items.length]
    var d = dashboards[item.dashboard_id]
    text($('dash-name'), d.name)
    var grid = $('grid')
    grid.innerHTML = ''
    cards = []
    var widgets = d.widgets || []
    for (var i = 0; i < widgets.length; i++) {
      var w = widgets[i]
      if (w.type === 'noc_header') continue
      var card = add(grid, el('section', 'card'))
      var inner = add(card, el('div'))
      add(inner, el('h2', null, w.title || label(w.type)))
      var body = add(inner, el('div', 'body'))
      add(body, el('p', 'nodata', 'Cargando…'))
      cards.push({ widget: w, card: card, inner: inner, body: body, data: null })
    }
    mode = ''
    layout()
    refreshData(d.id)
    clearInterval(dataTimer)
    dataTimer = setInterval(function () {
      refreshData(d.id)
    }, 30000)
    clearTimeout(rotateTimer)
    slideStart = new Date().getTime()
    slideMs = 0
    if (items.length > 1) {
      slideMs = Math.max(10, item.duration_seconds || 30) * 1000
      rotateTimer = setTimeout(next, slideMs)
    }
    tick()
  }

  function refreshData(dashId) {
    var list = cards
    for (var i = 0; i < list.length; i++) {
      ;(function (c) {
        var path = '/dashboards/' + dashId + '/widgets/' + encodeURIComponent(c.widget.id) + '/data'
        request('GET', path, null, function (status, data) {
          if (list !== cards) return
          if (status === 200 && data && data.data) {
            c.data = data.data
            var firstUpdate = !lastUpdate
            lastUpdate = new Date().getTime()
            draw(c)
            heartbeat()
            if (firstUpdate) tick()
          } else if (status !== 0 && c.body.firstChild && c.body.firstChild.className === 'nodata')
            text(c.body.firstChild, 'Sin datos')
        })
      })(list[i])
    }
  }

  // --- Distribución ----------------------------------------------------------------------------
  // Igual que la consola: rejilla de 12 columnas con las posiciones del dashboard, escalada a la
  // pantalla. 700–1199 px: 2 columnas (los widgets anchos ocupan la fila). <700 px o vertical:
  // una columna apilada con su altura natural y scroll.
  function layout() {
    var W = window.innerWidth || document.documentElement.clientWidth
    var H = window.innerHeight || document.documentElement.clientHeight
    var m = W < 700 || H > W ? 'stack' : W < 1200 ? 'mid' : 'wide'
    var fs = m === 'stack' ? Math.max(14, Math.round(W / 28)) : Math.max(14, Math.round(H / 45))
    document.body.style.fontSize = fs + 'px'
    document.body.className = m === 'stack' ? 'stack' : ''
    var barH = Math.round(fs * 2.8)
    var bar = $('bar')
    bar.style.height = barH + 'px'
    bar.style.lineHeight = barH + 'px'
    $('progress').style.top = barH + 'px'
    var grid = $('grid')
    var top = barH + 3
    var pad = Math.round(fs * 0.6)
    grid.style.top = m === 'stack' ? '0' : top + 'px'
    grid.style.padding = '0 ' + pad + 'px ' + pad + 'px'
    var avail = H - top - pad
    var i, c
    if (m === 'stack') {
      grid.style.height = 'auto'
      for (i = 0; i < cards.length; i++) place(cards[i].card, '', '', '', '')
    } else if (m === 'wide') {
      grid.style.height = avail + 'px'
      var minY = 1e9
      var rows = 0
      for (i = 0; i < cards.length; i++) minY = Math.min(minY, pos(cards[i].widget).y)
      for (i = 0; i < cards.length; i++) {
        var p = pos(cards[i].widget)
        rows = Math.max(rows, p.y - minY + p.h)
      }
      rows = Math.max(1, rows)
      for (i = 0; i < cards.length; i++) {
        var q = pos(cards[i].widget)
        place(
          cards[i].card,
          pct(q.x, 12),
          pct(q.w, 12),
          Math.round(((q.y - minY) / rows) * avail) + 'px',
          Math.round((q.h / rows) * avail) + 'px'
        )
      }
    } else {
      // Orden de lectura (fila y columna) y dos por fila salvo los anchos.
      var order = cards.slice(0)
      order.sort(function (a, b) {
        var pa = pos(a.widget)
        var pb = pos(b.widget)
        return pa.y - pb.y || pa.x - pb.x
      })
      var slots = []
      var col = 0
      var row = -1
      for (i = 0; i < order.length; i++) {
        var p2 = pos(order[i].widget)
        var nextFull = i + 1 >= order.length || pos(order[i + 1].widget).w >= 7
        // Una tarjeta media sin pareja (le sigue una ancha o es la última) ocupa la fila.
        var full = p2.w >= 7 || (col === 0 && nextFull)
        if (full || col === 0) row++
        slots.push({ c: order[i], row: row, col: full ? 0 : col, full: full, h: p2.h })
        col = full || col === 1 ? 0 : 1
      }
      // Alto de cada fila según el h del widget más alto (como en la rejilla de 12 columnas).
      var unit = Math.max(avail / 9, fs * 4)
      var rowH = []
      for (i = 0; i < slots.length; i++) rowH[slots[i].row] = Math.max(rowH[slots[i].row] || 0, slots[i].h * unit)
      var rowTop = [0]
      for (i = 0; i < rowH.length; i++) rowTop[i + 1] = rowTop[i] + Math.round(rowH[i])
      var total = rowTop[rowH.length]
      document.body.className = total > avail + 1 ? 'scroll' : ''
      grid.style.height = total + 'px'
      for (i = 0; i < slots.length; i++) {
        var s = slots[i]
        place(s.c.card, s.full ? '0' : s.col * 50 + '%', s.full ? '100%' : '50%', rowTop[s.row] + 'px', Math.round(rowH[s.row]) + 'px')
      }
    }
    if (m !== mode) {
      mode = m
      for (i = 0; i < cards.length; i++) {
        c = cards[i]
        if (c.data) draw(c)
      }
    }
  }
  function pos(w) {
    var p = w.position || {}
    return {
      x: num(p.x, 0),
      y: num(p.y, 0),
      w: Math.max(1, Math.min(12, num(p.w, 4))),
      h: Math.max(1, num(p.h, 2))
    }
  }
  function num(v, d) {
    return typeof v === 'number' && isFinite(v) ? v : d
  }
  function pct(n, of) {
    return (n / of) * 100 + '%'
  }
  function place(card, left, width, top, height) {
    var s = card.style
    s.left = left
    s.width = width
    s.top = top
    s.height = height
  }

  // --- Dibujo ----------------------------------------------------------------------------------
  // Latido de datos: el punto "En vivo" lanza un anillo con cada dato nuevo.
  function heartbeat() {
    var dot = $('live').firstChild
    liveFlip = !liveFlip
    if (dot) dot.className = 'dot ' + (liveFlip ? 'ring-a' : 'ring-b')
  }

  function draw(c) {
    var body = c.body
    var data = c.data
    body.innerHTML = ''
    drawing = c
    c.slot = 0
    c.inner.className = ''
    var v = data.values || {}
    var rows = data.rows || []
    try {
      switch (c.widget.type) {
        case 'traffic_now':
          if (data.kind === 'state') return drawTrafficNow(body, v)
          break
        case 'customers_active':
          if (data.kind === 'state') return drawCustomers(body, v)
          break
        case 'findings_summary':
          if (data.kind === 'state') return drawFindingsSummary(body, v)
          break
        case 'exporters_status':
          if (data.kind === 'table') return drawExporters(body, rows)
          break
        case 'routers_status_grid':
          if (data.kind === 'table') return drawRouters(body, rows)
          break
        case 'traffic_timeseries':
          if (data.kind === 'series') return drawChart(c, data.series || [])
          break
        case 'top_categories':
        case 'top_services':
        case 'top_organizations':
          if (data.kind === 'table') return drawTop(body, rows)
          break
        case 'top_customers':
          if (data.kind === 'table') return drawTopCustomers(body, rows)
          break
        case 'findings_feed':
          if (data.kind === 'table') return drawFeed(body, rows)
          break
      }
    } catch (e) {
      body.innerHTML = ''
    }
    if (data.kind === 'state') return drawState(body, v)
    if (data.kind === 'series') return drawChart(c, data.series || [])
    if (data.kind === 'table') return drawTable(body, data)
    nodata(body)
  }
  function nodata(body) {
    add(body, el('p', 'nodata', 'Sin datos'))
  }

  // Número grande con su unidad aparte ("2.4 Gbps"). Los dígitos ruedan desde el valor
  // anterior del mismo widget y el número da un destello si cambió.
  var drawing = null
  function bigValue(parent, cls, value, unit, arrow) {
    var c = drawing
    var key = c ? c.slot++ : 0
    var prev = c && c.prev ? c.prev[key] : null
    var b = add(parent, el('div', cls))
    if (arrow) add(b, el('span', 'arrow', arrow))
    roll(b, String(value), prev)
    if (unit) {
      b.appendChild(document.createTextNode(' '))
      add(b, el('span', 'unit', unit))
    }
    if (c) {
      if (!c.prev) c.prev = {}
      if (prev !== null && prev !== undefined && prev !== String(value)) {
        c.flip = !c.flip
        b.className = cls + (c.flip ? ' ping-a' : ' ping-b')
      }
      c.prev[key] = String(value)
    }
    return b
  }
  // Cada dígito es un span con su texto real (transparente) y una tira 0–9 en :before que
  // se desplaza (CSS). Se crea en el dígito anterior y se mueve al nuevo en el siguiente ciclo.
  function roll(parent, value, prev) {
    var moves = []
    for (var i = 0; i < value.length; i++) {
      var ch = value.charAt(i)
      if (!/\d/.test(ch)) {
        parent.appendChild(document.createTextNode(ch))
        continue
      }
      var j = prev ? prev.length - (value.length - i) : -1
      var from = j >= 0 && /\d/.test(prev.charAt(j)) ? prev.charAt(j) : ch
      var d = add(parent, el('span', 'rd d' + from, ch))
      if (from !== ch) moves.push([d, ch])
    }
    if (moves.length)
      setTimeout(function () {
        for (var k = 0; k < moves.length; k++) moves[k][0].className = 'rd d' + moves[k][1]
      }, 40)
  }
  function split(s) {
    var i = s.indexOf(' ')
    return i < 0 ? [s, ''] : [s.slice(0, i), s.slice(i + 1)]
  }
  function delta(now, before) {
    if (typeof now !== 'number' || typeof before !== 'number' || before <= 0) return '—'
    var d = Math.round(((now - before) / before) * 100)
    return (d > 0 ? '+' : d < 0 ? '−' : '±') + Math.abs(d) + ' %'
  }

  function drawTrafficNow(body, v) {
    var k = add(body, el('div', 'kpi'))
    var d = split(format('bps', v.down_bps))
    var u = split(format('bps', v.up_bps))
    bigValue(k, 'big', d[0], d[1], '↓')
    bigValue(k, 'mid', u[0], u[1], '↑')
    add(body, el('p', 'sub', 'vs. ayer ↓ ' + delta(v.down_bps, v.down_bps_yesterday) + ' · ↑ ' + delta(v.up_bps, v.up_bps_yesterday)))
  }

  function drawCustomers(body, v) {
    bigValue(body, 'big', format('', v.active), '')
    var parts = []
    if (typeof v.new_today === 'number') parts.push('+' + format('', v.new_today) + ' nuevos hoy')
    if (typeof v.total === 'number') parts.push('de ' + format('', v.total) + ' registrados')
    add(body, el('p', 'sub', parts.join(' · ')))
  }

  function drawFindingsSummary(body, v) {
    // Halo fijo cuando hay hallazgos críticos abiertos (la luz marca señales).
    if ((v.open_by_severity || {}).critical > 0 && drawing) drawing.inner.className = 'halo'
    var k = add(body, el('div', 'kpi'))
    bigValue(k, 'big', format('', v.open_total), '')
    var chips = add(k, el('div', 'chips'))
    var sev = v.open_by_severity || {}
    var line = add(chips, el('div'))
    chip(line, 'critical', 'Crítica', sev.critical || 0)
    chip(line, 'high', 'Alta', sev.high || 0)
    chip(line, 'medium', 'Media', sev.medium || 0)
    var st = v.by_security_state || {}
    line = add(chips, el('div'))
    chip(line, 'infected', 'Infectado', st.infected || 0)
    chip(line, 'suspected', 'Sospechoso', st.suspected || 0)
  }
  function chip(parent, cls, name, n) {
    var s = add(parent, el('span', cls))
    add(s, el('span', 'dot'))
    s.appendChild(document.createTextNode(name))
    add(s, el('b', null, format('', n)))
  }

  var EXP_STATE = {
    exporting: ['ok', 'Exportando'],
    lossy: ['warn', 'Con pérdida'],
    clock_skew: ['warn', 'Reloj desfasado'],
    silent: ['bad', 'Sin flujos'],
    pending_configuration: ['warn', 'Sin configurar']
  }
  var EXP_ORDER = { silent: 0, clock_skew: 1, lossy: 2, pending_configuration: 3 }
  function drawExporters(body, rows) {
    if (!rows.length) return nodata(body)
    var ok = 0
    for (var i = 0; i < rows.length; i++) if (rows[i].state === 'exporting') ok++
    var head = add(body, el('p', 'sub'))
    add(head, el('b', ok === rows.length ? 'ok' : 'warn', ok + ' de ' + rows.length))
    head.appendChild(document.createTextNode(' exportando'))
    var list = rows.slice(0)
    list.sort(function (a, b) {
      var oa = EXP_ORDER.hasOwnProperty(a.state) ? EXP_ORDER[a.state] : 9
      var ob = EXP_ORDER.hasOwnProperty(b.state) ? EXP_ORDER[b.state] : 9
      return oa - ob
    })
    var box = add(body, el('div', 'exp'))
    for (var j = 0; j < list.length; j++) {
      var r = list[j]
      var st = EXP_STATE[r.state] || ['warn', label(r.state)]
      var s = add(box, el('span'))
      add(s, el('span', 'dot ' + st[0]))
      add(s, el('b', null, r.router || '—')).style.color = '#e8edf3'
      var extra = st[1]
      if (r.state === 'exporting' && typeof r.flows_per_second === 'number')
        extra = format('', r.flows_per_second) + ' flujos/s'
      s.appendChild(document.createTextNode(' · ' + extra))
    }
  }

  // Estado de routers (SNMP): resumen "N de M en línea" y una línea por router, problemas
  // primero: punto de color + nombre + estado (y razón) + CPU + temperatura + bajada.
  var RT_STATE = {
    online: ['ok', 'En línea'],
    warning: ['warn', 'Aviso'],
    critical: ['bad', 'Crítico'],
    degraded: ['warn', 'Degradado'],
    offline: ['bad', 'Fuera de línea'],
    stale: ['warn', 'Sin datos recientes'],
    unknown: ['warn', 'Nunca observado'],
    maintenance: ['warn', 'Mantenimiento']
  }
  var RT_ORDER = { critical: 0, offline: 1, degraded: 2, warning: 3, stale: 4, unknown: 5, maintenance: 6 }
  var RT_REASON = {
    cpu_high: 'CPU alta',
    memory_high: 'memoria alta',
    temperature_high: 'temperatura alta',
    snmp_unreachable: 'SNMP no responde',
    snmp_auth_failed: 'credenciales SNMP',
    tunnel_down: 'túnel caído',
    host_unreachable_via_tunnel: 'no responde por el túnel',
    exporter_silent: 'exportador silencioso',
    interface_down: 'interfaz caída',
    psu_failed: 'fuente fallida',
    fan_failed: 'ventilador fallido'
  }
  function drawRouters(body, rows) {
    if (!rows.length) return nodata(body)
    var ok = 0
    for (var i = 0; i < rows.length; i++) if (rows[i].status === 'online') ok++
    var head = add(body, el('p', 'sub'))
    add(head, el('b', ok === rows.length ? 'ok' : 'warn', ok + ' de ' + rows.length))
    head.appendChild(document.createTextNode(' en línea'))
    var list = rows.slice(0)
    list.sort(function (a, b) {
      var oa = RT_ORDER.hasOwnProperty(a.status) ? RT_ORDER[a.status] : 9
      var ob = RT_ORDER.hasOwnProperty(b.status) ? RT_ORDER[b.status] : 9
      return oa - ob || String(a.router || '').localeCompare(String(b.router || ''))
    })
    list = list.slice(0, 8)
    var t = add(body, el('table'))
    for (var j = 0; j < list.length; j++) {
      var r = list[j]
      var st = RT_STATE[r.status] || ['warn', label(r.status)]
      var tr = add(t, el('tr'))
      var who = add(tr, el('td'))
      add(who, el('span', 'dot ' + st[0]))
      who.appendChild(document.createTextNode(' ' + (r.router || '—')))
      var what = st[1]
      if (r.reason && r.reason !== 'ok') what += ' · ' + (RT_REASON[r.reason] || label(r.reason))
      add(tr, el('td', st[0], what))
      add(tr, el('td', 'num', typeof r.cpu_percent === 'number' ? 'CPU ' + Math.round(r.cpu_percent) + ' %' : '—'))
      add(tr, el('td', 'num', typeof r.temperature_celsius === 'number' ? Math.round(r.temperature_celsius) + ' °C' : '—'))
      add(tr, el('td', 'num', typeof r.rx_bps === 'number' ? '↓ ' + format('bps', r.rx_bps) : '—'))
    }
  }

  function bytesOf(r) {
    if (typeof r.down_bytes === 'number' || typeof r.up_bytes === 'number')
      return (r.down_bytes || 0) + (r.up_bytes || 0)
    return typeof r.bytes === 'number' ? r.bytes : 0
  }
  function drawTop(body, rows) {
    rows = rows.slice(0, 8)
    if (!rows.length) return nodata(body)
    var max = 0
    for (var i = 0; i < rows.length; i++) max = Math.max(max, bytesOf(rows[i]))
    var box = add(body, el('div', 'bars'))
    for (var j = 0; j < rows.length; j++) {
      var b = bytesOf(rows[j])
      var row = add(box, el('div', 'row'))
      var lbl = add(row, el('div', 'lbl'))
      add(lbl, el('span', 'v', format('bytes', b)))
      lbl.appendChild(document.createTextNode(rows[j].label || '—'))
      var fill = add(add(row, el('div', 'track')), el('div', 'fill'))
      fill.style.width = (max ? Math.max(2, (b / max) * 100) : 0) + '%'
    }
  }

  function drawTopCustomers(body, rows) {
    rows = rows.slice(0, 8)
    if (!rows.length) return nodata(body)
    var t = add(body, el('table'))
    for (var i = 0; i < rows.length; i++) {
      var r = rows[i]
      var tr = add(t, el('tr'))
      var who = add(tr, el('td'))
      who.appendChild(document.createTextNode(r.alias || r.customer_ip || '—'))
      if (r.alias && r.customer_ip) add(who, el('span', 'ip', r.customer_ip))
      add(tr, el('td', 'num', format('bytes', bytesOf(r)))).style.width = '6.5em'
    }
  }

  var SEVERITY = { critical: 'Crítica', high: 'Alta', medium: 'Media', low: 'Baja', info: 'Info' }
  function drawFeed(body, rows) {
    rows = rows.slice(0, 8)
    if (!rows.length) return add(body, el('p', 'nodata', 'Sin hallazgos recientes'))
    var t = add(body, el('table'))
    for (var i = 0; i < rows.length; i++) {
      var r = rows[i]
      var tr = add(t, el('tr'))
      add(tr, el('td', 'sev ' + (r.severity || ''), SEVERITY[r.severity] || label(r.severity)))
      var what = add(tr, el('td'))
      what.appendChild(document.createTextNode(r.summary || label(r.kind)))
      if (r.alias || r.customer_ip) add(what, el('span', 'ip', r.alias || r.customer_ip))
      add(tr, el('td', 'when', ago(r.last_seen_at)))
    }
  }

  // Serie temporal: bajada y subida con rejilla, eje de valores y horas; los null son huecos.
  function drawChart(c, series) {
    var body = c.body
    var ss = []
    for (var i = 0; i < series.length && ss.length < 3; i++)
      if (series[i] && series[i].points && series[i].points.length) ss.push(series[i])
    if (!ss.length) return nodata(body)
    var legend = add(body, el('div', 'legend'))
    var max = 0
    var unit = ss[0].unit || ''
    for (i = 0; i < ss.length; i++) {
      var last = null
      for (var j = 0; j < ss[i].points.length; j++) {
        var v = ss[i].points[j][1]
        if (typeof v === 'number') {
          if (v > max) max = v
          last = v
        }
      }
      var item = add(legend, el('span'))
      add(item, el('span', 'swatch')).style.background = color(ss[i], i)
      item.appendChild(document.createTextNode(seriesName(ss[i].metric) + ' '))
      add(item, el('b', null, format(ss[i].unit || ss[i].metric, last)))
    }
    var fs = parseFloat(document.body.style.fontSize) || 16
    var W = Math.max(200, body.clientWidth || c.inner.clientWidth - fs * 1.8)
    var H
    if (mode === 'stack') H = Math.round(W * 0.45)
    else {
      var bottom = c.inner.offsetTop + c.inner.offsetHeight - fs * 0.7 - 1
      H = Math.round(bottom - (legend.offsetTop + legend.offsetHeight) - fs * 0.3)
    }
    H = Math.max(80, H)
    var s = svg('svg', { 'class': 'chart', width: W, height: H, viewBox: '0 0 ' + W + ' ' + H })
    var axisW = Math.round(fs * 3.6)
    var labH = Math.round(fs * 1.3)
    var tf = Math.round(fs * 0.75)
    var top = Math.round(tf * 0.7)
    var ph = H - labH - top
    var pw = W - axisW
    var nice = niceMax(max)
    for (var g = 0; g <= 4; g++) {
      var gy = Math.round(top + ph - (g / 4) * ph) + 0.5
      s.appendChild(svg('line', { x1: axisW, x2: W, y1: gy, y2: gy, stroke: '#2a2e36', 'stroke-width': 1 }))
      var t = svg('text', { x: axisW - 6, y: gy + tf * 0.35, fill: '#9aa5b4', 'font-size': tf, 'text-anchor': 'end' })
      t.textContent = g === 0 ? '0' : format(unit, (nice * g) / 4)
      s.appendChild(t)
    }
    var pts = ss[0].points
    var n = pts.length
    var hours = Math.min(6, Math.max(1, n - 1))
    for (var h = 0; h <= hours; h++) {
      var idx = Math.round((h / hours) * (n - 1))
      var d = toDate(pts[idx] && pts[idx][0])
      if (!d) continue
      var anchor = h === 0 ? 'start' : h === hours ? 'end' : 'middle'
      var hl = svg('text', { x: axisW + (n > 1 ? (idx / (n - 1)) * pw : 0), y: H - 2, fill: '#9aa5b4', 'font-size': tf, 'text-anchor': anchor })
      hl.textContent = hhmm(d)
      s.appendChild(hl)
    }
    for (i = 0; i < ss.length; i++) {
      var p = ss[i].points
      var seg = []
      for (j = 0; j <= p.length; j++) {
        var y = j < p.length ? p[j][1] : null
        if (typeof y === 'number') {
          var x = axisW + (p.length > 1 ? (j / (p.length - 1)) * pw : pw / 2)
          seg.push(x.toFixed(1) + ',' + (top + ph - (nice ? (y / nice) * ph : 0)).toFixed(1))
        } else if (seg.length) {
          if (seg.length === 1) seg.push(seg[0])
          // Halo: la misma línea más ancha y tenue debajo (sin filtros SVG, ligeros en TVs).
          var sw = Math.max(2, Math.round(fs / 8))
          s.appendChild(svg('polyline', { points: seg.join(' '), fill: 'none', stroke: color(ss[i], i), 'stroke-width': sw * 4, 'stroke-linejoin': 'round', 'stroke-linecap': 'round', opacity: '0.14', 'class': 'glow' }))
          s.appendChild(svg('polyline', { points: seg.join(' '), fill: 'none', stroke: color(ss[i], i), 'stroke-width': sw, 'stroke-linejoin': 'round' }))
          seg = []
        }
      }
    }
    body.appendChild(s)
  }
  function color(s, i) {
    if (/up|upload|tx/.test(s.metric || '')) return UP
    if (/down|download|rx/.test(s.metric || '')) return DOWN
    return [DOWN, UP, '#3fb97f'][i % 3]
  }
  function seriesName(m) {
    if (/up|upload/.test(m || '')) return 'Subida'
    if (/down|download/.test(m || '')) return 'Bajada'
    return label(m)
  }
  function niceMax(v) {
    if (!(v > 0)) return 1
    var e = Math.pow(10, Math.floor(Math.log(v) / Math.LN10))
    var f = v / e
    var nf = f <= 1 ? 1 : f <= 2 ? 2 : f <= 4 ? 4 : f <= 5 ? 5 : 10
    return nf * e
  }

  // Genéricos para widgets sin dibujo propio.
  function drawState(body, values) {
    var dl = el('dl', 'kv')
    var n = 0
    for (var k in values) {
      if (!values.hasOwnProperty(k) || n >= 6) continue
      var v = values[k]
      if (v !== null && typeof v === 'object') continue
      var row = add(dl, el('div'))
      add(row, el('dt', null, label(k)))
      add(row, el('dd', null, format(k, v)))
      n++
    }
    if (!n) return nodata(body)
    body.appendChild(dl)
  }

  function drawTable(body, data) {
    var cols = (data.columns || []).slice(0, 4)
    var rows = (data.rows || []).slice(0, 8)
    if (!rows.length) return nodata(body)
    var t = el('table')
    var head = add(t, el('tr'))
    var i
    for (i = 0; i < cols.length; i++) {
      var numH = /number|bytes|bps|percent/.test(cols[i].type)
      add(head, el('th', numH ? 'num' : null, label(cols[i].key)))
    }
    for (var r = 0; r < rows.length; r++) {
      var tr = add(t, el('tr'))
      for (var c = 0; c < cols.length; c++) {
        var isNum = /number|bytes|bps|percent/.test(cols[c].type)
        add(tr, el('td', isNum ? 'num' : null, format(cols[c].type || cols[c].key, rows[r][cols[c].key])))
      }
    }
    body.appendChild(t)
  }

  var LABELS = {
    traffic_now: 'Tráfico ahora',
    customers_active: 'Clientes activos',
    findings_summary: 'Hallazgos abiertos',
    exporters_status: 'Exportadores',
    routers_status_grid: 'Routers',
    traffic_timeseries: 'Tráfico',
    top_categories: 'Top categorías',
    top_services: 'Top servicios',
    top_organizations: 'Top organizaciones',
    top_customers: 'Top clientes',
    findings_feed: 'Hallazgos recientes',
    down_bps: 'Bajada',
    up_bps: 'Subida',
    active: 'Activos',
    total: 'Total',
    open: 'Abiertos',
    critical: 'Críticos',
    high: 'Altos',
    medium: 'Medios',
    low: 'Bajos',
    exporting: 'Exportando',
    silent: 'Silenciosos',
    label: 'Nombre',
    bytes: 'Volumen',
    share: 'Cuota',
    severity: 'Severidad',
    summary: 'Resumen',
    customer: 'Cliente',
    address: 'IP',
    site: 'Nodo',
    last_seen_at: 'Última vez'
  }
  function label(k) {
    if (LABELS[k]) return LABELS[k]
    var s = String(k || '').replace(/_/g, ' ')
    return s.charAt(0).toUpperCase() + s.slice(1)
  }
  function format(kind, v) {
    if (v === null || v === undefined) return '—'
    if (typeof v === 'boolean') return v ? 'Sí' : 'No'
    if (typeof v !== 'number') return String(v)
    if (/bps/.test(kind)) return units(v, ['bps', 'kbps', 'Mbps', 'Gbps', 'Tbps'], 1000)
    if (/bytes/.test(kind)) return units(v, ['B', 'KB', 'MB', 'GB', 'TB'], 1000)
    if (/percent|share|ratio/.test(kind)) return (v <= 1 ? v * 100 : v).toFixed(1) + ' %'
    if (v >= 1000) return thousands(Math.round(v))
    return String(Math.round(v * 100) / 100)
  }
  // Separador de miles a mano: toLocaleString no siempre respeta el idioma en TVs antiguas.
  function thousands(n) {
    var s = String(Math.abs(n))
    var out = ''
    while (s.length > 3) {
      out = '.' + s.slice(-3) + out
      s = s.slice(0, -3)
    }
    return (n < 0 ? '-' : '') + s + out
  }
  function units(v, list, base) {
    var i = 0
    while (v >= base && i < list.length - 1) {
      v /= base
      i++
    }
    return (v >= 100 ? Math.round(v) : v.toFixed(1).replace(/\.0$/, '')) + ' ' + list[i]
  }
  function toDate(ts) {
    if (ts == null) return null
    var t = typeof ts === 'number' ? (ts < 1e12 ? ts * 1000 : ts) : Date.parse(ts)
    return isNaN(t) ? null : new Date(t)
  }
  function ago(ts) {
    var d = toDate(ts)
    if (!d) return ''
    var s = Math.max(0, Math.round((new Date().getTime() - d.getTime()) / 1000))
    if (s < 60) return 'ahora'
    if (s < 3600) return 'hace ' + Math.floor(s / 60) + ' min'
    if (s < 86400) return 'hace ' + Math.floor(s / 3600) + ' h'
    return 'hace ' + Math.floor(s / 86400) + ' d'
  }
  function hhmm(d) {
    var h = d.getHours()
    var m = d.getMinutes()
    return (h < 10 ? '0' : '') + h + ':' + (m < 10 ? '0' : '') + m
  }
  var DAYS = ['dom', 'lun', 'mar', 'mié', 'jue', 'vie', 'sáb']
  var MONTHS = ['ene', 'feb', 'mar', 'abr', 'may', 'jun', 'jul', 'ago', 'sep', 'oct', 'nov', 'dic']
  function later(fn, ms) {
    setTimeout(fn, ms)
  }

  // Cada segundo: reloj, fecha, "actualizado hace", barra de progreso hasta el siguiente.
  function tick() {
    var n = new Date()
    text($('clock'), hhmm(n))
    text($('date'), DAYS[n.getDay()] + ' ' + n.getDate() + ' ' + MONTHS[n.getMonth()])
    if (lastUpdate) {
      var s = Math.round((n.getTime() - lastUpdate) / 1000)
      text($('updated'), s < 5 ? 'Actualizado ahora' : s < 120 ? 'Actualizado hace ' + s + ' s' : 'Actualizado hace ' + Math.round(s / 60) + ' min')
    }
    var bar = $('progress')
    bar.style.width = slideMs ? Math.min(100, ((n.getTime() - slideStart) / slideMs) * 100) + '%' : '0'
  }

  // --- Arranque --------------------------------------------------------------------------------
  setInterval(tick, 1000)
  tick()
  // Recarga diaria a las 04:00 (memoria acotada en TVs que nunca se apagan).
  setInterval(function () {
    var n = new Date()
    if (n.getHours() === 4 && n.getMinutes() === 0) location.reload()
  }, 60000)
  // Contra el marcado de pantallas encendidas todo el día: desplaza 1–2 px cada 10 min.
  var shift = 0
  setInterval(function () {
    shift = (shift + 1) % 4
    var p = $('player').style
    p.left = (shift === 1 || shift === 2 ? 2 : 0) + 'px'
    p.top = (shift >= 2 ? 1 : 0) + 'px'
  }, 600000)
  var resizeTimer = null
  window.onresize = function () {
    clearTimeout(resizeTimer)
    resizeTimer = setTimeout(function () {
      if (cards.length) layout()
    }, 200)
  }
  $('enroll-form').onsubmit = function (e) {
    if (e && e.preventDefault) e.preventDefault()
    enroll($('code').value)
    return false
  }
  var m = /code=([A-Za-z0-9-]+)/.exec(location.hash || '')
  if (m) {
    if (history.replaceState) history.replaceState(null, '', location.pathname)
    // Primero se comprueba si ya hay credencial; si no, se canjea el código del enlace.
    issueToken(function (r) {
      if (r === 'ok') return loadConfig()
      show('enroll')
      enroll(m[1])
    })
  } else {
    issueToken(function (r) {
      if (r === 'ok') return loadConfig()
      if (r === 'revoked') return toEnroll()
      // Servidor inalcanzable: se sigue intentando sin pedir el código otra vez.
      text(document.querySelector('#loading .big-msg'), 'Conectando con Horus Flow…')
      later(sync, 5000)
    })
  }
})()
