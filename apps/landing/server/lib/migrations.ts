// Migraciones versionadas de la base de datos SQLite. Cada una se aplica una sola vez, en orden
// y dentro de una transacción; la tabla schema_migrations guarda las aplicadas. Nunca se edita
// una migración ya publicada: los cambios van en una nueva con el número siguiente.
import { seedPagesSql } from './pages-seed'

export interface Migration {
  version: number
  name: string
  sql: string
}

export const migrations: Migration[] = [
  {
    version: 1,
    name: 'esquema inicial',
    sql: /* sql */ `
      -- Planes, precios y lo que incluye cada plan ------------------------------------------
      CREATE TABLE plans (
        id             TEXT PRIMARY KEY,
        sort           INTEGER NOT NULL,
        visible        INTEGER NOT NULL DEFAULT 1,
        highlighted    INTEGER NOT NULL DEFAULT 0,
        installer_size TEXT,
        clients        INTEGER,
        name_es        TEXT NOT NULL,
        name_en        TEXT NOT NULL,
        summary_es     TEXT NOT NULL,
        summary_en     TEXT NOT NULL,
        server_es      TEXT,
        server_en      TEXT,
        updated_at     TEXT NOT NULL
      );
      -- Importe en unidades menores (centavos) por moneda y periodo. Sin filas = a medida.
      CREATE TABLE plan_prices (
        plan_id      TEXT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
        currency     TEXT NOT NULL CHECK (currency IN ('USD', 'GTQ')),
        period       TEXT NOT NULL CHECK (period IN ('monthly', 'annual')),
        amount_minor INTEGER NOT NULL CHECK (amount_minor > 0),
        PRIMARY KEY (plan_id, currency, period)
      );
      CREATE TABLE plan_features (
        id      INTEGER PRIMARY KEY,
        plan_id TEXT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
        sort    INTEGER NOT NULL,
        text_es TEXT NOT NULL,
        text_en TEXT NOT NULL
      );
      CREATE INDEX plan_features_plan ON plan_features(plan_id, sort);
      -- Cada publicación de precios guarda una instantánea completa (historial y restaurar).
      CREATE TABLE pricing_versions (
        id            INTEGER PRIMARY KEY AUTOINCREMENT,
        created_at    TEXT NOT NULL,
        admin_id      INTEGER,
        admin_email   TEXT,
        note          TEXT NOT NULL DEFAULT '',
        restored_from INTEGER,
        snapshot      TEXT NOT NULL
      );

      -- Ajustes del sitio (clave → JSON): pricing, contact, support, banner ----------------
      CREATE TABLE settings (
        key        TEXT PRIMARY KEY,
        value      TEXT NOT NULL,
        updated_at TEXT NOT NULL
      );

      -- Métodos de pago: configuración pública (JSON) y secretos cifrados ------------------
      CREATE TABLE payment_methods (
        id         TEXT PRIMARY KEY CHECK (id IN ('paypal', 'neo', 'transfer')),
        enabled    INTEGER NOT NULL DEFAULT 0,
        config     TEXT NOT NULL DEFAULT '{}',
        secrets    TEXT,
        updated_at TEXT NOT NULL
      );

      -- Solicitudes de demo, contacto y compra --------------------------------------------
      CREATE TABLE requests (
        id                 INTEGER PRIMARY KEY,
        reference          TEXT NOT NULL UNIQUE,
        kind               TEXT NOT NULL CHECK (kind IN ('demo', 'contact', 'purchase')),
        status             TEXT NOT NULL DEFAULT 'new'
                           CHECK (status IN ('new', 'pending_payment', 'contacted', 'paid', 'cancelled')),
        created_at         TEXT NOT NULL,
        updated_at         TEXT NOT NULL,
        locale             TEXT NOT NULL,
        name               TEXT NOT NULL,
        company            TEXT NOT NULL,
        email              TEXT NOT NULL,
        country            TEXT NOT NULL,
        data               TEXT NOT NULL,
        plan               TEXT,
        period             TEXT,
        currency           TEXT,
        amount_minor       INTEGER,
        amount_usd_minor   INTEGER,
        payment_method     TEXT,
        access_token_hash  TEXT,
        paypal_order_id    TEXT UNIQUE,
        paypal_capture_id  TEXT UNIQUE,
        paid_at            TEXT,
        paid_amount_minor  INTEGER,
        paid_currency      TEXT
      );
      CREATE INDEX requests_status ON requests(kind, status, created_at DESC);
      CREATE TABLE request_events (
        id          INTEGER PRIMARY KEY,
        request_id  INTEGER NOT NULL REFERENCES requests(id) ON DELETE CASCADE,
        at          TEXT NOT NULL,
        actor       TEXT NOT NULL,
        from_status TEXT,
        to_status   TEXT,
        note        TEXT NOT NULL DEFAULT ''
      );
      CREATE INDEX request_events_request ON request_events(request_id, id);
      -- Eventos de webhook de PayPal ya procesados (idempotencia).
      CREATE TABLE paypal_events (
        id          TEXT PRIMARY KEY,
        received_at TEXT NOT NULL,
        type        TEXT NOT NULL,
        reference   TEXT
      );

      -- Administradores, sesiones, bloqueo de login y auditoría ----------------------------
      CREATE TABLE admins (
        id              INTEGER PRIMARY KEY,
        email           TEXT NOT NULL UNIQUE COLLATE NOCASE,
        name            TEXT NOT NULL DEFAULT '',
        password_hash   TEXT NOT NULL,
        totp_secret     TEXT,
        totp_enabled    INTEGER NOT NULL DEFAULT 0,
        totp_last_step  INTEGER NOT NULL DEFAULT 0,
        recovery_codes  TEXT NOT NULL DEFAULT '[]',
        disabled        INTEGER NOT NULL DEFAULT 0,
        created_at      TEXT NOT NULL,
        last_login_at   TEXT
      );
      CREATE TABLE sessions (
        id_hash     TEXT PRIMARY KEY,
        admin_id    INTEGER NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
        stage       TEXT NOT NULL CHECK (stage IN ('mfa', 'enroll', 'full')),
        csrf        TEXT NOT NULL,
        created_at  INTEGER NOT NULL,
        rotated_at  INTEGER NOT NULL,
        last_seen   INTEGER NOT NULL,
        expires_at  INTEGER NOT NULL,
        replaced_by TEXT,
        grace_until INTEGER,
        ip          TEXT NOT NULL DEFAULT '',
        user_agent  TEXT NOT NULL DEFAULT ''
      );
      CREATE INDEX sessions_admin ON sessions(admin_id);
      CREATE TABLE login_throttle (
        key          TEXT PRIMARY KEY,
        failures     INTEGER NOT NULL DEFAULT 0,
        locked_until INTEGER NOT NULL DEFAULT 0,
        updated_at   INTEGER NOT NULL
      );
      CREATE TABLE audit_log (
        id          INTEGER PRIMARY KEY,
        at          TEXT NOT NULL,
        admin_id    INTEGER,
        admin_email TEXT NOT NULL,
        action      TEXT NOT NULL,
        entity      TEXT NOT NULL,
        entity_id   TEXT NOT NULL DEFAULT '',
        before      TEXT,
        after       TEXT,
        ip          TEXT NOT NULL DEFAULT ''
      );
      CREATE INDEX audit_log_at ON audit_log(at DESC);
    `,
  },
  {
    version: 2,
    name: 'páginas editables (legales e informativas)',
    sql: /* sql */ `
      -- Páginas legales e informativas editables en /admin › Páginas (Markdown por idioma).
      -- id: clave estable (no cambia al cambiar el slug). Los slugs son únicos en cada idioma;
      -- server/lib/pages.ts impide además que el slug de un idioma coincida con el de otra
      -- página en el otro idioma.
      CREATE TABLE pages (
        id             TEXT PRIMARY KEY,
        kind           TEXT NOT NULL CHECK (kind IN ('legal', 'info')),
        slug_es        TEXT NOT NULL UNIQUE,
        slug_en        TEXT NOT NULL UNIQUE,
        title_es       TEXT NOT NULL,
        title_en       TEXT NOT NULL DEFAULT '',
        summary_es     TEXT NOT NULL DEFAULT '',
        summary_en     TEXT NOT NULL DEFAULT '',
        body_es        TEXT NOT NULL DEFAULT '',
        body_en        TEXT NOT NULL DEFAULT '',
        published      INTEGER NOT NULL DEFAULT 0,
        show_in_footer INTEGER NOT NULL DEFAULT 0,
        draft_notice   INTEGER NOT NULL DEFAULT 0,
        sort           INTEGER NOT NULL DEFAULT 0,
        created_at     TEXT NOT NULL,
        updated_at     TEXT NOT NULL,
        updated_by     TEXT NOT NULL DEFAULT ''
      );
      CREATE INDEX pages_sort ON pages(published, sort, id);
      -- Cada guardado guarda una instantánea completa (historial y restaurar), como
      -- pricing_versions. Al borrar una página informativa se borra su historial (la auditoría
      -- conserva el antes).
      CREATE TABLE page_versions (
        id            INTEGER PRIMARY KEY AUTOINCREMENT,
        page_id       TEXT NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
        created_at    TEXT NOT NULL,
        admin_id      INTEGER,
        admin_email   TEXT,
        note          TEXT NOT NULL DEFAULT '',
        restored_from INTEGER,
        snapshot      TEXT NOT NULL
      );
      CREATE INDEX page_versions_page ON page_versions(page_id, id DESC);

      -- Semilla: aviso legal, privacidad y términos (textos de app/content/legal.ts en
      -- Markdown) y los borradores nuevos (server/lib/pages-seed.ts).
      ${seedPagesSql()}
    `,
  },
]
