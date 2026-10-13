/*
 * Pantalla NOC ligera (kiosk-lite.html) para navegadores de TV antiguos: JavaScript ES5 sin
 * módulos, sin fetch ni Promise (XMLHttpRequest y callbacks). Mismo flujo que /kiosk:
 *   1. Sin credencial: código de 8 caracteres (o #code=… del enlace) → POST /kiosk/enroll
 *      (cookie de dispositivo __Secure-hf_kiosk).
 *   2. POST /kiosk/token → JWT de kiosco (10 min); GET /kiosk/config cada 60 s.
 *   3. Rota los dashboards de la lista; cada widget pide sus datos cada 30 s y se dibuja de
 *      forma simple según data_endpoint_kind: state (valores), series (línea) o table (filas).
 * Sin datos personales extra: el servidor ya enmascara según show_personal_data.
 */
/* eslint-disable */
;(function () {
  'use strict'
  var API = '/api/v1'
  var token = null
  var tokenExpires = 0
  var config = null
  var dashboards = {}
  var index = 0
  var rotateTimer = null
  var dataTimer = null
  var failingSince = null

  function $(id) {
    return document.getElementById(id)
  }
  function show(id) {
    var ids = ['loading', 'enroll', 'player', 'empty']
    for (var i = 0; i < ids.length; i++) {
      var el = $(ids[i])
      if (ids[i] === id) el.className = el.className.replace(/\s*hidden/g, '')
      else if (el.className.indexOf('hidden') < 0) el.className += ' hidden'
    }
  }
  function text(el, s) {
    el.textContent = s == null ? '' : String(s)
  }
  function el(tag, cls, content) {
    var e = document.createElement(tag)
    if (cls) e.className = cls
    if (content != null) text(e, content)
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
    if (new Date() - failingSince > 60000) {
      var off = $('offline')
      text(off, 'Sin conexión desde las ' + hhmm(failingSince) + ' · mostrando los últimos datos')
      off.className = 'offline'
    }
  }
  function networkUp() {
    failingSince = null
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

  function render() {
    var items = playable()
    if (!items.length) return show('empty')
    var item = items[index % items.length]
    var d = dashboards[item.dashboard_id]
    text($('dash-name'), d.name)
    var grid = $('grid')
    grid.innerHTML = ''
    var widgets = d.widgets || []
    var cards = []
    for (var i = 0; i < widgets.length; i++) {
      var w = widgets[i]
      if (w.type === 'noc_header') continue
      var card = el('section', 'card' + (/timeseries/.test(w.type) ? ' wide' : ''))
      var inner = el('div')
      inner.appendChild(el('h2', null, w.title || label(w.type)))
      var body = el('div', 'body')
      body.appendChild(el('p', 'nodata', 'Cargando…'))
      inner.appendChild(body)
      card.appendChild(inner)
      grid.appendChild(card)
      cards.push({ widget: w, body: body })
    }
    refreshData(d.id, cards)
    clearInterval(dataTimer)
    dataTimer = setInterval(function () {
      refreshData(d.id, cards)
    }, 30000)
    clearTimeout(rotateTimer)
    if (items.length > 1) {
      rotateTimer = setTimeout(function () {
        index = (index + 1) % items.length
        render()
      }, Math.max(10, item.duration_seconds || 30) * 1000)
    }
  }

  function refreshData(dashId, cards) {
    for (var i = 0; i < cards.length; i++) {
      ;(function (c) {
        var path = '/dashboards/' + dashId + '/widgets/' + encodeURIComponent(c.widget.id) + '/data'
        request('GET', path, null, function (status, data) {
          if (status === 200 && data && data.data) draw(c.body, data.data)
          else if (status !== 0 && c.body.firstChild && c.body.firstChild.className === 'nodata')
            text(c.body.firstChild, 'Sin datos')
        })
      })(cards[i])
    }
  }

  // --- Dibujo ----------------------------------------------------------------------------------
  function draw(body, data) {
    body.innerHTML = ''
    if (data.kind === 'state') return drawState(body, data.values || {})
    if (data.kind === 'series') return drawSeries(body, data.series || [])
    if (data.kind === 'table') return drawTable(body, data)
    body.appendChild(el('p', 'nodata', 'Sin datos'))
  }

  function drawState(body, values) {
    var dl = el('dl', 'kv')
    var n = 0
    for (var k in values) {
      if (!values.hasOwnProperty(k) || n >= 6) continue
      var v = values[k]
      if (v !== null && typeof v === 'object') continue
      var row = el('div')
      row.appendChild(el('dt', null, label(k)))
      row.appendChild(el('dd', null, format(k, v)))
      dl.appendChild(row)
      n++
    }
    if (!n) return body.appendChild(el('p', 'nodata', 'Sin datos'))
    body.appendChild(dl)
  }

  function drawSeries(body, series) {
    var s = series[0]
    if (!s || !s.points || !s.points.length) return body.appendChild(el('p', 'nodata', 'Sin datos'))
    var max = 0
    var last = null
    for (var i = 0; i < s.points.length; i++) {
      var v = s.points[i][1]
      if (v !== null && v > max) max = v
      if (v !== null) last = v
    }
    var w = 600
    var h = 160
    var pts = []
    for (var j = 0; j < s.points.length; j++) {
      var y = s.points[j][1]
      if (y === null) continue
      pts.push(((j / Math.max(1, s.points.length - 1)) * w).toFixed(1) + ',' + (h - (max ? (y / max) * (h - 8) : 0) - 4).toFixed(1))
    }
    var ns = 'http://www.w3.org/2000/svg'
    var svg = document.createElementNS(ns, 'svg')
    svg.setAttribute('viewBox', '0 0 ' + w + ' ' + h)
    svg.setAttribute('preserveAspectRatio', 'none')
    svg.setAttribute('class', 'spark')
    var line = document.createElementNS(ns, 'polyline')
    line.setAttribute('points', pts.join(' '))
    line.setAttribute('fill', 'none')
    line.setAttribute('stroke', '#58a6ff')
    line.setAttribute('stroke-width', '3')
    svg.appendChild(line)
    body.appendChild(el('p', 'last', format(s.unit, last)))
    body.appendChild(svg)
  }

  function drawTable(body, data) {
    var cols = (data.columns || []).slice(0, 4)
    var rows = (data.rows || []).slice(0, 8)
    if (!rows.length) return body.appendChild(el('p', 'nodata', 'Sin datos'))
    var t = el('table')
    var head = el('tr')
    for (var i = 0; i < cols.length; i++) head.appendChild(el('th', null, label(cols[i].key)))
    t.appendChild(head)
    for (var r = 0; r < rows.length; r++) {
      var tr = el('tr')
      for (var c = 0; c < cols.length; c++) {
        var num = /number|bytes|bps|percent/.test(cols[c].type)
        tr.appendChild(el('td', num ? 'num' : null, format(cols[c].type, rows[r][cols[c].key])))
      }
      t.appendChild(tr)
    }
    body.appendChild(t)
  }

  var LABELS = {
    traffic_now: 'Tráfico ahora',
    customers_active: 'Clientes activos',
    findings_summary: 'Hallazgos abiertos',
    exporters_status: 'Exportadores',
    traffic_timeseries: 'Tráfico',
    top_categories: 'Top categorías',
    top_customers: 'Top clientes',
    findings_feed: 'Hallazgos recientes',
    download_bps: 'Bajada',
    upload_bps: 'Subida',
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
    last_seen_at: 'Última vez',
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
    return v >= 1000 ? Math.round(v).toLocaleString('es') : String(Math.round(v * 100) / 100)
  }
  function units(v, list, base) {
    var i = 0
    while (v >= base && i < list.length - 1) {
      v /= base
      i++
    }
    return (v >= 100 ? Math.round(v) : v.toFixed(1)) + ' ' + list[i]
  }
  function hhmm(d) {
    var h = d.getHours()
    var m = d.getMinutes()
    return (h < 10 ? '0' : '') + h + ':' + (m < 10 ? '0' : '') + m
  }
  function later(fn, ms) {
    setTimeout(fn, ms)
  }

  // --- Arranque --------------------------------------------------------------------------------
  setInterval(function () {
    text($('clock'), hhmm(new Date()))
  }, 1000)
  // Recarga diaria a las 04:00 (memoria acotada en TVs que nunca se apagan).
  setInterval(function () {
    var n = new Date()
    if (n.getHours() === 4 && n.getMinutes() === 0) location.reload()
  }, 60000)
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
      text(document.querySelector('#loading .big'), 'Conectando con Horus Flow…')
      later(sync, 5000)
    })
  }
})()
