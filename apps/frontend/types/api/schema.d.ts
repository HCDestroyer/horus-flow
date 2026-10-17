// GENERADO por scripts/generate-api.mjs desde packages/schemas/openapi/dist/horus-api.v0.yaml.
// No editar: `pnpm api:generate`.
/* eslint-disable */
export interface paths {
    "/analytics/customers/{customer_id}/traffic": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                customer_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        /** Series de tráfico de un cliente (desde reset_at si existe). Acceso auditado */
        get: operations["getCustomerTraffic"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/analytics/routers/{router_id}/interfaces/metrics": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                router_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        /**
         * Series por interfaz (tráfico, paquetes, errores, descartes) con rango
         * @description Una serie por métrica e interfaz (`group` = nombre de la interfaz). Sin `interface_id`, las interfaces
         *     de subida (uplink) y, si no hay, las físicas con tráfico; como mucho 8 interfaces por consulta.
         */
        get: operations["getRouterInterfaceMetrics"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/analytics/routers/{router_id}/metrics": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                router_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        /**
         * Series del dispositivo (CPU, memoria, temperatura, latencia, disponibilidad) con rango
         * @description Desde `snmp.device_metrics` (crudo 30 d, 5 min 90 d, 1 h 13 meses). `availability_ratio` es la fracción
         *     de sondeos ICMP respondidos en el bucket; los huecos (router apagado, sondeador caído) son `null`, nunca 0.
         */
        get: operations["getRouterMetrics"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/analytics/traffic/attribution": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Cobertura de atribución (bytes atribuidos a clientes, de infraestructura y fuera de prefijos) */
        get: operations["getTrafficAttribution"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/analytics/traffic/timeseries": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Serie de tráfico ↓/↑ del ISP o de un nodo (huecos como null) */
        get: operations["getTrafficTimeseries"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/analytics/traffic/top": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Top N (≤ 10) + "Otros" por clientes, servicios, categorías, organizaciones o ASN
         * @description `dimension=customers` exige además `customers.read` (datos personales); sin él → 403.
         */
        get: operations["getTrafficTop"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api-token-templates": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Plantillas de permisos para API tokens (p. ej. ArgosISP) */
        get: operations["listApiTokenTemplates"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api-tokens": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** API tokens de integración del ISP (sin su valor) */
        get: operations["listApiTokens"];
        put?: never;
        /**
         * Crea un API token `hf_pat_…` (el valor se muestra una sola vez)
         * @description Token opaco `hf_pat_<32 B base62>` ligado a este ISP y a quien lo crea (docs/security.md §5.4). Lleva un
         *     subconjunto de los permisos **actuales** del creador (por `template` o `permissions`); en cada uso el
         *     gateway lo canjea por un access token de 2 min con la intersección entre esos permisos y los que el creador
         *     tenga en ese momento (con sus alcances por nodo). Caducidad por defecto 1 año (máximo 1 año). Solo se guarda
         *     su SHA-256. Plantilla `argosisp` = código de conexión para ArgosISP (docs/integrations/argosisp.md §1.1).
         *     Un API token no puede crear ni gestionar API tokens.
         */
        post: operations["createApiToken"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api-tokens/{api_token_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                api_token_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /** Revoca un API token (deja de servir de inmediato) */
        delete: operations["revokeApiToken"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/asn-rules": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Reglas ASN propias del ISP (nombre, categoría y servicio por ASN)
         * @description Lista completa (como mucho 1000 reglas por ISP, sin paginar), ordenada por ASN. Las reglas se aplican
         *     a los flujos que se ingieren desde su alta (`classification_method = local_override`); el histórico
         *     no se reclasifica. El nombre sí se muestra en los tops por ASN y organización de cualquier rango.
         */
        get: operations["listAsnRules"];
        put?: never;
        /** Nombra un ASN para el ISP (ASN repetido → 422 con error en `asn`) */
        post: operations["createAsnRule"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/asn-rules/{asn_rule_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                asn_rule_id: components["parameters"]["AsnRuleId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /** Borra una regla (el tráfico nuevo vuelve a clasificarse con el catálogo) */
        delete: operations["deleteAsnRule"];
        options?: never;
        head?: never;
        /** Edita el nombre, la categoría, el servicio o la nota de una regla (el ASN no cambia) */
        patch: operations["updateAsnRule"];
        trace?: never;
    };
    "/asn-rules/catalog": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Categorías y servicios del catálogo de plataforma que admite una regla ASN */
        get: operations["getAsnRuleCatalog"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/asn/{asn}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Cómo se llama hoy un ASN (dataset público, catálogo de plataforma y regla propia del ISP)
         * @description Nombre y país del snapshot IP→ASN de plataforma, organización del catálogo si el ASN está en él y la
         *     regla ASN propia del ISP si existe (proyección de los eventos de devices: puede tardar unos segundos
         *     en reflejar un cambio). Un ASN desconocido responde 200 con `name` null.
         */
        get: operations["getAsnInfo"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/assist/analyses": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Historial de análisis de IA de un hallazgo o un router, del más reciente al más antiguo
         * @description Devuelve los análisis correctos guardados del recurso tal como se mostraron (Markdown enmascarado),
         *     dentro de la retención `HORUS_ASSIST_HISTORY_RETENTION` (90 días por defecto; 0 = no se guardan y la
         *     lista viene vacía). Volver a verlos no consume consultas. Permiso: el de lectura del recurso
         *     (`finding` → `security.findings.read`, `router` → `devices.read`); el recurso se lee como el usuario
         *     y, si no lo ve, `404 ASSIST_CONTEXT_NOT_FOUND`.
         */
        get: operations["listAssistAnalyses"];
        put?: never;
        /**
         * Analiza un hallazgo o un router con IA (síncrono; puede tardar hasta HORUS_ASSIST_REQUEST_TIMEOUT)
         * @description Permiso: el de lectura del recurso analizado (`finding` → `security.findings.read`, `router` →
         *     `devices.read`) y el asistente activo en el ISP. El recurso se lee como el usuario: si no lo ve,
         *     `404 ASSIST_CONTEXT_NOT_FOUND`. Errores: `409 ASSIST_DISABLED` / `ASSIST_NOT_CONFIGURED`,
         *     `429 ASSIST_LIMIT_REACHED` (con `Retry-After` hasta el próximo día UTC), `422 ASSIST_REFUSED` (el modelo
         *     declinó), `502 ASSIST_PROVIDER_UNAVAILABLE` / `ASSIST_INCOMPLETE`, `502|503 ASSIST_CONTEXT_UNAVAILABLE` (la
         *     API de Horus no pudo servir el recurso: el `detail` trae el estado y el código que respondió el módulo),
         *     `503 ASSIST_STORE_UNAVAILABLE` (el almacén del asistente no responde).
         */
        post: operations["createAssistAnalysis"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/assist/audit": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Auditoría de las consultas a la IA del ISP (sin datos de clientes), de la más reciente a la más antigua */
        get: operations["listAssistAudit"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/assist/mcp": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Servidor MCP (Streamable HTTP, sin estado) con las herramientas de solo lectura del asistente
         * @description Mensajes JSON-RPC 2.0 de MCP (`initialize`, `tools/list`, `tools/call`…) con respuesta JSON. Autenticación
         *     `Authorization: Bearer hmcp_…` (no un JWT de Horus): el gateway no la valida, lo hace assist. Las
         *     herramientas actúan como el usuario dueño del token, con datos enmascarados, y cada llamada se audita
         *     (feature `mcp`). Requiere el asistente activo en el ISP. Configuración de Claude Desktop en docs/assist.md.
         */
        post: operations["assistMcp"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/assist/mcp-tokens": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Tokens del servidor MCP del ISP (sin su valor) */
        get: operations["listAssistMcpTokens"];
        put?: never;
        /**
         * Crea un token MCP que actúa como quien lo crea (el valor se muestra una sola vez)
         * @description El token solo da acceso a las herramientas de solo lectura del asistente, con la intersección entre sus
         *     permisos de lectura y los permisos **actuales** del usuario (si pierde un rol o la membresía, el token deja
         *     de servir). Se guarda solo su hash SHA-256. Caduca a los HORUS_ASSIST_MCP_TOKEN_TTL (90 días por defecto).
         */
        post: operations["createAssistMcpToken"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/assist/mcp-tokens/{token_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                token_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /** Revoca un token MCP */
        delete: operations["revokeAssistMcpToken"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/assist/settings": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Configuración efectiva del asistente del ISP (la propia o la de plataforma), sin la clave */
        get: operations["getAssistSettings"];
        /**
         * Guarda la configuración del asistente del ISP (la clave de API es write-only)
         * @description `api_key` se cifra (envelope encryption con la KEK de assist) y nunca vuelve en la respuesta; omitida =
         *     sin cambios; `clear_api_key: true` la borra. Al cambiar de proveedor sin enviar clave nueva, la clave
         *     guardada se descarta. Activar `anthropic` exige una clave (del ISP o de plataforma). `base_url` solo
         *     aplica a `openai_compatible` (obligatoria). Auditado en la auditoría del ISP.
         */
        put: operations["putAssistSettings"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/assist/settings/test": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Prueba de conexión con el proveedor configurado (petición mínima; no cuenta para el límite diario) */
        post: operations["testAssistSettings"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/assist/status": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Si el asistente está activo en el ISP (para mostrar u ocultar los botones "Analizar con IA") */
        get: operations["getAssistStatus"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/auth/invitations/accept": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Acepta una invitación a un tenant (crea el usuario si no existe) */
        post: operations["authAcceptInvitation"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/auth/invitations/inspect": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Datos de una invitación para la página de aceptación (sin consumirla) */
        post: operations["authInspectInvitation"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/auth/login": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Inicio de sesión con usuario y contraseña
         * @description Sin 2FA → access token de **sesión** (`scope=session`) + cookie de refresh. Con 2FA →
         *     `mfa_required` + `mfa_token` (5 min, un uso). Errores sin revelar si el usuario existe
         *     (`INVALID_CREDENTIALS`). Bloqueo progresivo según docs/security.md §4.2.
         */
        post: operations["authLogin"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/auth/logout": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Revoca la sesión actual y borra la cookie */
        post: operations["authLogout"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/auth/mfa/verify": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Segundo factor (TOTP o código de recuperación) */
        post: operations["authMfaVerify"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/auth/password/forgot": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Pide un enlace para restablecer la contraseña (respuesta idéntica exista o no la cuenta)
         * @description Si el correo corresponde a una cuenta activa y la instalación tiene SMTP, se le envía un enlace de un
         *     uso (30 min) a <HORUS_PUBLIC_BASE_URL>/reset-password#token=…; en cualquier otro caso la respuesta es
         *     la misma. `email_delivery` solo dice si la instalación puede enviar correo (no depende de la cuenta):
         *     sin SMTP, la persona debe pedir el enlace a un administrador (POST /members/{user_id}/password-reset).
         *     Límite: 20/min por IP y 5/min por cuenta.
         */
        post: operations["authForgotPassword"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/auth/password/reset": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Fija una contraseña nueva con un enlace de restablecimiento (un uso; revoca todas las sesiones)
         * @description Consume el token, guarda la contraseña (misma política que /me/password), quita el bloqueo por
         *     intentos fallidos y revoca todas las sesiones de la cuenta (reason password_reset). El 2FA no se toca.
         */
        post: operations["authResetPassword"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/auth/password/reset/inspect": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Datos de un enlace de restablecimiento para la página (sin consumirlo) */
        post: operations["authInspectPasswordReset"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/auth/reauth": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Re-autenticación reciente (marca `auth_time`) */
        post: operations["authReauth"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/auth/refresh": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Rota el refresh y devuelve un access token de sesión
         * @description Reutilización de un refresh ya usado ⇒ revoca la sesión (`refresh_token_reuse`). Exige `Origin` permitido (HORUS_ALLOWED_ORIGINS, D14).
         */
        post: operations["authRefresh"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/auth/token": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Access token de un tenant o de plataforma
         * @description `{"tenant_id"}` → JWT de 10 min con `tid` y los permisos de ese tenant. No miembro →
         *     `404 TENANT_NOT_FOUND` (no revela si existe); tenant suspendido → `403 TENANT_SUSPENDED`;
         *     rol que exige 2FA sin TOTP activo → `403 MFA_ENROLLMENT_REQUIRED` (D14: 2FA obligatorio para
         *     administradores). `{"scope": "platform"}` → token de plataforma (solo roles de plataforma).
         */
        post: operations["authIssueToken"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/classification/status": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Con qué datasets se clasifica el tráfico ahora (ASN, catálogo, reputación)
         * @description Sin dataset de ASN cargado, los flujos se clasifican solo por puerto (QUIC, HTTPS…) y el tráfico no se
         *     atribuye a organizaciones ni ASN. El rol traffic lo descarga al arrancar y lo renueva cada semana
         *     (HORUS_ASN_SYNC); el ingester lo lee de HORUS_ASN_SNAPSHOT_DIR. `source: unknown` significa que este
         *     proceso no enriquece flujos (despliegue en dos hosts) y no puede saberlo.
         */
        get: operations["getClassificationStatus"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/client-prefixes/{client_prefix_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                client_prefix_id: components["parameters"]["ClientPrefixId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /** Borra un prefijo (clientes fuera de todo prefijo → inactive, razón prefix_removed) */
        delete: operations["deleteClientPrefix"];
        options?: never;
        head?: never;
        /** Edita un prefijo (reducirlo pasa a inactive los clientes fuera de todo prefijo) */
        patch: operations["updateClientPrefix"];
        trace?: never;
    };
    "/client-prefixes/{client_prefix_id}/confirm": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                client_prefix_id: components["parameters"]["ClientPrefixId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Confirma un prefijo no confirmado */
        post: operations["confirmClientPrefix"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/customers": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Clientes descubiertos (sin filtro por IP en la URL) */
        get: operations["listCustomers"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/customers/{customer_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                customer_id: components["parameters"]["CustomerId"];
            };
            cookie?: never;
        };
        /** Ficha del cliente (acceso auditado) con sugerencia de tipo */
        get: operations["getCustomer"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /** Alias y notas (datos personales; auditado sin el valor) */
        patch: operations["updateCustomer"];
        trace?: never;
    };
    "/customers/{customer_id}/external-identity": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                customer_id: components["parameters"]["CustomerId"];
            };
            cookie?: never;
        };
        /**
         * Identidad del cliente en sistemas externos (ArgosISP) con sus periodos de vigencia
         * @description La identidad de Horus sigue siendo `(tenant, realm, ip)` (ADR-0018); la externa es una anotación con vigencia
         *     (`valid_from`/`valid_to`), porque en PPPoE una IP cambia de dueño (ADR-0030). `current` es la vigente;
         *     `stale = true` si ArgosISP no respondió en la última resolución (se conserva el último dato). Dato personal.
         */
        get: operations["getCustomerExternalIdentity"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/customers/{customer_id}/external-identity/refresh": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                customer_id: components["parameters"]["CustomerId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Actualiza ahora la identidad del cliente desde ArgosISP ("Actualizar desde ArgosISP")
         * @description Resuelve la IP del cliente en ArgosISP (síncrono, timeout 10 s). Si ArgosISP no responde, conserva el último
         *     dato marcado `stale` y responde 200 igualmente con `stale = true`. Un alias puesto a mano nunca se sobrescribe.
         *     409 `INTEGRATION_NOT_CONFIGURED` si el conector no está activo.
         */
        post: operations["refreshCustomerExternalIdentity"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/customers/{customer_id}/findings": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                customer_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        /** Hallazgos de un cliente */
        get: operations["listCustomerFindings"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/customers/{customer_id}/kind-history": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                customer_id: components["parameters"]["CustomerId"];
            };
            cookie?: never;
        };
        /** Historial inmutable de cambios de tipo */
        get: operations["listCustomerKindHistory"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/customers/{customer_id}/reset": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                customer_id: components["parameters"]["CustomerId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Reinicia el cliente (tipo por defecto, borra alias/notas, reset_at=now) */
        post: operations["resetCustomer"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/customers/{customer_id}/scoring": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                customer_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        /**
         * Último scoring residencial/comercial del cliente con su explicación y, con range, la serie diaria
         * @description Modelo `commercial-v1` (heurística ponderada y explicable): `features` con valor bruto, valor normalizado 0..1,
         *     peso y aportación; `reasons[]` ordenadas por aportación; probabilidades por clase; umbrales del ISP; estado de la
         *     histéresis (nivel publicado en `horus.detection.customer.kind_suggested` y candidato pendiente). Sin scoring
         *     todavía (menos horas activas que el mínimo, scoring desactivado o fuera del alcance de nodos) → `latest: null`.
         *     La IP del cliente no aparece en la respuesta ni en la URL.
         */
        get: operations["getCustomerScoring"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/customers/{customer_id}/set-kind": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                customer_id: components["parameters"]["CustomerId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Cambio manual de tipo (kind_source=manual, kind_locked=true; reason obligatorio) */
        post: operations["setCustomerKind"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/customers/{customer_id}/unlock-kind": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                customer_id: components["parameters"]["CustomerId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** kind_locked=false (el siguiente scoring puede cambiarlo) */
        post: operations["unlockCustomerKind"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/customers/lookup": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Búsqueda por IP o prefijo en el cuerpo (POST solo para sacar la IP de URLs y logs; no crea nada) */
        post: operations["lookupCustomers"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/customers/stats": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Conteos por tipo, origen, estado, estado de seguridad y nodo (widgets) */
        get: operations["getCustomerStats"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/dashboards": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Plantillas de sistema + propios + compartidos con el ISP */
        get: operations["listDashboards"];
        put?: never;
        /**
         * Crea un dashboard (privado por defecto; config de cada widget validada contra su config_schema)
         * @description `visibility=tenant` exige `dashboards.manage`. Tipo desconocido → `422 WIDGET_TYPE_UNKNOWN`; config inválida → `422 WIDGET_CONFIG_INVALID`.
         */
        post: operations["createDashboard"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/dashboards/{dashboard_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                dashboard_id: components["parameters"]["DashboardId"];
            };
            cookie?: never;
        };
        /** Documento de dashboard (kiosco solo si está asignado) */
        get: operations["getDashboard"];
        put?: never;
        post?: never;
        /** Borra un dashboard propio o del ISP */
        delete: operations["deleteDashboard"];
        options?: never;
        head?: never;
        /**
         * Edita nombre, visibilidad, rango y refresco (dueño, o dashboards.manage si es del ISP)
         * @description Plantillas de sistema → `409 DASHBOARD_READ_ONLY` (se duplican).
         */
        patch: operations["updateDashboard"];
        trace?: never;
    };
    "/dashboards/{dashboard_id}/duplicate": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                dashboard_id: components["parameters"]["DashboardId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Copia privada (forma de editar una plantilla) */
        post: operations["duplicateDashboard"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/dashboards/{dashboard_id}/layout": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                dashboard_id: components["parameters"]["DashboardId"];
            };
            cookie?: never;
        };
        get?: never;
        /** Reemplaza layout y posiciones de todos los widgets */
        put: operations["replaceDashboardLayout"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/dashboards/{dashboard_id}/widgets": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                dashboard_id: components["parameters"]["DashboardId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Añade un widget (If-Match con la versión del dashboard) */
        post: operations["addDashboardWidget"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/dashboards/{dashboard_id}/widgets/{widget_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                dashboard_id: components["parameters"]["DashboardId"];
                widget_id: components["parameters"]["WidgetId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /** Quita un widget */
        delete: operations["deleteDashboardWidget"];
        options?: never;
        head?: never;
        /** Edita título, posición, config o refresco de un widget */
        patch: operations["updateDashboardWidget"];
        trace?: never;
    };
    "/dashboards/{dashboard_id}/widgets/{widget_id}/data": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                dashboard_id: components["parameters"]["DashboardId"];
                widget_id: components["parameters"]["WidgetId"];
            };
            cookie?: never;
        };
        /**
         * Datos del widget resueltos en servidor desde su config guardada, con los permisos de quien mira
         * @description Sin el `required_permission` del tipo → `403 WIDGET_TYPE_NOT_ALLOWED` (solo ese widget). Kiosco: solo
         *     dashboards asignados y tipos `kiosk_allowed`; sin `show_personal_data` las IPs llegan enmascaradas
         *     (`meta.masked_personal_data = true`). `Cache-Control: private, max-age=<refresh/2>`, `ETag`; caché Valkey
         *     `t:<tenant_id>:wcache:…`. Timeout 10 s por widget.
         */
        get: operations["getWidgetData"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/detection/config": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Umbrales vigentes de los detectores del ISP (valores por defecto + ajustes del ISP), solo lectura
         * @description Los nombres de detector y los campos son los de detection.detector_config (domain.Params); las duraciones en formato Go ("5m0s").
         */
        get: operations["getDetectionConfig"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/enroll/wireguard": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * El router registra su clave pública con el token de un uso (público, sin sesión)
         * @description Token de 256 bits (solo su hash en BD) ligado a (tenant, router, peer previsto), TTL 24 h, un uso,
         *     revocable; 5 fallos lo invalidan; 10/min por IP. Usado, caducado, revocado o de otro router →
         *     `ENROLLMENT_TOKEN_INVALID` sin distinguir el caso. Clave pública ya registrada → `409
         *     WIREGUARD_PUBLIC_KEY_IN_USE`. No devuelve secretos. Auditado y notificado al admin del ISP.
         *     RouterOS envía el cuerpo con `/tool fetch http-method=post http-header-field="Content-Type: application/json"`.
         */
        post: operations["enrollWireguard"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/findings": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Hallazgos del ISP (orden por defecto severidad y recencia) */
        get: operations["listFindings"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/findings/{finding_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                finding_id: components["parameters"]["FindingId"];
            };
            cookie?: never;
        };
        /** Detalle con razones, evidencia agregada, acciones recomendadas y línea de tiempo */
        get: operations["getFinding"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/findings/{finding_id}/acknowledge": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                finding_id: components["parameters"]["FindingId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Reconocer (queda asignado a quien lo pulsa) */
        post: operations["acknowledgeFinding"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/findings/{finding_id}/evidence": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                finding_id: components["parameters"]["FindingId"];
            };
            cookie?: never;
        };
        /** Flujos de evidencia del cliente en la ventana (sin payloads). Acceso auditado */
        get: operations["getFindingEvidence"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/findings/{finding_id}/mark-false-positive": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                finding_id: components["parameters"]["FindingId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Falso positivo (comentario obligatorio; alimenta verdict_feedback y el periodo de silencio) */
        post: operations["markFindingFalsePositive"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/findings/{finding_id}/resolve": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                finding_id: components["parameters"]["FindingId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Resolver (si el patrón vuelve se abre uno nuevo enlazado, "reincidente") */
        post: operations["resolveFinding"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/findings/bulk-resolve": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Resolver en bloque los hallazgos activos que encajan con un filtro (p. ej. tras mejorar un detector)
         * @description Resuelve, con el mismo comentario, los hallazgos `open`/`acknowledged` que encajan con `kind`, `severity`
         *     y `site_id` (los que se omiten no filtran: sin `kind`, todos los tipos) dentro del alcance de nodos de
         *     quien llama. Cada hallazgo pasa por la misma transición que `resolveFinding`
         *     (veredicto, evento `horus.detection.finding.resolved` y estado de seguridad del cliente). Como máximo
         *     1000 por llamada: si `resolved` es 1000, puede quedar alguno; se vuelve a llamar.
         */
        post: operations["bulkResolveFindings"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/flow-exporters": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Estado de los exportadores del ISP */
        get: operations["listFlowExporters"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/flow-exporters/{router_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                router_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        /** Estado del exportador de un router */
        get: operations["getFlowExporter"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/infra-services": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Servicios de infraestructura del ISP con su estado (IPTV, HTTP, DNS, RADIUS…)
         * @description Inventario de los servidores propios del ISP que Horus vigila (no son clientes) con el estado del
         *     servicio (el peor de sus chequeos), la latencia del último chequeo activo y el último cambio. Los
         *     usuarios con alcance por nodo solo ven los servicios de sus nodos (los servicios sin nodo exigen
         *     alcance completo). Máximo 500 servicios por ISP.
         */
        get: operations["listInfraServices"];
        put?: never;
        /**
         * Da de alta un servicio de infraestructura y sus chequeos
         * @description Chequeos activos (`icmp`, `tcp`, `http`) desde el rol `snmp` y pasivos desde los flujos (`no_traffic`,
         *     `ddos_inbound`). `interval_seconds` ≥ 30. Horus solo sondea: nunca escribe en routers ni servidores.
         */
        post: operations["createInfraService"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/infra-services/{service_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                service_id: components["parameters"]["InfraServiceId"];
            };
            cookie?: never;
        };
        /** Detalle de un servicio con el historial reciente de cada chequeo */
        get: operations["getInfraService"];
        put?: never;
        post?: never;
        /** Borra el servicio y su estado (deja de vigilarse; no emite evento) */
        delete: operations["deleteInfraService"];
        options?: never;
        head?: never;
        /** Edita el servicio (los chequeos se reemplazan como lista; los que conservan `id` mantienen su estado) */
        patch: operations["updateInfraService"];
        trace?: never;
    };
    "/infra-services/{service_id}/router-snippet": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                service_id: components["parameters"]["InfraServiceId"];
            };
            cookie?: never;
        };
        /**
         * Script RouterOS para que el ISP haga alcanzable un servidor privado por el túnel (lo pega el usuario)
         * @description Para servidores con IP privada (RFC 1918 / ULA / CGNAT) que Horus no alcanza directamente. Genera las
         *     reglas MikroTik que el **usuario** revisa y pega en el router del nodo (Horus nunca escribe en routers):
         *     reenvío de cada puerto TCP/HTTP desde la IP de túnel del router hacia el servidor (`dst-nat`),
         *     `masquerade` de ese tráfico y aceptación en `forward`, todo limitado a la red de servicios de Horus y con
         *     `comment="horus-infra"`. El servicio debe declarar `probe_via_router_id` (o pasar `router_id`) y cada
         *     chequeo tcp/http `forward_port`. `horus_side` explica qué cambia en Horus: nada en el hub WireGuard, porque
         *     el tráfico va a la /32 de túnel del router, que ya está en sus AllowedIPs. ICMP no se puede reenviar.
         */
        get: operations["getInfraServiceRouterSnippet"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/integrations/argosisp": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Conector ArgosISP del ISP (sin credenciales) */
        get: operations["getArgosIntegration"];
        /**
         * Configura el conector ArgosISP (URL, token `atk_…` de solo escritura, activo)
         * @description `token` es de solo escritura: se guarda cifrado con la KEK de devices y nunca se devuelve (`has_token`).
         *     Omitirlo conserva el guardado. La URL base incluye `/api/v1` de ArgosISP; https obligatorio (destinos
         *     privados o http solo con HORUS_INTEGRATIONS_ALLOW_PRIVATE=true, laboratorios). La primera configuración genera
         *     el secreto del webhook (mostrarlo con `POST …/webhook-secret`).
         */
        put: operations["putArgosIntegration"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/integrations/argosisp/connection-test": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Prueba la conexión con ArgosISP (síncrona, timeout 10 s, no cambia nada) */
        post: operations["testArgosIntegration"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/integrations/argosisp/webhook-secret": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Genera un secreto nuevo para el webhook de ArgosISP y lo muestra una vez (con la URL a copiar en Argos)
         * @description El secreto lo genera Horus (receptor) y se copia en ArgosISP junto con `webhook_url` (contrato §1.3). El
         *     anterior sigue valiendo 24 h para que dé tiempo a cambiarlo en Argos.
         */
        post: operations["rotateArgosWebhookSecret"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/integrations/argosisp/webhook/{connector_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description Identificador opaco del conector (no es el ISP); forma parte de la URL que se copia en ArgosISP. */
                connector_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Receptor de webhooks firmados de ArgosISP
         * @description Contrato docs/integrations/argosisp.md §1.3 y §2.3. Firma obligatoria (`X-Webhook-Signature: v1=…` con el
         *     secreto del conector), marca de tiempo con ≤ 5 min de desfase y deduplicación por `X-Webhook-Id`. Responde
         *     202 rápido y procesa en segundo plano (re-resolución de las IPs o servicios afectados). Firma inválida → 401;
         *     conector desconocido o apagado → 404 (sin distinguir). Eventos desconocidos se aceptan y se ignoran.
         */
        post: operations["receiveArgosWebhook"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/integrations/consumption": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Consumo por lotes (≤ 500) por IP o por servicio de ArgosISP
         * @description Contrato docs/integrations/argosisp.md §3.2. `external_service_id` usa las vigencias de la identidad externa:
         *     suma todas las IPs que tuvo el servicio en el periodo, cada una solo dentro de su vigencia (precisión de 1 h en
         *     los bordes). Sin conector ArgosISP activo solo se acepta `address`. Días en UTC. Un elemento sin datos o sin
         *     cliente devuelve ceros y `error` (`not_found`); el lote no falla por un elemento. Acceso auditado.
         */
        post: operations["getIntegrationConsumption"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/integrations/ip-lookup": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Consulta 360 de una IP de cliente (identidad, consumo, salud y seguridad) con la IP en el cuerpo
         * @description Contrato docs/integrations/argosisp.md §3.1. La IP viaja en el cuerpo, nunca en la URL. `at` fija el instante
         *     de la identidad externa (quién tenía la IP entonces; por defecto ahora) y el final de las ventanas de consumo.
         *     Cada bloque exige además su permiso: `consumption` → `traffic.customer.read`, `health` → `snmp.read`,
         *     `security` → `security.findings.read`; sin él, o si el módulo que lo calcula no está en el proceso, el bloque
         *     es `null` y `meta.omitted` dice por qué. Si la IP coincide con clientes de varios realms, se devuelve el visto
         *     más recientemente. Acceso auditado (dato personal). Lo usan la consola y ArgosISP con su código de conexión.
         */
        post: operations["lookupIpIntegration"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/kiosk/config": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Dashboards/playlist asignados al kiosco y parámetros de rotación */
        get: operations["getKioskConfig"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/kiosk/enroll": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Canje del código → cookie de dispositivo
         * @description Público, rate limited (5/min por IP; 10 fallos invalidan el código); aplica `allowed_cidrs`.
         */
        post: operations["kioskEnroll"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/kiosk/token": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Cookie de dispositivo (rotativa) → access JWT de kiosco */
        post: operations["kioskIssueToken"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/kiosks": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Kioscos (pantallas NOC) del tenant */
        get: operations["listKiosks"];
        put?: never;
        /** Alta de kiosco */
        post: operations["createKiosk"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/kiosks/{kiosk_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                kiosk_id: components["parameters"]["KioskId"];
            };
            cookie?: never;
        };
        /** Detalle de kiosco */
        get: operations["getKiosk"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /** Edita dashboards/playlist, CIDR, política de datos personales y caducidad */
        patch: operations["updateKiosk"];
        trace?: never;
    };
    "/kiosks/{kiosk_id}/enrollment-codes": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                kiosk_id: components["parameters"]["KioskId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Código de enrolamiento de un uso (10 min) + URL del QR
         * @description `qr_url` = `<HORUS_PUBLIC_BASE_URL>/kiosk/enroll#code=<código>` (fragmento: no viaja al servidor).
         */
        post: operations["createKioskEnrollmentCode"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/kiosks/{kiosk_id}/revoke": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                kiosk_id: components["parameters"]["KioskId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Revocación inmediata (cierra su WebSocket con 4409 en < 5 s) */
        post: operations["revokeKiosk"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/librenms/routers": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Routers del ISP con su IP de gestión y modo de monitoreo para LibreNMS
         * @description La IP del túnel WireGuard hacia Horus no es alcanzable desde LibreNMS: cada router necesita una IP de gestión
         *     para LibreNMS y un modo (`ping` por defecto, SNMP v2c/v3 propio para LibreNMS, o las credenciales SNMPv3 de
         *     Horus con consentimiento explícito). Se guarda en `alerts` sin tocar el inventario de `devices`.
         */
        get: operations["listLibreNmsRouters"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/librenms/routers/{router_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                router_id: components["parameters"]["RouterId"];
            };
            cookie?: never;
        };
        get?: never;
        /**
         * Fija la IP de gestión y el modo de monitoreo del router en LibreNMS
         * @description Se aplica en la siguiente sincronización. `horus_snmp_v3` exige `reuse_horus_snmp_consent=true` (auditado):
         *     LibreNMS recibe entonces las credenciales SNMPv3 de solo lectura de Horus para ese router. Cambiar de modo
         *     borra las credenciales SNMP propias guardadas.
         */
        put: operations["setLibreNmsRouter"];
        post?: never;
        /** Quita la configuración de LibreNMS del router (sale del grupo en la siguiente sincronización) */
        delete: operations["clearLibreNmsRouter"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/librenms/routers/{router_id}/snmp-credentials": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                router_id: components["parameters"]["RouterId"];
            };
            cookie?: never;
        };
        get?: never;
        /**
         * Credenciales SNMP propias para LibreNMS (write-only)
         * @description Solo en los modos `snmp_v2c` (`community`) y `snmp_v3` (`v3_*`). Write-only: nunca se devuelven (solo
         *     `has_snmp_credentials`), se cifran con la KEK de `alerts` y se borran con la configuración. Auditado.
         */
        put: operations["putLibreNmsRouterSnmpCredentials"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/me": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Usuario actual, roles de plataforma y membresías con permisos por ISP */
        get: operations["getMe"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /**
         * Nombre, idioma, zona horaria, tenant por defecto y correo (el correo exige re-autenticación reciente)
         * @description Cambiar `email` requiere auth_time de menos de 5 min (403 REAUTH_REQUIRED si no; POST /auth/reauth);
         *     409 ALREADY_EXISTS si otra cuenta ya usa ese correo. Los demás campos no exigen re-autenticación.
         */
        patch: operations["updateMe"];
        trace?: never;
    };
    "/me/password": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Cambio de contraseña (revoca las demás sesiones) */
        post: operations["changeMyPassword"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/me/totp/confirm": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Confirma TOTP con un código y devuelve los códigos de recuperación */
        post: operations["confirmMyTotp"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/me/totp/enroll": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Inicia el alta de TOTP (devuelve secreto y URI otpauth, una vez)
         * @description Adelantado a I1 por D14 (acceso por Internet ⇒ 2FA obligatorio para administradores).
         */
        post: operations["enrollMyTotp"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/members": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Miembros del tenant */
        get: operations["listMembers"];
        put?: never;
        /** Invita por email con roles iniciales (misma respuesta exista o no el email) */
        post: operations["inviteMember"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/members/{user_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                user_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /** Quita al usuario del tenant (corta su acceso en ≤ 5 s) */
        delete: operations["removeMember"];
        options?: never;
        head?: never;
        /**
         * Corrige el nombre o el correo de un miembro que solo pertenece a este ISP
         * @description La cuenta es global: un administrador del ISP solo puede editarla si no tiene membresías activas en
         *     otros ISP ni roles de plataforma (409 USER_SHARED si las tiene: lo hace la plataforma). 409
         *     ALREADY_EXISTS si otra cuenta usa ese correo. Exige re-autenticación reciente y queda auditado.
         */
        patch: operations["updateMember"];
        trace?: never;
    };
    "/members/{user_id}/invitation": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                user_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Genera un enlace de invitación nuevo para un miembro invitado (invalida los anteriores) */
        post: operations["reissueMemberInvitation"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/members/{user_id}/password-reset": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                user_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Genera un enlace de restablecimiento de contraseña para un miembro que solo pertenece a este ISP
         * @description Enlace de un uso (30 min) a <HORUS_PUBLIC_BASE_URL>/reset-password#token=…, que se muestra una vez y,
         *     si hay SMTP, también se envía por correo. Invalida los enlaces anteriores sin usar. Las sesiones se
         *     revocan cuando la persona lo canjea, no al emitirlo. 409 USER_SHARED si la cuenta tiene membresías
         *     activas en otros ISP o roles de plataforma; 409 CONFLICT si el miembro está invitado (reenvíe la
         *     invitación) o la cuenta está bloqueada. Anti-escalada: quien lo emite debe tener todos los permisos de
         *     los roles del miembro.
         */
        post: operations["resetMemberPassword"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/members/{user_id}/role-assignments": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                user_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        get?: never;
        /** Reemplaza las asignaciones (rol, alcance) del miembro */
        put: operations["replaceMemberRoleAssignments"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/notification-channels": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Canales de notificación del ISP */
        get: operations["listNotificationChannels"];
        put?: never;
        /** Crea un canal (email, telegram o librenms) */
        post: operations["createNotificationChannel"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/notification-channels/{channel_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                channel_id: components["parameters"]["ChannelId"];
            };
            cookie?: never;
        };
        /** Detalle del canal */
        get: operations["getNotificationChannel"];
        put?: never;
        post?: never;
        /** Borra el canal (y sus secretos, crypto-shredding) */
        delete: operations["deleteNotificationChannel"];
        options?: never;
        head?: never;
        /** Edita nombre, destino, suscripciones o lo habilita/deshabilita */
        patch: operations["updateNotificationChannel"];
        trace?: never;
    };
    "/notification-channels/{channel_id}/connection-test": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                channel_id: components["parameters"]["ChannelId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Prueba de conexión sin enviar notificación (síncrona, timeout 10 s)
         * @description Comprueba alcance, TLS y autenticación del destino con las credenciales guardadas, sin publicar nada.
         *     `librenms` (D17): resuelve `base_url`, valida el certificado (salvo `tls_verify=false`) y hace una llamada
         *     de solo lectura autenticada a la API. `telegram`: `getMe` del bot. `email`: conexión y `EHLO`/`STARTTLS`
         *     al SMTP de la instalación. Un resultado correcto pasa el canal de `unverified` a `ok`. El error nunca incluye
         *     secretos ni el cuerpo de la respuesta remota.
         */
        post: operations["testNotificationChannelConnection"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/notification-channels/{channel_id}/credentials": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                channel_id: components["parameters"]["ChannelId"];
            };
            cookie?: never;
        };
        get?: never;
        /**
         * Secretos write-only del canal (token de bot de Telegram propio, contraseña y token de API de LibreNMS)
         * @description Write-only: nunca se devuelven (solo `has_credentials`); se cifran con la KEK de `alerts` y se borran con el
         *     canal (crypto-shredding). Auditado. Solo se aceptan los campos del `kind` del canal (otro → `422 VALIDATION_FAILED`).
         *     - `telegram`: opcional (sin token propio se usa el bot de la instalación, HORUS_TELEGRAM_BOT_TOKEN_FILE).
         *     - `librenms` (D17): `password` obligatoria salvo que se envíe `api_token`; `api_token` opcional
         *       (cabecera `X-Auth-Token` de la API de LibreNMS). Reemplaza el conjunto completo: lo no enviado se borra.
         *     - `email`: usa el SMTP de la instalación (HORUS_SMTP_*); no tiene secretos por canal (`422`).
         *     - `webhook`: `webhook_secret` obligatorio (lo genera el receptor; sin él la entrega falla con
         *       `missing_credentials`).
         */
        put: operations["putNotificationChannelCredentials"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/notification-channels/{channel_id}/librenms": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                channel_id: components["parameters"]["ChannelId"];
            };
            cookie?: never;
        };
        /**
         * Estado de la integración con LibreNMS del canal (objetos aprovisionados y última prueba)
         * @description Lo que Horus creó (o adoptó) en LibreNMS con sus ids, el resultado de la última sincronización y de la última
         *     prueba de extremo a extremo. Solo canales `librenms` (otro → `422 LIBRENMS_CHANNEL_REQUIRED`). Sin secretos.
         */
        get: operations["getLibreNmsIntegration"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/notification-channels/{channel_id}/librenms/devices/{router_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                channel_id: components["parameters"]["ChannelId"];
                router_id: components["parameters"]["RouterId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /**
         * Borra de LibreNMS el dispositivo que Horus creó para un router (acción explícita)
         * @description Solo dispositivos creados por Horus (un dispositivo que ya existía y Horus adoptó → `409
         *     LIBRENMS_DEVICE_NOT_OWNED`). Borra también su historial en LibreNMS. Auditado.
         */
        delete: operations["deleteLibreNmsRouterDevice"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/notification-channels/{channel_id}/librenms/sync": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                channel_id: components["parameters"]["ChannelId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Conecta o sincroniza Horus con LibreNMS (idempotente)
         * @description Crea o actualiza en LibreNMS, con el prefijo «Horus · » y guardando sus ids en Horus: el dispositivo «Horus Flow»
         *     (solo ICMP; host público de Horus) al que van las líneas de syslog de Horus, los grupos estáticos «Routers» e
         *     «Infraestructura», las plantillas en español y las reglas (router caído, CPU, memoria, interfaz caída, reinicio,
         *     servicio de infraestructura caído y evento de seguridad Horus por syslog), y los routers con IP de gestión
         *     configurada (`/librenms/routers`). Recrea lo que se borró a mano en LibreNMS; nunca modifica ni borra objetos
         *     que Horus no creó; un router que deja de estar configurado sale del grupo pero no se borra de LibreNMS.
         *     Los transportes no se pueden crear por la API: las reglas usan los transportes marcados por defecto en
         *     LibreNMS. Mide el desfase horario de LibreNMS (registro de eventos) para fechar el syslog. Síncrona; el
         *     fallo de un objeto concreto no detiene el resto (ver `steps`).
         */
        post: operations["syncLibreNmsIntegration"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/notification-channels/{channel_id}/librenms/test": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                channel_id: components["parameters"]["ChannelId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Prueba de extremo a extremo (syslog de prueba y comprobación de que LibreNMS lo guardó)
         * @description Comprueba en orden: autenticación (`auth`), que el dispositivo «Horus Flow» existe (`device`), que la regla de
         *     eventos de Horus existe (`rule`), el envío de una línea `HORUS INFO test …` por `POST /api/v0/syslogsink`
         *     (`syslog_send`) y su llegada al syslog del dispositivo en unos segundos (`syslog_stored`). `failed_step` y
         *     `error_code` indican el paso exacto que falla (p. ej. `syslog_not_stored` si el syslog de LibreNMS está
         *     desactivado). La línea coincide con la regla de eventos: LibreNMS emitirá la alerta por sus transportes por
         *     defecto y la resolverá a los 5 minutos. Sin aprovisionar → `409 LIBRENMS_NOT_PROVISIONED`.
         */
        post: operations["testLibreNmsIntegration"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/notification-channels/{channel_id}/test": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                channel_id: components["parameters"]["ChannelId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Envía una notificación de prueba (asíncrona; el resultado llega por WebSocket `me` y en el canal) */
        post: operations["testNotificationChannel"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/notification-channels/telegram/bot-validation": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Valida un token de bot de Telegram (getMe) sin guardarlo
         * @description Paso del asistente «Crear bot»: tras `/newbot` en @BotFather la persona pega el token y la consola muestra el
         *     nombre y el usuario del bot. El token no se guarda ni se devuelve (se guarda después con
         *     `PUT …/credentials`). Un token rechazado → `422 NOTIFICATION_CHANNEL_AUTH_FAILED` con la descripción de
         *     Telegram (saneada, sin el token).
         */
        post: operations["validateTelegramBot"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/notification-channels/telegram/chat-detection": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Detecta los chats donde escribieron al bot o lo añadieron (getUpdates)
         * @description Lee las actualizaciones pendientes del bot con `getUpdates` (`message`, `channel_post`, `my_chat_member`; sin
         *     espera larga) y las confirma con `offset` para que no se acumulen. Con un bot propio (`bot_token` pegado en el
         *     asistente o el guardado en `channel_id`) devuelve todos los chats; con el bot de la instalación exige `code`
         *     (`POST …/telegram/link-codes`) y solo devuelve el chat que lo envió. Horus no usa webhooks: si el bot tiene
         *     uno, Telegram rechaza `getUpdates` → `422 NOTIFICATION_TELEGRAM_WEBHOOK_ACTIVE`.
         */
        post: operations["detectTelegramChats"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/notification-channels/telegram/link-codes": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Código de un solo uso para detectar el chat con el bot de la instalación
         * @description Las actualizaciones del bot compartido (HORUS_TELEGRAM_BOT_TOKEN_FILE) mezclan a todos los ISP: para detectar
         *     el chat la persona envía `/start <código>` al bot (o publica el código en el canal) y
         *     `POST …/telegram/chat-detection` solo devuelve el chat que lo envió. Caduca a los 15 minutos. Sin bot de la
         *     instalación → `422 NOTIFICATION_TELEGRAM_BOT_NOT_CONFIGURED`.
         */
        post: operations["createTelegramLinkCode"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/notification-deliveries": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Entregas recientes (enviadas o fallidas) por canal */
        get: operations["listNotificationDeliveries"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/permissions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Catálogo de permisos y roles de sistema (C7) */
        get: operations["listPermissions"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/platform/events": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Registro de eventos de plataforma (arranques, caídas, migraciones, degradaciones; D23)
         * @description Eventos de la instalación, más recientes primero: arranque y parada de cada proceso y rol con su versión,
         *     paradas no limpias detectadas al arrancar (`unclean_shutdown`), dependencias caídas o recuperadas según el
         *     healthcheck de cada rol, migraciones aplicadas, búfer de telemetría por encima del 70 %, spool activo,
         *     reinicios del agente WireGuard, fallos definitivos de envío de alertas y cambios de la configuración
         *     efectiva entre arranques. Sin datos de clientes; `tenant_id` solo en eventos de un ISP (p. ej.
         *     `alert_delivery_failed`). Retención `HORUS_PLATFORM_EVENTS_RETENTION` (90 días). La UI de la consola de
         *     plataforma queda pendiente.
         */
        get: operations["platformListEvents"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/platform/exporters/unregistered": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Exportadores que envían flujos sin estar registrados (descartados) */
        get: operations["platformListUnregisteredExporters"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/platform/installation": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Modo de acceso de la instalación (dominio, subdominio o solo IP), TLS y avisos (D19)
         * @description Solo lectura: la configuración es de despliegue (`HORUS_ACCESS_MODE`, `HORUS_PUBLIC_BASE_URL`,
         *     `HORUS_TLS_MODE`), no se edita por API. En `ip_only` la consola de plataforma muestra siempre el aviso
         *     `ip_only_access` (y `self_signed_certificate` si aplica) con la huella del certificado para verificarla
         *     en el primer acceso.
         */
        get: operations["platformGetInstallation"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/platform/overview": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Estado por ISP sin datos de clientes (única vista multi-ISP de v1) */
        get: operations["platformOverview"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/platform/remote-destinations": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Destinos remotos de copias (cero destinos es válido; la UI avisa de que no hay copia fuera del servidor) */
        get: operations["platformListRemoteDestinations"];
        put?: never;
        /** Alta de un destino SFTP (sin credenciales; se ponen con PUT .../credentials y se prueba con POST .../test) */
        post: operations["platformCreateRemoteDestination"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/platform/remote-destinations/{destination_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                destination_id: components["parameters"]["DestinationId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /** Borra el destino y sus credenciales (los archivos ya copiados en el servidor remoto no se tocan) */
        delete: operations["platformDeleteRemoteDestination"];
        options?: never;
        head?: never;
        /** Edita un destino (cambiar host o puerto borra la huella confirmada y lo deshabilita hasta volver a probarlo) */
        patch: operations["platformUpdateRemoteDestination"];
        trace?: never;
    };
    "/platform/remote-destinations/{destination_id}/credentials": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                destination_id: components["parameters"]["DestinationId"];
            };
            cookie?: never;
        };
        get?: never;
        /** Credenciales SFTP write-only (contraseña o clave privada OpenSSH); cifradas, nunca se devuelven */
        put: operations["platformPutRemoteDestinationCredentials"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/platform/remote-destinations/{destination_id}/sync": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                destination_id: components["parameters"]["DestinationId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Encola una copia inmediata al destino (además del calendario automático tras cada backup local) */
        post: operations["platformSyncRemoteDestination"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/platform/remote-destinations/{destination_id}/test": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                destination_id: components["parameters"]["DestinationId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Prueba la conexión: se conecta, devuelve la huella SHA-256 de la clave del servidor y, si
         *     coincide con la confirmada (o se envía confirm_host_key con ella), autentica y escribe y borra
         *     un archivo de prueba en la ruta. La primera vez la huella hay que confirmarla (TOFU).
         */
        post: operations["platformTestRemoteDestination"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/platform/reputation/sources": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Fuentes de reputación (catálogo base y personalizadas) con estado de carga (D20) */
        get: operations["platformListReputationSources"];
        put?: never;
        /**
         * Alta de una fuente de reputación personalizada (se carga automáticamente y entra en el snapshot)
         * @description D20: el superadministrador agrega las listas que desee. La URL debe ser `https://`, pública (se rechazan
         *     loopback, link-local, rangos privados, CGNAT y las redes de la instalación, también tras resolver DNS y en
         *     cada redirección → `422 REPUTATION_SOURCE_URL_NOT_ALLOWED`) y sin credenciales en la URL; si el feed pide
         *     autenticación va en `auth_header_*` (write-only). `terms_acknowledged=true` deja constancia (auditada) de que
         *     quien la da de alta confirmó que los términos de la lista permiten su uso. La primera carga se encola al
         *     crearla; cada carga emite `horus.detection.reputation.source_refreshed`.
         */
        post: operations["platformCreateReputationSource"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/platform/reputation/sources/{source_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                source_id: components["parameters"]["ReputationSourceId"];
            };
            cookie?: never;
        };
        /** Detalle y estado de carga de una fuente */
        get: operations["platformGetReputationSource"];
        put?: never;
        post?: never;
        /**
         * Borra una fuente personalizada (sus indicadores salen del siguiente snapshot)
         * @description Una fuente del catálogo base no se borra (se deshabilita) → `422 REPUTATION_SOURCE_READ_ONLY`. Los hallazgos ya abiertos conservan la referencia a la fuente.
         */
        delete: operations["platformDeleteReputationSource"];
        options?: never;
        head?: never;
        /**
         * Edita una fuente personalizada (del catálogo base solo `enabled`)
         * @description Catálogo base con otro campo distinto de `enabled` → `422 REPUTATION_SOURCE_READ_ONLY`. Cambiar `url`, `format` o la autenticación encola una carga.
         */
        patch: operations["platformUpdateReputationSource"];
        trace?: never;
    };
    "/platform/reputation/sources/{source_id}/refresh": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                source_id: components["parameters"]["ReputationSourceId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Fuerza una carga ahora (asíncrona; el resultado llega como source_refreshed) */
        post: operations["platformRefreshReputationSource"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/platform/tenants": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** ISP de la instalación */
        get: operations["platformListTenants"];
        put?: never;
        /** Alta de ISP + invitación al primer administrador del ISP */
        post: operations["platformCreateTenant"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/platform/tenants/{tenant_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                tenant_id: components["parameters"]["TenantId"];
            };
            cookie?: never;
        };
        /** Detalle de un ISP */
        get: operations["platformGetTenant"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /** Nombre, país, zona horaria, cuotas y ajustes dentro de los límites de plataforma */
        patch: operations["platformUpdateTenant"];
        trace?: never;
    };
    "/platform/tenants/{tenant_id}/resume": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                tenant_id: components["parameters"]["TenantId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Reactiva un ISP suspendido */
        post: operations["platformResumeTenant"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/platform/tenants/{tenant_id}/suspend": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                tenant_id: components["parameters"]["TenantId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Suspende un ISP (sus tokens dejan de emitirse; los vigentes reciben 403 TENANT_SUSPENDED) */
        post: operations["platformSuspendTenant"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/platform/updates": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Aviso de versión nueva de Horus (canal stable o beta) con notas y comando de actualización
         * @description Lo calcula el rol `jobs` cada 6 h consultando la fuente de versiones de la instalación
         *     (`HORUS_UPDATE_SOURCE`: API de releases de GitHub o un `latest.json` publicado con cada release).
         *     Sin salida a Internet la comprobación falla en silencio y `status` = `unchecked`. Solo lectura: la
         *     actualización la aplica la persona en el servidor (`sudo horus-ctl upgrade`) o, si está activada, la
         *     actualización automática de parches (Z de X.Y.Z) en la ventana de mantenimiento.
         */
        get: operations["platformGetUpdates"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/platform/users": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Usuarios de la plataforma con sus membresías */
        get: operations["platformListUsers"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/platform/users/{user_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                user_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /**
         * Elimina una cuenta (baja lógica; revoca sus sesiones y membresías; nunca la propia ni el último platform_admin)
         * @description Baja lógica (deleted_at): el correo queda libre para una cuenta nueva, se revocan todas sus sesiones
         *     (reason user_deleted) y sus membresías (horus.auth.membership.revoked), se quitan sus roles de
         *     plataforma y se emite horus.auth.user.deleted. Su rastro en auditoría e historial se conserva.
         *     422 CANNOT_DELETE_SELF; 409 LAST_PLATFORM_ADMIN.
         */
        delete: operations["platformDeleteUser"];
        options?: never;
        head?: never;
        /**
         * Edita una cuenta (estado, correo o nombre); bloquear revoca sus sesiones, nunca la propia ni el último platform_admin activo
         * @description Merge parcial: `status` (active | disabled), `email` (409 ALREADY_EXISTS si otra cuenta lo usa) y
         *     `display_name`. Un cambio de correo o nombre emite horus.auth.user.updated.
         */
        patch: operations["platformUpdateUser"];
        trace?: never;
    };
    "/platform/users/{user_id}/password-reset": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                user_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Genera un enlace de restablecimiento de contraseña para una cuenta (un uso, 30 min)
         * @description Como POST /members/{user_id}/password-reset pero para cualquier cuenta activa de la plataforma. El
         *     enlace se muestra una vez y, si hay SMTP, se envía por correo; invalida los anteriores sin usar. Las
         *     sesiones se revocan al canjearlo. 409 CONFLICT si la cuenta está pendiente (reenvíe la invitación) o
         *     bloqueada.
         */
        post: operations["platformResetUserPassword"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/platform/users/{user_id}/platform-roles": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                user_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        get?: never;
        /** Sustituye los roles de plataforma de un usuario ([] los quita; siempre queda un platform_admin activo) */
        put: operations["platformSetUserRoles"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/platform/wireguard/hubs": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Hubs WireGuard de plataforma con endpoint, rango de túneles y ocupación (lectura, I1-31) */
        get: operations["platformListWireguardHubs"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/platform/wireguard/hubs/{hub_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                hub_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /**
         * Edita el hub (nombre, endpoint público, MTU y keepalive por defecto). Los routers ya conectados
         *     siguen con su configuración hasta que se les genere un script nuevo; el puerto UDP no se cambia
         *     aquí (lo publica Docker: horus-ctl / install.sh --wg-port).
         */
        patch: operations["platformUpdateWireguardHub"];
        trace?: never;
    };
    "/platform/wireguard/hubs/{hub_id}/pools": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                hub_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Añade un rango de direcciones de túnel. Solo se añaden rangos: las direcciones ya asignadas son
         *     la identidad de cada router y no se mueven. Sin solaparse con otros rangos ni con el de servicios,
         *     fuera de 100.64.0.0/10 y entre /16 y /28 (IPv4) o /64 y /120 (IPv6).
         */
        post: operations["platformAddWireguardPool"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/platform/wireguard/hubs/{hub_id}/pools/{pool_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                hub_id: components["schemas"]["Uuid"];
                pool_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /** Quita un rango sin direcciones asignadas ni en cuarentena (409 WIREGUARD_POOL_IN_USE); nunca el último. */
        delete: operations["platformDeleteWireguardPool"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/playlists": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Listas de reproducción para pantallas */
        get: operations["listPlaylists"];
        put?: never;
        /** Crea una playlist */
        post: operations["createPlaylist"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/playlists/{playlist_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                playlist_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /** Borra una playlist */
        delete: operations["deletePlaylist"];
        options?: never;
        head?: never;
        /** Edita una playlist (emite horus.analytics.playlist.updated → los kioscos la aplican) */
        patch: operations["updatePlaylist"];
        trace?: never;
    };
    "/report-definitions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Programación de los reportes del ISP (se crea con los valores por defecto si falta)
         * @description Por defecto el reporte semanal de seguridad está habilitado los lunes a las 07:00 (zona del ISP), en PDF y CSV.
         */
        get: operations["listReportDefinitions"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/report-definitions/{definition_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                definition_id: components["parameters"]["ReportDefinitionId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /**
         * Cambia la programación (habilitado, día y hora, zona, formatos, destinatarios del aviso)
         * @description JSON Merge Patch con `If-Match`. `recipients`: hasta 20 correos que reciben un aviso con el enlace a la
         *     página de reportes de la consola (sin adjunto) si la instalación tiene SMTP (`email_available`).
         */
        patch: operations["updateReportDefinition"];
        trace?: never;
    };
    "/reports": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Ejecuciones de reportes del ISP (programadas y a petición), más recientes primero
         * @description Reporte semanal de seguridad por ISP (I3, rol `reporting`). Los reportes cubren todo el ISP: el permiso
         *     debe tener alcance de todo el ISP (un permiso limitado a nodos → 403).
         */
        get: operations["listReports"];
        put?: never;
        /**
         * Genera un reporte a petición (asíncrono → 202 + Location de la ejecución)
         * @description Sin `from`/`to`: la última semana ISO completa (lunes 00:00 → lunes 00:00) en la zona horaria del ISP.
         *     Con rango: como mucho 31 días, sin empezar en el futuro (`to` se recorta a ahora). Idempotency-Key
         *     obligatorio: la misma clave del mismo usuario devuelve la misma ejecución (`Idempotency-Replayed: true`).
         *     Máximo 10 por usuario y hora (`429 RATE_LIMITED` con `Retry-After`). El aviso de "reporte listo" llega
         *     por WebSocket (topic `me`, `horus.reporting.report.completed|failed`).
         */
        post: operations["createReport"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/reports/{report_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                report_id: components["parameters"]["ReportId"];
            };
            cookie?: never;
        };
        /**
         * Estado de una ejecución (queued, running, completed, failed) y sus archivos
         * @description Otro ISP o inexistente → `404 REPORT_NOT_FOUND`.
         */
        get: operations["getReport"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/reports/{report_id}/download": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                report_id: components["parameters"]["ReportId"];
            };
            cookie?: never;
        };
        /**
         * Descarga el archivo de un reporte (PDF o zip de CSV), autenticada y auditada
         * @description Solo por la API (docs/api.md §2.10 bis; sin URLs firmadas): `Content-Disposition: attachment`,
         *     `Cache-Control: no-store`, `Repr-Digest` con el SHA-256. Cada descarga queda en la auditoría
         *     (`reports.download`). Otro ISP → `404`; aún no generado → `409 REPORT_NOT_READY`; archivos borrados por
         *     la retención → `409 REPORT_EXPIRED`. CSV: zip con un archivo por sección, UTF-8 con BOM, separador `;`.
         */
        get: operations["downloadReport"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/reputation/allowlist": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Allowlist del ISP (prefijos/ASN que no deben generar hallazgos) */
        get: operations["listAllowlist"];
        put?: never;
        /** Añade una entrada a la allowlist */
        post: operations["createAllowlistEntry"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/reputation/allowlist/{entry_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                entry_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /** Quita una entrada de la allowlist */
        delete: operations["deleteAllowlistEntry"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/reputation/sources": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Estado de los feeds de reputación de plataforma (última actualización, entradas, licencia) */
        get: operations["listReputationSources"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/roles": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Roles del tenant (plantillas de sistema + propios) */
        get: operations["listRoles"];
        put?: never;
        /** Crea un rol propio del ISP (solo permisos de ISP que tenga quien lo crea) */
        post: operations["createRole"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/roles/{role_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                role_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /** Borra un rol propio sin asignaciones (409 ROLE_IN_USE si alguien lo tiene) */
        delete: operations["deleteRole"];
        options?: never;
        head?: never;
        /** Edita un rol propio (nombre, descripción, permisos); los de sistema no se editan */
        patch: operations["updateRole"];
        trace?: never;
    };
    "/routers": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Routers del ISP */
        get: operations["listRouters"];
        put?: never;
        /**
         * Registra un router (queda "Pendiente de configurar"; publica horus.devices.router.created)
         * @description Segundo router principal en el nodo → `409 ROUTER_PRIMARY_EXISTS`. RouterOS < 7.12 → se acepta con `warnings: [routeros_version_unsupported]`.
         */
        post: operations["createRouter"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/routers/{router_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                router_id: components["parameters"]["RouterId"];
            };
            cookie?: never;
        };
        /** Detalle del router */
        get: operations["getRouter"];
        put?: never;
        post?: never;
        /** Baja lógica del router */
        delete: operations["deleteRouter"];
        options?: never;
        head?: never;
        /** Edita un router */
        patch: operations["updateRouter"];
        trace?: never;
    };
    "/routers/{router_id}/credentials/{kind}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                kind: "snmp" | "routeros_api";
                router_id: components["parameters"]["RouterId"];
            };
            cookie?: never;
        };
        get?: never;
        /**
         * Credencial write-only (SNMPv3 o usuario de solo lectura de la API de RouterOS)
         * @description Nunca devuelve el secreto. En I1 el script de onboarding genera ambas y las guarda cifradas; este endpoint permite reemplazarlas.
         */
        put: operations["putRouterCredential"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/routers/{router_id}/deprovisioning-script": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                router_id: components["parameters"]["RouterId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Script inverso (elimina lo que lleva comment="horus" y solo el target de Traffic Flow de Horus) */
        post: operations["createDeprovisioningScript"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/routers/{router_id}/interfaces": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                router_id: components["parameters"]["RouterId"];
            };
            cookie?: never;
        };
        /**
         * Interfaces descubiertas por SNMP y cuáles se sondean
         * @description Inventario leído de IF-MIB en el último descubrimiento (cada hora y al reconectar). Se sondean las
         *     físicas (ether, sfp, wlan), VLAN, bridge y bonding; no las dinámicas por cliente (`<pppoe-…>`), cuyo
         *     consumo sale de los flujos. `uplink` marca la interfaz con más tráfico de subida al exterior, que es
         *     la que resume el estado del router.
         */
        get: operations["listRouterInterfaces"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/routers/{router_id}/metrics/live": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                router_id: components["parameters"]["RouterId"];
            };
            cookie?: never;
        };
        /** Última muestra completa de un router (dispositivo, sensores e interfaces) */
        get: operations["getRouterLiveMetrics"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/routers/{router_id}/poll": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                router_id: components["parameters"]["RouterId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Sondeo inmediato (el resultado llega por WebSocket router.{id}.metrics y en /metrics/live) */
        post: operations["pollRouterNow"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/routers/{router_id}/prefix-import-preview": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                router_id: components["parameters"]["RouterId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Lee del MikroTik (REST, solo lectura) pools y redes de cara al cliente y propone prefijos
         * @description No aplica nada (I1-28). Usa el usuario de solo lectura por el túnel; solo métodos GET hacia RouterOS.
         *     Router inalcanzable → `502 ROUTER_UNREACHABLE`; huella TLS distinta → `409 ROUTER_TLS_FINGERPRINT_CHANGED`
         *     (requiere confirmación explícita, auditada).
         */
        post: operations["previewRouterPrefixImport"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/routers/{router_id}/provisioning-script": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                router_id: components["parameters"]["RouterId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Genera el script RouterOS .rsc de alta con un token de enrolamiento de un uso embebido
         * @description Plantilla versionada por RouterOS (7.12 y última long-term). Contiene: interfaz WireGuard sin clave
         *     privada, peer al hub (`HORUS_WG_ENDPOINT:HORUS_WG_PORT`), firewall solo desde el túnel, usuario de
         *     solo lectura `horus-ro`, SNMPv3 authPriv, Traffic Flow IPFIX sin muestreo hacia el colector por el
         *     túnel, y `/tool fetch` a `<HORUS_PUBLIC_BASE_URL>/api/v1/enroll/wireguard` con cuerpo JSON
         *     `{"token", "public_key"}`. Contraseñas y token se muestran **una sola vez**: pedirlo de nuevo crea
         *     otros e invalida los anteriores (auditado). RouterOS < 7.12 → `422 ROUTEROS_VERSION_UNSUPPORTED` (D15).
         *     `/tool fetch` siempre con `check-certificate=yes`: con TLS `self_signed` o `provided` (D19, p. ej. modo
         *     `ip_only`) el script incluye el certificado público de la instalación y lo importa como confiable antes del
         *     fetch; nunca desactiva la verificación.
         */
        post: operations["createProvisioningScript"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/routers/status": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Estado efectivo y últimas métricas de cada router (vista NOC de routers)
         * @description Una fila por router del ISP (o del nodo) con el estado efectivo, su razón, las señales que lo sostienen
         *     (ICMP, SNMP, túnel, exportador) y la última muestra (CPU, memoria, temperatura, uptime, latencia, enlace
         *     de subida). `meta.poller` dice si el sondeador late: sin latido, todo router pasa a `stale`. Apto para
         *     kioscos (sin datos personales). Orden: problemas primero (critical, offline, degraded, warning, stale),
         *     después por nombre.
         */
        get: operations["listRouterStatus"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/routers/thresholds": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Umbrales de CPU, memoria y temperatura de los routers del ISP
         * @description Umbrales vigentes (aviso y crítico) con los que el sondeador calcula `warning`/`critical` y las razones
         *     `cpu_high`, `memory_high` y `temperature_high`. Sin ajustes del ISP (`customized=false`, `version=0`) rigen los
         *     de la instalación (HORUS_SNMP_*_WARNING/_CRITICAL), que también se devuelven en `defaults`. CPU y memoria deben
         *     mantenerse `sustained_cycles` ciclos seguidos; la temperatura, uno.
         */
        get: operations["getRouterThresholds"];
        /**
         * Fija los umbrales propios del ISP (se aplican en el siguiente ciclo de sondeo)
         * @description CPU y memoria entre 1 y 100 %, temperatura entre 20 y 150 °C; el aviso no puede superar al crítico (`422`).
         *     `If-Match` opcional (ETag de la lectura; no coincide → `412`). Sin PostgreSQL en el rol snmp → `503`. Auditado
         *     (`snmp.thresholds.updated`, antes y después). Horus no escribe nada en los routers: solo cambia cómo evalúa sus
         *     métricas.
         */
        put: operations["putRouterThresholds"];
        post?: never;
        /** Vuelve a los umbrales de la instalación */
        delete: operations["resetRouterThresholds"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/security/summary": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Conteos por severidad, señal, kind, estado de seguridad y nodo (widgets; sin IPs) */
        get: operations["getSecuritySummary"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/sites": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Nodos del ISP */
        get: operations["listSites"];
        put?: never;
        /** Alta de nodo */
        post: operations["createSite"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/sites/{site_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                site_id: components["parameters"]["SiteId"];
            };
            cookie?: never;
        };
        /** Detalle de nodo */
        get: operations["getSite"];
        put?: never;
        post?: never;
        /** Baja lógica de un nodo (con routers → 409 SITE_NOT_EMPTY) */
        delete: operations["deleteSite"];
        options?: never;
        head?: never;
        /** Edita un nodo */
        patch: operations["updateSite"];
        trace?: never;
    };
    "/sites/{site_id}/client-prefixes": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                site_id: components["parameters"]["SiteId"];
            };
            cookie?: never;
        };
        /** Prefijos del nodo (vacío = modo descubrimiento) */
        get: operations["listClientPrefixes"];
        put?: never;
        /**
         * Declara un prefijo (solape en el realm → 409 CLIENT_PREFIX_OVERLAP)
         * @description El realm lo decide el servidor (privado/CGNAT → realm del nodo; público → realm del ISP). Si el
         *     prefijo se cruza con otro activo del realm, 409 CLIENT_PREFIX_OVERLAP cuyo `detail` y
         *     `errors[0]` (field `prefix`, code `OVERLAP`) dicen con qué prefijo choca, su rol y en qué nodo
         *     está; los prefijos quitados no cuentan.
         */
        post: operations["createClientPrefix"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/sites/{site_id}/client-prefixes/batch": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                site_id: components["parameters"]["SiteId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Confirma una selección (importación del MikroTik o propuestas del modo descubrimiento), todo o nada
         * @description Todo o nada. Si un elemento se cruza con un prefijo activo del realm, o con otro de la misma
         *     selección, 409 CLIENT_PREFIX_OVERLAP con `errors[0].field = items[i].prefix` señalando cuál y
         *     con qué choca.
         */
        post: operations["batchCreateClientPrefixes"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/sites/{site_id}/prefix-proposals": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                site_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        /** Propuestas del modo descubrimiento (prefijos agregados vistos del lado customer_edge) */
        get: operations["listPrefixProposals"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/sites/{site_id}/unattributed-addresses": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                site_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * IPs con más tráfico dentro de un rango sin atribuir de un nodo
         * @description Detalle de una propuesta de prefijo (`GET /sites/{site_id}/prefix-proposals`): las IP del rango que más
         *     tráfico generan en el periodo, de más a menos. Son IP de clientes aún sin declarar (dato personal), por eso
         *     exige `customers.read`. El rango va en el cuerpo (nunca IP en la URL). Fuente: flows.unattributed_1h (30 días).
         */
        post: operations["queryUnattributedAddresses"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/system/status": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Capacidades del sistema (resumen para todos; detalle solo con platform.status.read) */
        get: operations["getSystemStatus"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/widget-data/preview": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Datos de un widget no guardado (editor; no disponible para kioscos) */
        post: operations["previewWidgetData"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/widget-types": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Catálogo de tipos de widget (sin tenant; autoritativo) */
        get: operations["listWidgetTypes"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/wireguard/enrollment-tokens/{token_id}/revoke": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                token_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Revoca un token de enrolamiento no usado */
        post: operations["revokeEnrollmentToken"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/wireguard/peers": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Túneles (peers) de los routers del ISP */
        get: operations["listWireguardPeers"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/wireguard/peers/{peer_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                peer_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        /** Detalle del túnel ("Túnel activo · último handshake hace N s") */
        get: operations["getWireguardPeer"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/ws/tickets": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Ticket de un uso (30 s) para abrir el WebSocket ligado al `tid` del token
         * @description La conexión `wss://<dominio>/api/v1/ws?ticket=<ticket>` queda ligada al principal y al `tid` del token
         *     con el que se pidió el ticket. `Origin` se valida contra HORUS_ALLOWED_ORIGINS (D14). Sin Valkey → 503
         *     (el frontend pasa a polling).
         */
        post: operations["createWsTicket"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
}
export type webhooks = Record<string, never>;
export interface components {
    schemas: {
        "$defs-Reason": {
            code: string;
            data?: {
                [key: string]: unknown;
            } | null;
            detail: string;
            weight?: number | null;
        };
        /** Format: date-time */
        "$defs-Timestamp": string;
        /** Format: uuid */
        "$defs-Uuid": string;
        /**
         * @description D19, por instalación (`HORUS_ACCESS_MODE`; si falta se deduce de `HORUS_PUBLIC_BASE_URL`: IP literal → `ip_only`).
         *     `domain`: dominio propio del cliente. `subdomain`: subdominio (del cliente o del proveedor de la instalación).
         *     `ip_only`: solo la IP pública del servidor; nada en Horus depende de tener dominio.
         * @enum {string}
         */
        AccessMode: "domain" | "subdomain" | "ip_only";
        AccessTokenResponse: {
            /** @description JWT EdDSA; solo en memoria de la SPA. */
            access_token: string;
            expires_at: components["schemas"]["Timestamp"];
            /** @enum {string} */
            scope: "session" | "tenant" | "platform" | "kiosk";
            /** @description `tid` del token (solo `scope=tenant` o `kiosk`). */
            tenant_id?: components["schemas"]["Uuid"] | null;
            /** @constant */
            token_type: "Bearer";
        };
        AllowlistEntry: {
            asn?: number | null;
            created_at: components["schemas"]["Timestamp"];
            created_by: components["schemas"]["Uuid"];
            id: components["schemas"]["Uuid"];
            kinds?: string[];
            prefix?: string | null;
            reason: string;
            tenant_id: components["schemas"]["Uuid"];
        };
        AnalyticsMeta: {
            /** @enum {string} */
            agg?: "avg" | "max" | "min" | "sum" | "last" | "p95";
            /** @description Fracción del rango con datos. Huecos como ausencia, nunca ceros. */
            coverage: number;
            from: components["schemas"]["Timestamp"];
            generated_at?: components["schemas"]["Timestamp"];
            /** @description true si parte del rango no está disponible (retención, exportador silencioso, backend degradado). */
            partial: boolean;
            /**
             * @example flows
             * @example snmp
             */
            source?: string;
            step: number | null;
            to: components["schemas"]["Timestamp"];
        };
        ApiToken: {
            active: boolean;
            created_at: components["schemas"]["Timestamp"];
            expires_at: components["schemas"]["Timestamp"];
            id: components["schemas"]["Uuid"];
            last_used_at: components["schemas"]["Timestamp"] | null;
            /** @description IP del último canje (resolución ~2 min). */
            last_used_ip: string | null;
            name: string;
            permissions: string[];
            /** @description Primeros caracteres del token (hf_pat_XXXX) para reconocerlo. */
            prefix: string;
            revoked_at: components["schemas"]["Timestamp"] | null;
            template: string | null;
            /** @description Quien lo creó (el token actúa con sus permisos actuales ∩ los del token). */
            user_id: components["schemas"]["Uuid"];
        };
        ApiTokenInput: {
            /** @description Caducidad (≥ 1 h, ≤ 1 año). Por defecto, dentro de un año. */
            expires_at?: components["schemas"]["Timestamp"] | null;
            /** @example ArgosISP */
            name: string;
            /** @description Claves de permiso de tenant que el creador tiene ahora (nunca `api_tokens.manage`). */
            permissions?: string[];
            /**
             * @description Plantilla de permisos. Si también llega `permissions`, mandan estos.
             * @enum {string|null}
             */
            template?: "argosisp" | null;
        };
        ApiTokenIssued: components["schemas"]["ApiToken"] & {
            /** @description Valor del token. Solo en esta respuesta. */
            token: string;
        };
        ApiTokenTemplate: {
            description: string;
            /** @example argosisp */
            key: string;
            /** @example ArgosISP */
            name: string;
            permissions: string[];
        };
        ArgosConnectionTest: {
            error: string | null;
            /** @enum {string|null} */
            error_code: "auth_failed" | "forbidden" | "timeout" | "connect_failed" | "tls_invalid" | "unexpected_response" | "not_allowed" | null;
            latency_ms: number | null;
            ok: boolean;
            /** @enum {string|null} */
            resolver: "batch" | "fallback" | null;
            tested_at: components["schemas"]["Timestamp"];
        };
        ArgosIntegration: {
            /** @example https://argos.isp.example/api/v1 */
            base_url: string | null;
            configured: boolean;
            enabled: boolean;
            has_token: boolean;
            has_webhook_secret: boolean;
            /** @description Id del conector (parte de webhook_url); null si nunca se configuró. */
            id: components["schemas"]["Uuid"] | null;
            /** @description Identidades vigentes. */
            identities: number;
            last_error: string | null;
            last_sync_at: components["schemas"]["Timestamp"] | null;
            /** @enum {string|null} */
            last_sync_status: "ok" | "degraded" | "failing" | null;
            /**
             * @description batch = POST /integraciones/horus/resolver; fallback = GET /ip/:ip.
             * @enum {string|null}
             */
            resolver?: "batch" | "fallback" | null;
            stale_identities: number;
            version: number;
            /** @description URL a copiar en ArgosISP (con el secreto). */
            webhook_url: string | null;
        };
        ArgosIntegrationInput: {
            /** Format: uri */
            base_url: string;
            enabled: boolean;
            /** @description Token atk_… de ArgosISP (solo escritura). */
            token?: string;
        };
        ArgosWebhookEvent: {
            /**
             * @example asignacion.cambiada
             * @example sesion.iniciada
             * @example sesion.terminada
             * @example cliente.actualizado
             * @example servicio.estado_cambiado
             */
            evento: string;
            id: string;
            ocurrido_en?: string;
            payload: {
                [key: string]: unknown;
            };
        };
        ArgosWebhookSecret: {
            /** @description Solo en esta respuesta. */
            webhook_secret: string;
            webhook_url: string;
        };
        AsnInfo: {
            /** Format: int64 */
            asn: number;
            catalog_organization: {
                name: string;
                slug: string;
            } | null;
            /** @description ISO 3166-1 alfa-2. */
            country: string | null;
            /** @description Nombre en el dataset público (null si no se conoce). */
            name: string | null;
            tenant_rule: {
                category: string;
                name: string;
                service: string | null;
            } | null;
        };
        AsnRule: components["schemas"]["TenantScopedResource"] & components["schemas"]["AsnRuleInput"] & {
            category: string;
            note: string | null;
            service: string | null;
        };
        AsnRuleCatalog: {
            categories: {
                name: string;
                slug: string;
            }[];
            services: {
                category: string;
                name: string;
                slug: string;
            }[];
        };
        /**
         * @description `category` es obligatoria salvo que se indique `service`: con un servicio del catálogo la categoría es la de ese
         *     servicio (los agregados solo guardan el servicio) y una distinta → 422 en `category`.
         */
        AsnRuleInput: {
            /**
             * Format: int64
             * @example 263237
             */
            asn: number;
            /** @description Slug de una categoría del catálogo (`GET /asn-rules/catalog`). */
            category?: string;
            /** @description Nombre visible en tops, dashboards y kioscos. */
            name: string;
            note?: string | null;
            /** @description Slug de un servicio del catálogo; null = el nombre de la regla hace de servicio. */
            service?: string | null;
        };
        AsnRulePatch: {
            category?: string;
            name?: string;
            note?: string | null;
            service?: string | null;
        };
        AssistAnalysis: {
            context_id: components["schemas"]["Uuid"];
            /** @example Generado por IA; revisa antes de aplicar. Horus no ejecuta nada en tus routers. */
            disclaimer: string;
            duration_ms: number;
            feature: components["schemas"]["AssistFeature"];
            generated_at: components["schemas"]["Timestamp"];
            grounding: components["schemas"]["AssistGrounding"];
            id: components["schemas"]["Uuid"];
            /** @description Markdown (secciones Qué está pasando, Posibles causas, Qué hacer, Para deshacer). Datos de clientes seudonimizados. La UI lo muestra sin HTML. */
            markdown: string;
            model: string;
            provider: components["schemas"]["AssistProvider"];
            tool_calls: components["schemas"]["AssistToolUse"][];
            /** @description La respuesta se cortó por el máximo de tokens. */
            truncated: boolean;
            usage: {
                input_tokens: number;
                output_tokens: number;
            };
        };
        AssistAnalysisRequest: {
            context_id: components["schemas"]["Uuid"];
            feature: components["schemas"]["AssistFeature"];
        };
        AssistAuditEntry: {
            /** @description ID del recurso analizado o nombre de la herramienta MCP. */
            context_id: string;
            created_at: components["schemas"]["Timestamp"];
            duration_ms: number;
            error_code: string | null;
            /** @enum {string} */
            feature: "finding" | "router" | "mcp" | "test";
            id: components["schemas"]["Uuid"];
            input_tokens: number;
            model: string;
            /** @enum {string} */
            outcome: "success" | "refused" | "error" | "limited" | "denied";
            output_tokens: number;
            provider: string;
            tool_calls: components["schemas"]["AssistToolUse"][];
            user_id: components["schemas"]["Uuid"] | null;
        };
        /** @enum {string} */
        AssistEffort: "low" | "medium" | "high";
        /**
         * @description Punto de entrada (enum abierto; llegarán más).
         * @enum {string}
         */
        AssistFeature: "finding" | "router";
        /**
         * @description Verificación de la respuesta contra los datos de Horus (docs/assist.md §Anclaje): cada IP, ASN, UUID, puerto,
         *     porcentaje y cifra con unidad debe aparecer en los resultados de las herramientas, y cada comando RouterOS de
         *     escritura en el catálogo verificado. Si falla, el modelo recibe un turno de corrección; si sigue fallando, la
         *     respuesta lleva un aviso y aquí se lista lo que no se pudo verificar.
         */
        AssistGrounding: {
            missing_sections: string[];
            /** @description Hubo un turno de corrección. */
            repaired: boolean;
            /**
             * @description verified = todo respaldado; partial = hay datos o comandos sin respaldo (listados); unverified = sin evidencia.
             * @enum {string}
             */
            status: "verified" | "partial" | "unverified";
            /** @description Datos de la respuesta que no aparecen en Horus. */
            unsupported: string[];
            /** @description Comandos RouterOS fuera del catálogo verificado. */
            unverified_commands: string[];
        };
        /** @enum {string} */
        AssistProvider: "anthropic" | "openai_compatible";
        AssistSettings: {
            /** @description El ISP guardó su propia clave (nunca se devuelve). */
            api_key_set: boolean;
            /**
             * @description De dónde sale la clave que se usará.
             * @enum {string}
             */
            api_key_source: "tenant" | "platform" | "none";
            /** @description Solo openai_compatible (p. ej. http://10.0.0.5:11434). */
            base_url: string | null;
            daily_request_limit: number;
            effort: components["schemas"]["AssistEffort"];
            enabled: boolean;
            /** @description El servidor puede cifrar claves por ISP (HORUS_ASSIST_KEK_FILE). */
            key_store_available: boolean;
            /** @description El servidor MCP está disponible en esta instalación. */
            mcp_enabled: boolean;
            /**
             * @example claude-opus-5-5
             * @example llama3.1
             */
            model: string;
            platform_default: {
                api_key_set: boolean;
                enabled: boolean;
                model: string;
                provider: components["schemas"]["AssistProvider"];
            };
            provider: components["schemas"]["AssistProvider"];
            /**
             * @description La configuración es del ISP o el valor por defecto de plataforma.
             * @enum {string}
             */
            source: "tenant" | "platform";
            updated_at: components["schemas"]["Timestamp"] | null;
            updated_by: components["schemas"]["Uuid"] | null;
            /** @description Consultas de hoy (UTC) que cuentan para el límite. */
            usage_today: number;
            version: number;
        };
        AssistSettingsUpdate: {
            api_key?: string;
            base_url?: string | null;
            clear_api_key?: boolean;
            daily_request_limit: number;
            effort?: components["schemas"]["AssistEffort"];
            enabled: boolean;
            /** @description Vacío = el del proveedor por defecto. */
            model?: string;
            provider: components["schemas"]["AssistProvider"];
        };
        AssistStatus: {
            /** @description Activo y con proveedor utilizable. */
            enabled: boolean;
            model: string;
            provider: components["schemas"]["AssistProvider"];
            remaining_today: number;
        };
        AssistTestResult: {
            /** @enum {string} */
            error_code?: "PROVIDER_UNAUTHORIZED" | "PROVIDER_RATE_LIMITED" | "PROVIDER_MODEL_NOT_FOUND" | "PROVIDER_BAD_REQUEST" | "PROVIDER_TIMEOUT" | "PROVIDER_UNAVAILABLE";
            latency_ms: number;
            message: string;
            model: string;
            ok: boolean;
            provider: components["schemas"]["AssistProvider"];
        };
        AssistToolUse: {
            count: number;
            errors?: number;
            /** @example get_finding */
            name: string;
        };
        ByteCounters: {
            down_bytes: number;
            up_bytes: number;
        };
        /** @enum {string} */
        CapabilityState: "ok" | "degraded" | "stale" | "unavailable";
        /**
         * @description Un evento llega al canal si su tipo está en `event_types` **o** su categoría en `categories` (al menos una de las
         *     dos con valores), su severidad alcanza `min_severity` y su nodo está en `site_ids`. Compatible con las
         *     suscripciones anteriores (solo `event_types`).
         */
        ChannelSubscription: {
            /** @description Categorías completas (incluye los tipos que se añadan a ellas). */
            categories?: components["schemas"]["NotificationCategory"][];
            event_types: components["schemas"]["NotificationEventType"][];
            min_severity?: components["schemas"]["Severity"];
            /**
             * @description La recuperación de un recurso llega al canal que recibió su alarma sin filtro de severidad ni agrupación.
             * @default true
             */
            notify_recovery?: boolean;
            /** @description Vacío = todos los nodos. */
            site_ids?: components["schemas"]["Uuid"][];
            /**
             * @description Ventana de agrupación por (canal, tipo, recurso) para no inundar.
             * @default 15
             */
            throttle_minutes?: number;
        };
        /**
         * @description Prefijo IPv4/IPv6 en notación CIDR.
         * @example 100.64.0.0/16
         */
        Cidr: string;
        ClientPrefix: components["schemas"]["TenantScopedResource"] & components["schemas"]["ClientPrefixInput"] & {
            confirmed: boolean;
            customers_count?: number | null;
            realm_id: components["schemas"]["Uuid"];
            /** @enum {string} */
            realm_kind: "public" | "node_private";
            site_id: components["schemas"]["Uuid"];
        };
        ClientPrefixInput: {
            /**
             * @default unknown
             * @enum {string}
             */
            assignment_mode?: "static" | "dynamic" | "unknown";
            default_kind?: components["schemas"]["CustomerKind"];
            /**
             * @description Solo IPv6 (por defecto 64).
             * @enum {integer|null}
             */
            ipv6_client_len?: 48 | 56 | 60 | 64 | null;
            note?: string | null;
            prefix: components["schemas"]["Cidr"];
            role: components["schemas"]["ClientPrefixRole"];
            /**
             * @default manual
             * @enum {string}
             */
            source?: "manual" | "routeros_api" | "discovery_confirmed";
        };
        ClientPrefixPatch: {
            /** @enum {string} */
            assignment_mode?: "static" | "dynamic" | "unknown";
            default_kind?: components["schemas"]["CustomerKind"];
            ipv6_client_len?: number | null;
            note?: string | null;
            role?: components["schemas"]["ClientPrefixRole"];
        };
        /** @enum {string} */
        ClientPrefixRole: "customers" | "infrastructure" | "excluded";
        ConnectionTestResult: {
            /** @description telegram: @usuario del bot. */
            bot_username?: string | null;
            channel_id: components["schemas"]["Uuid"];
            channel_kind: components["schemas"]["NotificationChannelKind"];
            /** @description telegram: título del chat comprobado. */
            chat_title?: string | null;
            checked_at: components["schemas"]["Timestamp"];
            /**
             * @description Sin secretos ni cuerpo de la respuesta remota; en Telegram incluye la `description` de la Bot API saneada (sin el
             *     token). `telegram`: `getMe` + `getChat` + `getChatMember` (en un canal el bot debe ser administrador con
             *     `can_post_messages`).
             */
            error?: string | null;
            /** @description Sin valor si `ok`. Enum abierto. */
            error_code?: ("dns_failed" | "connect_failed" | "tls_invalid" | "auth_failed" | "forbidden" | "timeout" | "unexpected_response" | "missing_credentials" | "chat_not_found" | "bot_not_member" | "bot_cannot_post" | "not_allowed") | null;
            latency_ms?: number | null;
            ok: boolean;
            /** @description librenms: versión que informa la API, si la expone. */
            remote_version?: string | null;
        };
        ConsumptionRequest: {
            from: components["schemas"]["Timestamp"];
            /**
             * @default total
             * @enum {string}
             */
            granularity?: "day" | "total";
            items: {
                address?: components["schemas"]["IpAddress"];
                external_service_id?: number;
            }[];
            to: components["schemas"]["Timestamp"];
        };
        ConsumptionResponse: {
            items: {
                address: string | null;
                /** @description Solo por servicio — IPs sumadas (dato personal). */
                addresses?: string[];
                down_bytes: number;
                /** @enum {string} */
                error?: "not_found" | "invalid";
                external_service_id: number | null;
                /** @description Solo con granularity=day (días UTC). */
                series: {
                    /** Format: date */
                    day: string;
                    down_bytes: number;
                    up_bytes: number;
                }[];
                up_bytes: number;
            }[];
            meta: {
                from: components["schemas"]["Timestamp"];
                /** @enum {string} */
                granularity: "day" | "total";
                to: components["schemas"]["Timestamp"];
            };
        };
        CredentialStatus: {
            configured: boolean;
            /** @enum {string} */
            kind: "snmp" | "routeros_api";
            /** @enum {string|null} */
            last_result?: "ok" | "auth_failed" | "unreachable" | "tls_fingerprint_changed" | null;
            last_used_at?: components["schemas"]["Timestamp"] | null;
            /** @description Huella SHA-256 del certificado TLS fijada para la API de RouterOS (solo routeros_api; null hasta el primer contacto). */
            tls_fingerprint_sha256?: string | null;
            updated_at?: components["schemas"]["Timestamp"] | null;
        };
        Customer: {
            /** @description IP canónica (IPv6 truncada al prefijo del cliente). Dato personal. */
            address: components["schemas"]["IpAddress"];
            /** @description Dato personal. */
            alias: string | null;
            /**
             * @description argosisp = nombre del cliente en ArgosISP (nunca pisa uno manual).
             * @enum {string|null}
             */
            alias_source: "manual" | "routeros_ppp" | "argosisp" | null;
            client_prefix_id: components["schemas"]["Uuid"] | null;
            /** @description Última probabilidad comercial del scoring (I2). */
            commercial_score?: number | null;
            commercial_use_suspected: boolean;
            first_seen: components["schemas"]["Timestamp"];
            id: components["schemas"]["Uuid"];
            /** @enum {string|null} */
            inactive_reason?: "no_traffic" | "prefix_removed" | null;
            kind: components["schemas"]["CustomerKind"];
            kind_changed_at: components["schemas"]["Timestamp"] | null;
            kind_confidence: number | null;
            kind_locked: boolean;
            /** @enum {string} */
            kind_source: "default" | "scoring" | "manual";
            /** @description Resolución ≤ 1 h. */
            last_seen: components["schemas"]["Timestamp"];
            open_findings: number;
            realm_id: components["schemas"]["Uuid"];
            reset_at: components["schemas"]["Timestamp"] | null;
            security_state: components["schemas"]["SecurityState"];
            site_id: components["schemas"]["Uuid"];
            /** @enum {string} */
            status: "active" | "inactive";
            suggested_at?: components["schemas"]["Timestamp"] | null;
            suggested_confidence?: number | null;
            /** @description Tipo sugerido por el último scoring (también con el tipo fijado a mano). */
            suggested_kind?: components["schemas"]["CustomerKind"] | null;
            suggested_model_version?: string | null;
            /** @description Razones del último scoring (código estable + detalle con los números reales). */
            suggested_reasons?: components["schemas"]["Reason"][];
            tenant_id: components["schemas"]["Uuid"];
            /** @description Resumen opcional (lo rellena analytics si está disponible; null si ClickHouse no responde). */
            traffic_24h?: {
                down_bytes?: components["schemas"]["Uint64String"];
                top_category?: string | null;
                up_bytes?: components["schemas"]["Uint64String"];
            } | null;
            version: components["schemas"]["Version"];
        };
        CustomerDetail: components["schemas"]["Customer"] & {
            /** @description Dato personal. */
            notes?: string | null;
        };
        CustomerExternalIdentity: {
            connector_enabled: boolean;
            current: components["schemas"]["ExternalRef"] | null;
            customer_id: components["schemas"]["Uuid"];
            history: components["schemas"]["ExternalIdentityPeriod"][];
            /** @enum {string|null} */
            last_check_status: "found" | "not_found" | "error" | null;
            last_checked_at: components["schemas"]["Timestamp"] | null;
        };
        /**
         * @description Tipo de cliente (enum abierto). Por defecto `residential` (D1).
         * @enum {string}
         */
        CustomerKind: "residential" | "commercial" | "unknown";
        CustomerPage: {
            data: components["schemas"]["Customer"][];
            page: components["schemas"]["PageInfo"];
        };
        CustomerPatch: {
            alias?: string | null;
            notes?: string | null;
        };
        CustomerScoring: {
            /** @description Probabilidad comercial tras el ajuste por pool dinámico. */
            commercial_score: number;
            commercial_use_suspected: boolean;
            computed_at: components["schemas"]["Timestamp"];
            /** @description Probabilidad de la clase sugerida. */
            confidence: number;
            customer_id: components["schemas"]["Uuid"];
            /** @description Tipo del cliente al puntuar. */
            customer_kind: string;
            /** @description La IP está en un prefijo dinámico (confianza comercial reducida). */
            dynamic_pool: boolean;
            features: components["schemas"]["ScoringFeature"][];
            /** @description Tipo fijado a mano (el scoring sugiere pero devices no lo cambia). */
            kind_locked: boolean;
            /**
             * @description suspected = residencial con indicios comerciales.
             * @enum {string}
             */
            level: "residential" | "suspected" | "commercial";
            /** @example commercial-v1 */
            model_version: string;
            /**
             * @description Nivel nuevo que aún no ha sostenido la histéresis.
             * @enum {string|null}
             */
            pending_level: "residential" | "suspected" | "commercial" | null;
            pending_since: components["schemas"]["Timestamp"] | null;
            published_at: components["schemas"]["Timestamp"] | null;
            /** @enum {string|null} */
            published_level: "residential" | "suspected" | "commercial" | null;
            /** @description Probabilidad comercial antes del ajuste. */
            raw_score: number;
            reasons: components["schemas"]["Reason"][];
            scores: {
                commercial: number;
                residential: number;
            };
            /** @enum {string} */
            suggested_kind: "residential" | "commercial";
            thresholds: {
                commercial: number;
                hysteresis_days: number;
                suspected: number;
            };
            window_from: components["schemas"]["Timestamp"];
            window_to: components["schemas"]["Timestamp"];
        };
        CustomerScoringPoint: {
            commercial_score: number;
            confidence: number;
            /** Format: date */
            day: string;
            /** @description Tipo vigente del cliente ese día. */
            kind: string;
            /** @enum {string} */
            level: "residential" | "suspected" | "commercial";
            suggested_kind: string;
        };
        CustomerScoringResponse: {
            latest: components["schemas"]["CustomerScoring"] | null;
            series: components["schemas"]["CustomerScoringPoint"][];
            /** @description ClickHouse no respondió; la serie va vacía. */
            series_unavailable?: boolean;
        };
        CustomerStats: {
            active: number;
            by_kind: {
                [key: string]: number;
            };
            by_kind_source: {
                [key: string]: number;
            };
            by_security_state: {
                [key: string]: number;
            };
            by_site: {
                active: number;
                site_id: components["schemas"]["Uuid"];
                with_open_findings?: number;
            }[];
            by_status: {
                [key: string]: number;
            };
            generated_at: components["schemas"]["Timestamp"];
            new_today: number;
            total: number;
        };
        /**
         * Dashboard
         * @description Documento de dashboard (C9; api.md §2.11, frontend.md §6.4): layout en grilla de 12 columnas + widgets con posición y config. Un solo ETag (version) para todo el documento. Las plantillas de Horus son documentos con visibility=system, tenant_id=null, de solo lectura (se duplican).
         */
        "dashboard.schema": {
            created_at: components["schemas"]["$defs-Timestamp"];
            default_range: components["schemas"]["RelativeRange"];
            id: components["schemas"]["$defs-Uuid"];
            layout: components["schemas"]["Layout"];
            name: string;
            owner_id: components["schemas"]["$defs-Uuid"] | null;
            refresh_seconds: number;
            /** @description Clave estable de la plantilla (noc_isp, security) o de la que se duplicó. */
            template_key?: string | null;
            template_version?: number | null;
            /** @description null solo en plantillas de sistema. */
            tenant_id: components["schemas"]["$defs-Uuid"] | null;
            updated_at: components["schemas"]["$defs-Timestamp"];
            variables?: components["schemas"]["Variables"];
            version: number;
            visibility: components["schemas"]["Visibility"];
            widgets: components["schemas"]["Widget"][];
        };
        DashboardInput: {
            default_range?: components["schemas"]["RelativeRange"];
            layout: components["schemas"]["Layout"];
            name: string;
            refresh_seconds?: number;
            variables?: components["schemas"]["Variables"];
            /**
             * @default private
             * @enum {string}
             */
            visibility?: "private" | "tenant";
            widgets: components["schemas"]["Widget"][];
        };
        DashboardPatch: {
            default_range?: components["schemas"]["RelativeRange"];
            name?: string;
            refresh_seconds?: number;
            variables?: components["schemas"]["Variables"];
            /** @enum {string} */
            visibility?: "private" | "tenant";
        };
        DashboardSummary: {
            id: components["schemas"]["$defs-Uuid"];
            name: string;
            owner_id: components["schemas"]["$defs-Uuid"] | null;
            template_key?: string | null;
            tenant_id: components["schemas"]["$defs-Uuid"] | null;
            updated_at: components["schemas"]["$defs-Timestamp"];
            version: number;
            visibility: components["schemas"]["Visibility"];
            widget_count: number;
        };
        DatasetStatus: {
            created_at?: components["schemas"]["Timestamp"];
            /** @description Prefijos del dataset. */
            entries?: number;
            loaded: boolean;
            /** @example 2026-10-13T00:00:00Z */
            version?: string;
        };
        /** EmailChannelConfig */
        EmailChannelConfig: {
            /**
             * @default es
             * @enum {string}
             */
            language?: "es" | "en";
            recipients: string[];
            /** @default [Horus] */
            subject_prefix?: string;
        };
        /**
         * @description Código estable UPPER_SNAKE_CASE (= `ErrorInfo.reason` de gRPC). **Enum abierto**: se listan los
         *     conocidos en v0; el cliente trata un código desconocido según el `status` HTTP.
         */
        ErrorCode: ("VALIDATION_FAILED" | "UNAUTHENTICATED" | "TOKEN_EXPIRED" | "SESSION_REVOKED" | "PERMISSION_DENIED" | "ORIGIN_NOT_ALLOWED" | "MFA_REQUIRED" | "MFA_ENROLLMENT_REQUIRED" | "REAUTH_REQUIRED" | "NOT_FOUND" | "ALREADY_EXISTS" | "CONFLICT" | "PRECONDITION_FAILED" | "PRECONDITION_REQUIRED" | "IDEMPOTENCY_KEY_REUSED" | "IDEMPOTENCY_IN_PROGRESS" | "RATE_LIMITED" | "TENANT_RATE_LIMITED" | "INTERNAL" | "SERVICE_UNAVAILABLE" | "ANALYTICS_UNAVAILABLE" | "TIMEOUT" | "INVALID_CREDENTIALS" | "INVALID_CURSOR" | "INVALID_FILTER" | "INVALID_SORT_FIELD" | "TIME_RANGE_TOO_LARGE" | "TENANT_NOT_FOUND" | "TENANT_MISMATCH" | "TENANT_SUSPENDED" | "TOKEN_SCOPE_INVALID" | "KIOSK_FORBIDDEN" | "ROUTER_NOT_FOUND" | "ROUTER_PRIMARY_EXISTS" | "ROUTEROS_VERSION_UNSUPPORTED" | "ROUTER_UNREACHABLE" | "ROUTER_TLS_FINGERPRINT_CHANGED" | "SITE_NOT_FOUND" | "SITE_NOT_EMPTY" | "PEER_ALREADY_REVOKED" | "WIREGUARD_IP_POOL_EXHAUSTED" | "WIREGUARD_PUBLIC_KEY_IN_USE" | "ENROLLMENT_TOKEN_INVALID" | "CUSTOMER_NOT_FOUND" | "CUSTOMER_KIND_LOCKED" | "CLIENT_PREFIX_OVERLAP" | "CLIENT_PREFIX_NOT_FOUND" | "ASN_RULE_NOT_FOUND" | "FINDING_NOT_FOUND" | "FINDING_STATE_INVALID" | "REPORT_NOT_FOUND" | "REPORT_NOT_READY" | "REPORT_EXPIRED" | "REPORT_DEFINITION_NOT_FOUND" | "DASHBOARD_NOT_FOUND" | "DASHBOARD_READ_ONLY" | "WIDGET_TYPE_NOT_ALLOWED" | "WIDGET_TYPE_UNKNOWN" | "WIDGET_CONFIG_INVALID" | "KIOSK_ENROLLMENT_CODE_INVALID" | "NOTIFICATION_CHANNEL_NOT_FOUND" | "NOTIFICATION_CHANNEL_KIND_NOT_AVAILABLE" | "NOTIFICATION_CHANNEL_UNREACHABLE" | "NOTIFICATION_CHANNEL_AUTH_FAILED" | "NOTIFICATION_CHANNEL_CREDENTIALS_MISSING" | "LIBRENMS_CHANNEL_REQUIRED" | "LIBRENMS_NOT_PROVISIONED" | "LIBRENMS_DEVICE_NOT_OWNED" | "NOTIFICATION_TELEGRAM_WEBHOOK_ACTIVE" | "NOTIFICATION_TELEGRAM_LINK_CODE_INVALID" | "NOTIFICATION_TELEGRAM_BOT_NOT_CONFIGURED" | "STORAGE_TARGET_UNREACHABLE" | "REPUTATION_SOURCE_NOT_FOUND" | "REPUTATION_SOURCE_READ_ONLY" | "REPUTATION_SOURCE_URL_NOT_ALLOWED" | "ROLE_NOT_FOUND" | "ROLE_READ_ONLY" | "ROLE_IN_USE" | "LAST_PLATFORM_ADMIN" | "WIREGUARD_HUB_NOT_FOUND" | "WIREGUARD_POOL_NOT_FOUND" | "WIREGUARD_POOL_OVERLAP" | "WIREGUARD_POOL_INVALID" | "WIREGUARD_POOL_IN_USE" | "REMOTE_DESTINATION_NOT_FOUND" | "REMOTE_DESTINATION_NOT_READY" | "REMOTE_DESTINATION_SYNC_RUNNING" | "API_TOKEN_NOT_FOUND" | "INTEGRATION_NOT_CONFIGURED" | "INTEGRATION_UNAVAILABLE") | string;
        /** @description Evidencia agregada según kind (nunca payloads; nunca la IP del cliente). Campos conocidos; otros permitidos. */
        Evidence: {
            bps_peak?: number;
            bytes_est?: string;
            destination_asns?: number[];
            destination_ports?: number[];
            /** @description Muestra de IPs remotas (no del cliente). */
            destination_sample?: string[];
            destinations_per_minute?: number[];
            distinct_destinations?: number;
            distinct_nets24?: number;
            duration_seconds?: number;
            flows?: number;
            interval_cv?: number;
            interval_seconds?: number;
            packets_est?: string;
            pps_peak?: number;
            protocol?: string;
            reputation_sources?: {
                category?: string;
                indicator: string;
                /** Format: date-time */
                listed_at: string;
                /** @description true si hubo respuesta (no solo SYN). */
                responded?: boolean;
                source: string;
            }[];
            smtp_servers_per_hour?: number;
            spoofing_suspected?: boolean;
            syn_ratio?: number;
            target_asn?: number;
            target_prefix?: string;
            upload_ratio?: number;
        } & {
            [key: string]: unknown;
        };
        ExternalIdentityPeriod: {
            code: string | null;
            display_name: string | null;
            external_customer_id: number | null;
            external_service_id: number | null;
            id: components["schemas"]["Uuid"];
            plan: string | null;
            service_state: string | null;
            stale: boolean;
            synced_at: components["schemas"]["Timestamp"];
            /** @enum {string} */
            system: "argosisp";
            valid_from: components["schemas"]["Timestamp"];
            /** @description null = vigente. */
            valid_to: components["schemas"]["Timestamp"] | null;
        };
        /** @description Identidad del cliente en un sistema externo vigente en el instante consultado (contrato ArgosISP §3.1). */
        ExternalRef: {
            /** @example C-000455 */
            code: string | null;
            /** @description Id del cliente en ArgosISP. */
            customer_id: number | null;
            /** @description Nombre en ArgosISP (dato personal). */
            display_name: string | null;
            plan: string | null;
            /** @description Id del servicio en ArgosISP. */
            service_id: number | null;
            /** @description activo, limitado, suspendido o retirado (enum abierto de ArgosISP). */
            service_state: string | null;
            /** @description ArgosISP no respondió en la última resolución; se muestra el último dato conocido. */
            stale: boolean;
            synced_at: components["schemas"]["Timestamp"];
            /** @enum {string} */
            system: "argosisp";
        };
        FieldError: {
            /**
             * @example INVALID_IP
             * @example NOT_FOUND
             * @example REQUIRED
             */
            code: string;
            /** @description Notación de puntos (`allowed_ips.0`). */
            field: string;
            message: string;
        };
        /**
         * Finding
         * @description Hallazgo de seguridad (C8): correlación explicable de señales compatibles con botnet en una ventana, por cliente (= IP). Misma forma en la API (GET /findings/{id}) y en el payload de horus.detection.finding.opened|updated|resolved (events.md §8.8), salvo `customer` y `recommended_actions[].routeros.rendered_*`, que solo la API rellena y solo para quien tiene customers.read (nunca kioscos ni eventos, events.md §5.7). D11: `recommended_actions` explica qué hacer y propone comandos RouterOS que Horus NUNCA ejecuta (ADR-0022). Lenguaje: 'señales compatibles con…', nunca 'infectado'.
         */
        "finding.schema": {
            acknowledged_at?: components["schemas"]["$defs-Timestamp"] | null;
            acknowledged_by?: components["schemas"]["$defs-Uuid"] | null;
            /**
             * @description Abierto: 'commercial' llegará con el scoring (I2).
             * @enum {string}
             */
            category: "security";
            confidence: number;
            /**
             * @description Banda derivada de confidence (la UI muestra severidad y confianza por separado).
             * @enum {string}
             */
            confidence_level: "low" | "medium" | "high";
            /** @description Solo API. null en eventos. address solo con customers.read y nunca a kioscos (enmascarada). */
            customer?: null | {
                address: string;
                address_masked: boolean;
                alias: string | null;
                kind: string;
            };
            customer_id: components["schemas"]["$defs-Uuid"];
            evidence: components["schemas"]["Evidence"];
            first_seen_at: components["schemas"]["$defs-Timestamp"];
            id: components["schemas"]["$defs-Uuid"];
            kind: components["schemas"]["FindingKind"];
            last_seen_at: components["schemas"]["$defs-Timestamp"];
            min_sampling_rate?: number | null;
            /** @description Ocurrencias acumuladas en el hallazgo abierto (deduplicación por ISP, cliente, kind y objetivo principal). */
            occurrences: number;
            opened_at: components["schemas"]["$defs-Timestamp"];
            /** @description Hallazgo resuelto anterior del mismo patrón (reincidente). */
            previous_finding_id?: components["schemas"]["$defs-Uuid"] | null;
            /** @description Objetivo principal que forma la clave de deduplicación junto con (tenant, customer, kind). */
            primary_target: {
                /** @enum {string} */
                type: "remote_ip" | "remote_port" | "remote_asn" | "remote_prefix" | "none";
                value: string | null;
            };
            realm_id: components["schemas"]["$defs-Uuid"];
            reasons: components["schemas"]["$defs-Reason"][];
            recommended_actions: components["schemas"]["RecommendedAction"][];
            reputation_snapshot_version?: number | null;
            resolution?: null | {
                actions_taken?: string[];
                comment?: string | null;
                resolved_at: components["schemas"]["$defs-Timestamp"];
                resolved_by: components["schemas"]["$defs-Uuid"] | null;
                silence_until?: components["schemas"]["$defs-Timestamp"] | null;
                /** @enum {string} */
                verdict: "resolved" | "false_positive" | "auto_expired";
            };
            router_id: components["schemas"]["$defs-Uuid"];
            /** @example c2-contact@4 */
            rule_version: string;
            /** @default false */
            sampling_reduced_confidence?: boolean;
            /** @enum {string} */
            severity: "low" | "medium" | "high" | "critical";
            site_id: components["schemas"]["$defs-Uuid"];
            state: components["schemas"]["FindingState"];
            /** @enum {string} */
            subject_type: "customer";
            summary: {
                code: string;
                params?: {
                    [key: string]: string | number | boolean;
                };
                /** @description Resumen legible en español, sin IP del cliente (p. ej. 'Escaneo del puerto 23 a 1 240 destinos en 5 min'). */
                text: string;
            };
            tenant_id: components["schemas"]["$defs-Uuid"];
            updated_at: components["schemas"]["$defs-Timestamp"];
            version: number;
            window_from: components["schemas"]["$defs-Timestamp"];
            window_to: components["schemas"]["$defs-Timestamp"];
        };
        /** @description Enum abierto (api.md §2.10, events.md §8.8). Consumidores tratan valores desconocidos como 'otro'. */
        FindingKind: ("botnet_c2_communication" | "ddos_participation" | "outbound_scanning" | "spam_smtp_outbound" | "open_proxy_abuse" | "cryptomining" | "beaconing" | "reputation_hit") | string;
        /** @enum {string} */
        FindingState: "open" | "acknowledged" | "resolved" | "false_positive";
        FindingTransition: {
            /** @description Códigos de las acciones recomendadas que el operador aplicó a mano (D11; informativo). */
            actions_taken?: string[];
            comment?: string | null;
        };
        FlowExporter: {
            clock_skew_seconds: number | null;
            /** @description Bytes en flujos / bytes del uplink por SNMP (I2+); null si no hay SNMP. */
            coverage_ratio?: number | null;
            dropped_records_quota_1h?: components["schemas"]["Uint64String"];
            /** @description IP de túnel del router (identidad del exportador). */
            exporter_ip: string | null;
            /** @enum {string|null} */
            flow_source: "ipfix" | "netflow_v9" | "netflow_v5" | null;
            flows_per_second: number | null;
            /** @description Qué revisar (códigos traducibles), p. ej. firewall, ruta al hub, Traffic Flow, offload por hardware. */
            hints?: ("check_tunnel" | "check_traffic_flow_target" | "check_firewall" | "hardware_offload_suspected" | "check_ntp")[];
            last_flow_at: components["schemas"]["Timestamp"] | null;
            loss_ratio_5m: number | null;
            router_id: components["schemas"]["Uuid"];
            sampling_rate: number | null;
            site_id: components["schemas"]["Uuid"];
            state: components["schemas"]["FlowExporterState"];
            state_since: components["schemas"]["Timestamp"];
            tenant_id: components["schemas"]["Uuid"];
            version: components["schemas"]["Version"];
        };
        /**
         * @description Enum abierto.
         * @enum {string}
         */
        FlowExporterState: "pending_configuration" | "exporting" | "silent" | "lossy" | "clock_skew";
        /** @description Un chequeo. Solo se aceptan los campos de su `type`; `id` lo asigna el servidor y se conserva al editar. */
        InfraCheck: {
            /** @description ddos_inbound. */
            bps_threshold?: number | null;
            /** @description http; por defecto 399. */
            expected_status_max?: number | null;
            /** @description http; por defecto 200. */
            expected_status_min?: number | null;
            /** @description tcp y http con `probe_via_router_id`; puerto de la IP de túnel del router que reenvía a `port` del servidor. */
            forward_port?: number | null;
            /** @description http. */
            https?: boolean | null;
            id?: components["schemas"]["Uuid"];
            /** @description no_traffic; por defecto 1. */
            min_bytes?: number | null;
            /** @description http; ruta absoluta, por defecto /. */
            path?: string | null;
            /** @description tcp y http (http: 80/443 por defecto). */
            port?: number | null;
            /** @description ddos_inbound (al menos uno de los dos umbrales). */
            pps_threshold?: number | null;
            /** @description http con https; por defecto true. */
            tls_verify?: boolean | null;
            type: components["schemas"]["InfraCheckType"];
            /** @description no_traffic; por defecto 15. */
            window_minutes?: number | null;
        };
        InfraCheckResult: {
            at: components["schemas"]["Timestamp"];
            latency_ms: number | null;
            reason: string | null;
            state: components["schemas"]["InfraServiceState"];
        };
        /** @description Estado actual de un chequeo con histéresis (`failures_before_down` fallos seguidos → down; `recoveries_before_up` éxitos → up). */
        InfraCheckStatus: {
            check_id: components["schemas"]["Uuid"];
            consecutive_failures: number;
            consecutive_successes: number;
            /** @description Medida del último resultado sin datos sensibles (p. ej. "HTTP 503", "1250 pps entrantes"). */
            detail: string | null;
            last_checked_at: components["schemas"]["Timestamp"] | null;
            latency_ms: number | null;
            /** @description Enum abierto: ok, timeout, refused, unreachable, tls_error, http_status, no_traffic, ddos_inbound, no_flow_data, icmp_unavailable, router_unavailable… */
            reason: string | null;
            /** @description Solo en el detalle (GET /infra-services/{id}); últimos resultados, el más reciente primero. */
            recent?: components["schemas"]["InfraCheckResult"][];
            since: components["schemas"]["Timestamp"] | null;
            state: components["schemas"]["InfraServiceState"];
            type: components["schemas"]["InfraCheckType"];
        };
        /**
         * @description `icmp` (ping), `tcp` (conexión al puerto), `http` (GET con estado esperado; no se guarda el cuerpo) — activos,
         *     desde el rol `snmp`. `no_traffic` (bytes hacia/desde el objetivo en la ventana por debajo del mínimo, solo si
         *     antes tuvo tráfico) y `ddos_inbound` (pps/bps entrantes por encima del umbral → `degraded`) — pasivos, desde
         *     los flujos.
         * @enum {string}
         */
        InfraCheckType: "icmp" | "tcp" | "http" | "no_traffic" | "ddos_inbound";
        InfraRouterSnippet: {
            /** @description false si el objetivo no es privado o no hay chequeos tcp/http con forward_port. */
            applicable: boolean;
            /** @description Qué cambia (o no) del lado de Horus. */
            horus_side: string;
            router_id: components["schemas"]["Uuid"];
            router_name: string;
            /** @description Script RouterOS (.rsc) para pegar; vacío si no aplica. */
            script: string;
            warnings: string[];
        };
        InfraSchedulerHealth: {
            /** @description false: el rol snmp no vigila (HORUS_SNMP_ENABLED=false). */
            enabled: boolean;
            healthy: boolean;
            last_cycle_at: components["schemas"]["Timestamp"] | null;
            /** @description false: sin lector de flujos (ClickHouse); no_traffic y ddos_inbound quedan unknown. */
            passive_checks: boolean;
        };
        InfraService: components["schemas"]["TenantScopedResource"] & {
            check_status: components["schemas"]["InfraCheckStatus"][];
            checks: components["schemas"]["InfraCheck"][];
            enabled: boolean;
            failures_before_down: number;
            interval_seconds: number;
            /**
             * @description Etiqueta libre (iptv | http | dns | radius | other | …).
             * @example iptv
             * @example http
             * @example dns
             * @example radius
             * @example other
             */
            kind: string;
            name: string;
            /** @description Si se fija, los chequeos tcp/http van a la IP de túnel de ese router en `forward_port` (servidor privado con reenvío). */
            probe_via_router_id: components["schemas"]["Uuid"] | null;
            recoveries_before_up: number;
            site_id: components["schemas"]["Uuid"] | null;
            status: components["schemas"]["InfraServiceStatus"];
            /** @description IP del servidor del ISP (infraestructura propia, no de un cliente). */
            target: components["schemas"]["IpAddress"];
            /** @description RFC 1918 / ULA / CGNAT: puede necesitar el script de router. */
            target_private: boolean;
        };
        InfraServiceInput: {
            checks: components["schemas"]["InfraCheck"][];
            /** @default true */
            enabled?: boolean;
            /** @default 3 */
            failures_before_down?: number;
            /** @default 60 */
            interval_seconds?: number;
            kind: string;
            name: string;
            probe_via_router_id?: components["schemas"]["Uuid"] | null;
            /** @default 2 */
            recoveries_before_up?: number;
            site_id?: components["schemas"]["Uuid"] | null;
            target: components["schemas"]["IpAddress"];
        };
        /** @description JSON Merge Patch; `checks` reemplaza la lista completa. */
        InfraServicePatch: {
            checks?: components["schemas"]["InfraCheck"][];
            enabled?: boolean;
            failures_before_down?: number;
            interval_seconds?: number;
            kind?: string;
            name?: string;
            probe_via_router_id?: components["schemas"]["Uuid"] | null;
            recoveries_before_up?: number;
            site_id?: components["schemas"]["Uuid"] | null;
            target?: components["schemas"]["IpAddress"];
        };
        /**
         * @description Estado de un servicio o chequeo. `degraded` = responde pero con un problema (p. ej. `ddos_inbound`).
         * @enum {string}
         */
        InfraServiceState: "up" | "down" | "degraded" | "unknown";
        /** @description Estado del servicio = el peor de sus chequeos conocidos (down > degraded > up); `unknown` solo si ningún chequeo tiene estado todavía. */
        InfraServiceStatus: {
            /** @description Chequeo que determina el estado (null si todos están up o no hay chequeos). */
            check_type: components["schemas"]["InfraCheckType"] | null;
            reason: string | null;
            since: components["schemas"]["Timestamp"] | null;
            state: components["schemas"]["InfraServiceState"];
        };
        InstallationAccess: {
            access_mode: components["schemas"]["AccessMode"];
            allowed_origins: string[];
            /**
             * @description Nombre o IP literal de HORUS_PUBLIC_BASE_URL.
             * @example horus.isp.example
             * @example 203.0.113.10
             */
            host: string;
            /**
             * Format: uri
             * @description HORUS_PUBLIC_BASE_URL; siempre `https://` (también en `ip_only`).
             * @example https://horus.isp.example
             * @example https://203.0.113.10
             */
            public_base_url: string;
            tls: {
                /** @description Huella del certificado servido, para verificarla a mano con self_signed. */
                fingerprint_sha256: string | null;
                /** @description true solo con nombre y certificado de confianza pública (`domain`/`subdomain` + `acme`). */
                hsts: boolean;
                issuer?: string | null;
                mode: components["schemas"]["TlsMode"];
                not_after: components["schemas"]["Timestamp"] | null;
            };
            /** @description Avisos para la consola de plataforma (enum abierto de `code`). */
            warnings: {
                code: ("ip_only_access" | "self_signed_certificate" | "certificate_expiring" | "acme_renewal_failed" | "proxy_forwarded_proto_http" | "proxy_untrusted_source") | string;
                message: string;
                /** @enum {string} */
                severity: "info" | "warning" | "critical";
            }[];
            /** @description HORUS_WG_ENDPOINT (nombre o IP). */
            wireguard_endpoint: string;
        };
        /** @enum {string} */
        InterfaceOperStatus: "up" | "down" | "testing" | "unknown" | "dormant" | "not_present" | "lower_layer_down";
        /** @description Última muestra de una interfaz sondeada. Tasas null en el primer sondeo o tras una discontinuidad de contador. */
        InterfaceSample: {
            alias?: string | null;
            /** @default false */
            counter_discontinuity?: boolean;
            if_index: number;
            interface_id: components["schemas"]["Uuid"];
            name: string;
            oper_status: components["schemas"]["InterfaceOperStatus"] | null;
            rx_bps: number | null;
            rx_drops_delta?: number | null;
            rx_errors_delta?: number | null;
            rx_pps?: number | null;
            speed_bps?: number | null;
            tx_bps: number | null;
            tx_drops_delta?: number | null;
            tx_errors_delta?: number | null;
            tx_pps?: number | null;
            /** @default false */
            uplink?: boolean;
            utilization_rx?: number | null;
            utilization_tx?: number | null;
        };
        InvitationIssued: {
            email_sent: boolean;
            /** Format: date-time */
            expires_at?: string | null;
            /**
             * Format: uri
             * @description Solo con outcome=invited. <HORUS_PUBLIC_BASE_URL>/invitation#token=<token>; se muestra una vez.
             */
            invitation_url?: string | null;
            /**
             * @description added = la cuenta ya existía y la membresía queda activa; invited = hay que aceptar el enlace.
             * @enum {string}
             */
            outcome: "added" | "invited";
            user_id: components["schemas"]["Uuid"];
        };
        /**
         * @description IPv4 o IPv6. **Dato personal** cuando es la IP de un cliente (nunca en URLs).
         * @example 100.64.12.34
         */
        IpAddress: string;
        IpLookupRequest: {
            address: components["schemas"]["IpAddress"];
            /** @description Instante de la consulta (por defecto ahora). */
            at?: components["schemas"]["Timestamp"];
            /** @description Bloques opcionales (por defecto los tres). */
            include?: ("consumption" | "health" | "security")[];
        };
        IpLookupResponse: {
            consumption: {
                last_7d: components["schemas"]["ByteCounters"];
                last_24h: components["schemas"]["ByteCounters"];
                last_30d: components["schemas"]["ByteCounters"];
                /** @description Pico de bajada en buckets de 5 min. */
                peak_down_bps_24h: number;
                top_services_7d: {
                    bytes: number;
                    name: string;
                }[];
            } | null;
            customer: {
                address: components["schemas"]["IpAddress"];
                alias: string | null;
                /** @enum {string|null} */
                alias_source: "manual" | "routeros_ppp" | "argosisp" | null;
                first_seen: components["schemas"]["Timestamp"];
                id: components["schemas"]["Uuid"];
                kind: components["schemas"]["CustomerKind"];
                /** @enum {string} */
                kind_source: "default" | "scoring" | "manual";
                last_seen: components["schemas"]["Timestamp"];
                site: {
                    id: components["schemas"]["Uuid"];
                    name: string | null;
                };
                /** @enum {string} */
                status: "active" | "inactive";
            };
            external: components["schemas"]["ExternalRef"] | null;
            health: {
                exporter: {
                    last_flow_at: components["schemas"]["Timestamp"] | null;
                    /** @description FlowExporterState del router principal del nodo. */
                    status: string | null;
                };
                last_seen_minutes: number;
                router: {
                    id: components["schemas"]["Uuid"];
                    name: string;
                    since: components["schemas"]["Timestamp"] | null;
                    /** @description online, warning, critical, degraded, offline o unknown. */
                    status: string;
                } | null;
            } | null;
            meta: {
                at: components["schemas"]["Timestamp"];
                omitted: {
                    /** @enum {string} */
                    reason: "permission" | "unavailable" | "not_requested" | "not_configured";
                    /** @enum {string} */
                    section: "external" | "consumption" | "health" | "security";
                }[];
            };
            security: {
                findings: {
                    id: components["schemas"]["Uuid"];
                    kind: string;
                    opened_at: components["schemas"]["Timestamp"];
                    severity: string;
                    state?: string;
                    summary: string;
                }[];
                open_findings: number;
                state: components["schemas"]["SecurityState"];
            } | null;
        };
        KindChange: {
            actor_id: components["schemas"]["Uuid"] | null;
            changed_at: components["schemas"]["Timestamp"];
            confidence: number | null;
            from_kind: components["schemas"]["CustomerKind"] | null;
            id: components["schemas"]["Uuid"];
            manual_reason: string | null;
            model_ref: string | null;
            reasons: components["schemas"]["Reason"][];
            /** @enum {string} */
            source: "default" | "scoring" | "manual" | "reset";
            to_kind: components["schemas"]["CustomerKind"];
        };
        Kiosk: components["schemas"]["TenantScopedResource"] & {
            allowed_cidrs: components["schemas"]["Cidr"][];
            /** @description true si no hay `allowed_cidrs` (la UI lo marca como riesgo). */
            cidr_risk?: boolean;
            critical_finding_banner?: boolean;
            dashboard_ids: components["schemas"]["Uuid"][];
            expires_at: components["schemas"]["Timestamp"];
            frontend_version?: string | null;
            last_ip?: string | null;
            last_seen_at?: components["schemas"]["Timestamp"] | null;
            name: string;
            playlist_id: components["schemas"]["Uuid"] | null;
            show_personal_data: boolean;
            /** @enum {string} */
            status: "pending_enrollment" | "active" | "revoked" | "expired";
        };
        KioskInput: {
            allowed_cidrs?: components["schemas"]["Cidr"][];
            /** @default false */
            critical_finding_banner?: boolean;
            dashboard_ids?: components["schemas"]["Uuid"][];
            expires_at?: components["schemas"]["Timestamp"];
            name?: string;
            playlist_id?: components["schemas"]["Uuid"] | null;
            /** @default false */
            show_personal_data?: boolean;
            /** @description Obligatorio (auditado) si `show_personal_data = true`. */
            show_personal_data_reason?: string | null;
        };
        Layout: {
            breakpoints?: {
                lg?: number;
                md?: number;
                sm?: number;
            };
            /** @constant */
            columns: 12;
            /** @constant */
            grid: "12-col";
            row_height_px: number;
        };
        LayoutReplace: {
            layout: components["schemas"]["Layout"];
            /** @description widget_id → posición; debe cubrir todos los widgets del dashboard. */
            positions: {
                [key: string]: components["schemas"]["Position"];
            };
        };
        /**
         * LibreNmsChannelConfig
         * @description D17: LibreNMS como destino por **su API** (instancia dedicada a Horus), configurado a mano por cada ISP.
         *     Solo la parte no secreta: contraseña y token van por `PUT /notification-channels/{id}/credentials`
         *     (write-only). LibreNMS como fuente de inventario queda fuera del I1.
         */
        LibreNmsChannelConfig: {
            /**
             * Format: uri
             * @description URL base de la instancia (p. ej. `https://librenms.isp.example`); Horus añade `/api/v0`. Sin credenciales en la URL.
             * @example https://librenms.isp.example
             */
            base_url: string;
            /** @default 10 */
            timeout_seconds?: number;
            /**
             * @description false solo para certificados autofirmados (auditado).
             * @default true
             */
            tls_verify?: boolean;
            /** @description Usuario de LibreNMS dedicado a Horus. */
            username: string;
        };
        /**
         * @description Enum abierto.
         * @enum {string}
         */
        LibreNmsErrorCode: "dns_failed" | "connect_failed" | "tls_invalid" | "timeout" | "auth_failed" | "forbidden" | "not_found" | "rejected" | "remote_error" | "unexpected_response" | "missing_credentials" | "no_default_transport" | "librenms_not_provisioned" | "device_missing" | "rule_missing" | "syslog_not_stored" | "devices_unavailable";
        LibreNmsIntegration: {
            channel_id: components["schemas"]["Uuid"];
            cpu_threshold: number;
            /** @description Host del dispositivo «Horus Flow» en LibreNMS. */
            horus_hostname: string | null;
            /** @description Host de HORUS_PUBLIC_BASE_URL. */
            horus_hostname_default: string | null;
            last_error: string | null;
            last_error_code: components["schemas"]["LibreNmsErrorCode"] | null;
            last_sync_at: components["schemas"]["Timestamp"] | null;
            last_sync_steps: components["schemas"]["LibreNmsSyncStep"][];
            last_test: components["schemas"]["LibreNmsTestResult"] | null;
            memory_threshold: number;
            objects: components["schemas"]["LibreNmsObject"][];
            remote_version: string | null;
            /**
             * @description `partial`: la última sincronización terminó con pasos fallidos.
             * @enum {string}
             */
            status: "not_provisioned" | "ok" | "partial" | "failed";
            /** @description Desfase horario de LibreNMS respecto de UTC. */
            tz_offset_minutes: number | null;
            warnings: string[];
        };
        /**
         * @description `ping` (por defecto): solo ICMP. `snmp_v2c` / `snmp_v3`: credenciales propias para LibreNMS (write-only).
         *     `horus_snmp_v3`: las credenciales SNMPv3 de solo lectura de Horus, con consentimiento explícito por router.
         * @enum {string}
         */
        LibreNmsMonitoringMode: "ping" | "snmp_v2c" | "snmp_v3" | "horus_snmp_v3";
        /** @description Objeto de LibreNMS que Horus conoce (creado por Horus o adoptado porque ya existía). */
        LibreNmsObject: {
            /** @description false: el router ya no está configurado (fuera del grupo, conservado). */
            active: boolean;
            /** @description false: ya existía y Horus solo lo usa (nunca lo borra ni edita). */
            created_by_horus: boolean;
            /** @description Dispositivos: host con el que está en LibreNMS. */
            hostname: string | null;
            /** @description Clave estable (`horus`, `routers`, `infra`, `cpu_high`…, el id del router o el nombre del servicio). */
            key: string;
            /** @enum {string} */
            kind: "device" | "router_device" | "infra_device" | "group" | "template" | "rule";
            name: string;
            /** @description Id en LibreNMS. */
            remote_id: number;
        };
        LibreNmsRouter: {
            has_snmp_credentials: boolean;
            horus_snmp_reuse_consent: boolean;
            /** @description IP de gestión para LibreNMS (no la del túnel). null = el router no se monitorea en LibreNMS. */
            mgmt_ip: string | null;
            mode: components["schemas"]["LibreNmsMonitoringMode"];
            onboarding_state: string;
            router_id: components["schemas"]["Uuid"];
            router_name: string;
            site_id: components["schemas"]["Uuid"];
            updated_at: components["schemas"]["Timestamp"] | null;
        };
        LibreNmsRouterInput: {
            /** @description IPv4 o IPv6 de gestión alcanzable desde LibreNMS. */
            mgmt_ip: string;
            mode: components["schemas"]["LibreNmsMonitoringMode"];
            /**
             * @description Obligatorio (true) con `horus_snmp_v3`. Auditado.
             * @default false
             */
            reuse_horus_snmp_consent?: boolean;
        };
        /** @description Write-only. `community` en `snmp_v2c`; `v3_*` en `snmp_v3`. */
        LibreNmsSnmpCredentials: {
            community?: string;
            /** @enum {string} */
            v3_auth_level?: "noAuthNoPriv" | "authNoPriv" | "authPriv";
            v3_auth_password?: string;
            /** @enum {string} */
            v3_auth_protocol?: "MD5" | "SHA" | "SHA-224" | "SHA-256" | "SHA-384" | "SHA-512";
            v3_priv_password?: string;
            /** @enum {string} */
            v3_priv_protocol?: "AES" | "DES";
            v3_username?: string;
        };
        LibreNmsSyncInput: {
            /** @default 85 */
            cpu_threshold?: number;
            /** @description Host público de Horus tal como LibreNMS lo verá (por defecto el de HORUS_PUBLIC_BASE_URL). */
            horus_hostname?: string;
            /** @default 90 */
            memory_threshold?: number;
        };
        LibreNmsSyncResult: {
            error: string | null;
            error_code: components["schemas"]["LibreNmsErrorCode"] | null;
            integration: components["schemas"]["LibreNmsIntegration"];
            /** @description true si terminó sin pasos fallidos. */
            ok: boolean;
            steps: components["schemas"]["LibreNmsSyncStep"][];
            warnings: string[];
        };
        LibreNmsSyncStep: {
            /** @enum {string} */
            action: "created" | "adopted" | "updated" | "unchanged" | "recreated" | "removed" | "deleted" | "failed" | "skipped";
            /** @description `message` de LibreNMS saneado (sin secretos). */
            error?: string;
            error_code?: components["schemas"]["LibreNmsErrorCode"];
            key: string;
            /** @enum {string} */
            kind: "device" | "router_device" | "infra_device" | "group" | "template" | "rule";
            name: string;
            remote_id: number | null;
        };
        LibreNmsTestResult: {
            channel_id: components["schemas"]["Uuid"];
            checked_at: components["schemas"]["Timestamp"];
            error: string | null;
            error_code: components["schemas"]["LibreNmsErrorCode"] | null;
            failed_step: ("auth" | "device" | "rule" | "syslog_send" | "syslog_stored" | "eventlog") | null;
            latency_ms?: number | null;
            /** @description Línea de syslog enviada. */
            line?: string | null;
            ok: boolean;
            remote_version?: string | null;
            steps: components["schemas"]["LibreNmsTestStep"][];
        };
        LibreNmsTestStep: {
            detail?: string;
            error_code?: components["schemas"]["LibreNmsErrorCode"];
            ok: boolean;
            /** @enum {string} */
            step: "auth" | "device" | "rule" | "syslog_send" | "syslog_stored" | "eventlog";
        };
        McpToken: {
            active: boolean;
            created_at: components["schemas"]["Timestamp"];
            expires_at: components["schemas"]["Timestamp"];
            id: components["schemas"]["Uuid"];
            last_used_at: components["schemas"]["Timestamp"] | null;
            name: string;
            /** @description Primeros caracteres del token (para reconocerlo). */
            prefix: string;
            revoked_at: components["schemas"]["Timestamp"] | null;
            user_id: components["schemas"]["Uuid"];
        };
        McpTokenIssued: components["schemas"]["McpToken"] & {
            /** @description Valor del token (hmcp_…). Solo en esta respuesta. */
            token: string;
        };
        Me: {
            default_tenant_id?: components["schemas"]["Uuid"] | null;
            display_name: string;
            /** Format: email */
            email: string;
            id: components["schemas"]["Uuid"];
            /** @example es-MX */
            locale?: string | null;
            memberships: components["schemas"]["Membership"][];
            mfa_enabled: boolean;
            must_change_password: boolean;
            platform_permissions: string[];
            platform_roles: ("platform_admin" | "platform_operator" | "platform_auditor")[];
            /** @example America/Mexico_City */
            timezone?: string | null;
        };
        Member: {
            display_name: string;
            /** Format: email */
            email: string;
            mfa_enabled: boolean;
            role_assignments: components["schemas"]["RoleAssignment"][];
            /** @enum {string} */
            status: "active" | "invited" | "revoked";
            tenant_id?: components["schemas"]["Uuid"];
            user_id: components["schemas"]["Uuid"];
            version: components["schemas"]["Version"];
        };
        MemberPatch: {
            display_name?: string;
            /** Format: email */
            email?: string;
        };
        Membership: {
            /**
             * @description Permiso → alcances (`["*"]` = todo el tenant; `site:<uuid>`, `router_group:<uuid>`).
             * @example {
             *       "devices.read": [
             *         "*"
             *       ],
             *       "customers.kind.write": [
             *         "site:0192e111-0000-7000-8000-000000000001"
             *       ]
             *     }
             */
            permissions_with_scope: {
                [key: string]: string[];
            };
            roles: components["schemas"]["RoleAssignment"][];
            tenant_id: components["schemas"]["Uuid"];
            tenant_name: string;
            tenant_slug: string;
            /** @enum {string} */
            tenant_status: "active" | "suspended" | "offboarding";
        };
        MePatch: {
            default_tenant_id?: components["schemas"]["Uuid"] | null;
            display_name?: string;
            /**
             * Format: email
             * @description Correo nuevo (identidad de la cuenta en toda la plataforma). Exige re-autenticación reciente.
             */
            email?: string;
            locale?: string | null;
            timezone?: string | null;
        };
        Meta: {
            /** @enum {string} */
            cache?: "hit" | "miss";
            coverage?: number | null;
            /** @enum {string} */
            data_endpoint_kind: "state" | "series" | "table";
            /** @description Edad del dato más reciente (la UI marca atrasado > 2× el intervalo esperado y obsoleto > 5×). */
            freshness_seconds?: number | null;
            /** Format: date-time */
            from?: string | null;
            /** Format: date-time */
            generated_at: string;
            /** @description true si se enmascararon IPs (kiosco sin show_personal_data o usuario sin customers.read). */
            masked_personal_data: boolean;
            partial: boolean;
            step?: number | null;
            /** Format: date-time */
            to?: string | null;
            widget_type: string;
        };
        MfaChallenge: {
            /** @constant */
            mfa_required: true;
            mfa_token: string;
        };
        /**
         * @description Grupo de tipos de evento (ver NotificationEventType). Enum abierto.
         * @enum {string}
         */
        NotificationCategory: "security" | "routers" | "infrastructure" | "exporter_tunnel" | "security_updates";
        NotificationChannel: components["schemas"]["TenantScopedResource"] & components["schemas"]["NotificationChannelInput"] & {
            has_credentials: boolean;
            last_delivery_at: components["schemas"]["Timestamp"] | null;
            /** @description Sin datos sensibles. */
            last_error: string | null;
            /** @enum {string} */
            status: "unverified" | "ok" | "failing" | "disabled";
        };
        /** @description Secretos write-only por `kind` (nunca devueltos). Ver `PUT /notification-channels/{id}/credentials`. */
        NotificationChannelCredentials: {
            /** @description librenms: token de API (opcional). */
            api_token?: string;
            /** @description librenms: contraseña del usuario. */
            password?: string;
            telegram_bot_token?: string;
            /** @description webhook: secreto HMAC que genera el receptor. */
            webhook_secret?: string;
        };
        NotificationChannelInput: {
            config: components["schemas"]["EmailChannelConfig"] | components["schemas"]["TelegramChannelConfig"] | components["schemas"]["LibreNmsChannelConfig"] | components["schemas"]["WebhookChannelConfig"];
            /** @default true */
            enabled?: boolean;
            /**
             * @description true ⇒ los mensajes incluyen la IP del cliente (auditado). Por defecto alias o IP enmascarada.
             * @default false
             */
            include_personal_data?: boolean;
            kind: components["schemas"]["NotificationChannelKind"];
            name: string;
            subscription: components["schemas"]["ChannelSubscription"];
        };
        /**
         * @description `email`, `telegram`, `librenms` (D17, por API) y `webhook` (POST JSON firmado con HMAC-SHA256, ADR-0030;
         *     contrato docs/integrations/argosisp.md §1.3 y §3.4) disponibles. Enum abierto (después: whatsapp, sms); un
         *     `kind` conocido por el contrato pero aún no implementado → `422 NOTIFICATION_CHANNEL_KIND_NOT_AVAILABLE`.
         * @enum {string}
         */
        NotificationChannelKind: "email" | "telegram" | "librenms" | "webhook";
        NotificationChannelPatch: {
            config?: components["schemas"]["EmailChannelConfig"] | components["schemas"]["TelegramChannelConfig"] | components["schemas"]["LibreNmsChannelConfig"] | components["schemas"]["WebhookChannelConfig"];
            enabled?: boolean;
            include_personal_data?: boolean;
            name?: string;
            subscription?: components["schemas"]["ChannelSubscription"];
        };
        NotificationDelivery: {
            channel_id: components["schemas"]["Uuid"];
            channel_kind: components["schemas"]["NotificationChannelKind"];
            created_at: components["schemas"]["Timestamp"];
            error?: string | null;
            event_type?: components["schemas"]["NotificationEventType"];
            id: components["schemas"]["Uuid"];
            is_test?: boolean;
            /** @description Recurso que agrupa avisos y empareja recuperaciones (router, interfaz, servicio…). */
            resource_id?: string | null;
            sent_at?: components["schemas"]["Timestamp"] | null;
            severity?: components["schemas"]["Severity"] | null;
            source_event_id: components["schemas"]["Uuid"] | null;
            /** @example horus.detection.finding.opened */
            source_event_type: string | null;
            /** @enum {string} */
            status: "queued" | "sent" | "failed" | "throttled";
            /** @description Asunto del mensaje enviado (sin datos personales). */
            summary?: string | null;
            tenant_id: components["schemas"]["Uuid"];
        };
        /**
         * @description Tipos de evento a los que se suscribe un canal (enum abierto), por categoría:
         *     - `security`: `finding_opened`, `finding_reopened`.
         *     - `security_updates` (pensada para webhooks, ADR-0030): `finding_resolved` y `customer_security_state_changed`.
         *     - `routers` (rol snmp): `router_offline`, `router_recovered` (vuelve tras una caída o un umbral avisado),
         *       `router_cpu_high`, `router_memory_high`, `router_temperature_high` (aviso → `medium`, crítico → `high`),
         *       `router_hardware_failed` (fuente o ventilador), `router_interface_down` / `router_interface_up` (enlace de
         *       subida → `high`), `router_rebooted`.
         *     - `infrastructure` (servicios vigilados, `horus.snmp.service.state_changed`): `service_down`,
         *       `service_degraded`, `service_recovered`.
         *     - `exporter_tunnel`: `exporter_silent`, `exporter_recovered`, `tunnel_down`, `tunnel_recovered`.
         *     Las recuperaciones (`*_recovered`, `router_interface_up`) tienen severidad `info`.
         * @enum {string}
         */
        NotificationEventType: "finding_opened" | "finding_reopened" | "exporter_silent" | "exporter_recovered" | "tunnel_down" | "tunnel_recovered" | "router_offline" | "router_recovered" | "router_cpu_high" | "router_memory_high" | "router_temperature_high" | "router_hardware_failed" | "router_interface_down" | "router_interface_up" | "router_rebooted" | "service_down" | "service_degraded" | "service_recovered" | "finding_resolved" | "customer_security_state_changed";
        PageInfo: {
            has_more: boolean;
            limit: number;
            next_cursor: string | null;
            prev_cursor: string | null;
            /** @description Solo con `include_total=true` en colecciones administrativas. */
            total?: number | null;
        };
        PasswordResetIssued: {
            email_sent: boolean;
            /** Format: date-time */
            expires_at: string;
            /**
             * Format: uri
             * @description <HORUS_PUBLIC_BASE_URL>/reset-password#token=<token>; se muestra una vez (Cache-Control no-store).
             */
            reset_url: string;
            user_id: components["schemas"]["Uuid"];
        };
        Peer: components["schemas"]["TenantScopedResource"] & {
            /**
             * @description /32 de túnel (identidad del exportador)
             * @example 10.255.3.17/32
             */
            address: string;
            /** @description IP:puerto público visto del router. */
            endpoint?: string | null;
            enrollment: {
                enrolled_at?: components["schemas"]["Timestamp"] | null;
                token_expires_at?: components["schemas"]["Timestamp"] | null;
                token_id?: components["schemas"]["Uuid"] | null;
                /** @enum {string} */
                token_state: "none" | "pending" | "used" | "expired" | "revoked";
            };
            /** @enum {string} */
            handshake_state: "never" | "ok" | "stale";
            last_handshake_at: components["schemas"]["Timestamp"] | null;
            /** @example 25 */
            persistent_keepalive_seconds?: number;
            public_key: string | null;
            router_id: components["schemas"]["Uuid"];
            rx_bytes?: components["schemas"]["Uint64String"];
            server_id: components["schemas"]["Uuid"];
            /** @enum {string} */
            status: "awaiting_enrollment" | "pending_handshake" | "active" | "revoked";
            tx_bytes?: components["schemas"]["Uint64String"];
        };
        PermissionCatalog: {
            permissions: {
                audited?: boolean;
                description: string;
                /** @enum {string} */
                family: "tenant" | "platform";
                key: string;
                personal_data?: boolean;
            }[];
            roles: components["schemas"]["Role"][];
            /** @example v0 */
            version: string;
        };
        PlatformEvent: {
            /** @description Datos del evento sin secretos ni datos de clientes (p. ej. dependency, error, schema, changed_keys). */
            details?: {
                [key: string]: unknown;
            };
            /** Format: uuid */
            id: string;
            /** @description Hostname del contenedor (o HORUS_INSTANCE). */
            instance: string;
            /** @enum {string} */
            kind: "process_started" | "process_stopped" | "unclean_shutdown" | "role_started" | "role_stopped" | "role_failed" | "dependency_down" | "dependency_recovered" | "migration_applied" | "buffer_high" | "buffer_recovered" | "spool_active" | "spool_drained" | "alert_delivery_failed" | "config_changed" | "agent_restarted" | "container_restarted";
            message: string;
            occurred_at: components["schemas"]["Timestamp"];
            /** @example horus-app */
            process: string;
            role?: string;
            /** @enum {string} */
            severity: "info" | "warn" | "error";
            /** Format: uuid */
            tenant_id?: string | null;
            trace_id?: string;
            version?: string;
        };
        PlatformUser: {
            display_name: string;
            /** Format: email */
            email: string;
            id: components["schemas"]["Uuid"];
            memberships: {
                role_keys: string[];
                tenant_id: components["schemas"]["Uuid"];
                tenant_slug: string;
            }[];
            mfa_enabled?: boolean;
            platform_roles: string[];
            /** @enum {string} */
            status: "active" | "disabled" | "locked" | "pending";
        };
        PlatformUserPatch: {
            display_name?: string;
            /** Format: email */
            email?: string;
            /** @enum {string} */
            status?: "active" | "disabled";
        };
        /**
         * Playlist
         * @description Lista de reproducción de dashboards para kioscos (C9; frontend.md §7.5). Duración por defecto 30 s, mínimo 10 s.
         */
        "playlist.schema": {
            /** Format: date-time */
            created_at: string;
            /** Format: uuid */
            id: string;
            items: components["schemas"]["PlaylistItem"][];
            name: string;
            /** Format: uuid */
            tenant_id: string;
            /** @enum {string} */
            transition: "fade" | "none";
            /** Format: date-time */
            updated_at: string;
            version: number;
        };
        PlaylistInput: {
            items?: components["schemas"]["PlaylistItem"][];
            name?: string;
            /**
             * @default fade
             * @enum {string}
             */
            transition?: "fade" | "none";
        };
        PlaylistItem: {
            /** Format: uuid */
            dashboard_id: string;
            /** @default 30 */
            duration_seconds: number;
            variables?: {
                /** Format: uuid */
                site_id?: string | null;
            };
        };
        PollerHealth: {
            /**
             * @description false si HORUS_SNMP_ENABLED=false en la instalación.
             * @default true
             */
            enabled?: boolean;
            /** @description El sondeador late (último ciclo hace menos de 3 intervalos). */
            healthy: boolean;
            /** @description Intervalo del sondeo de sistema e interfaces (60 por defecto). */
            interval_seconds: number;
            last_cycle_at: components["schemas"]["Timestamp"] | null;
            /** @description Routers que este ISP tiene en sondeo (con túnel y credenciales SNMP). */
            targets: number;
        };
        Position: {
            h: number;
            w: number;
            x: number;
            y: number;
        };
        PrefixImportPreview: {
            items: {
                /** @description Solo IPv6. `prefix-length` leído de `/ipv6/pool` (tamaño que el router entrega a cada cliente); null en IPv4 o si no es un pool. */
                delegated_prefix_length?: number | null;
                /** @enum {string} */
                diff: "new" | "exists" | "overlaps";
                existing_client_prefix_id?: components["schemas"]["Uuid"] | null;
                /**
                 * @description Solo IPv6. Uso del pool según `/ppp/profile` y `/ipv6/dhcp-server`: `dhcpv6_pd` (dhcpv6-pd-pool o prefix-pool),
                 *     `ppp_link` (remote-ipv6-prefix-pool), `ppp_link_shared` (remote-ipv6-prefix-reuse=yes → se sugiere `infrastructure`),
                 *     `dhcpv6_address` (address-pool /128). Ver docs/vendors/mikrotik.md §11.4.
                 * @enum {string|null}
                 */
                ipv6_pool_usage?: "dhcpv6_pd" | "ppp_link" | "ppp_link_shared" | "dhcpv6_address" | "unused" | "unknown" | null;
                /** @enum {string} */
                origin: "ip_pool" | "ipv6_pool" | "interface_address";
                /** @example pool-pppoe */
                origin_name?: string;
                prefix: components["schemas"]["Cidr"];
                /** @enum {string} */
                suggested_assignment_mode?: "static" | "dynamic" | "unknown";
                /**
                 * @description Solo IPv6. Valor propuesto para `ipv6_client_len` (normalmente = delegated_prefix_length si es 48/56/60/64; null si no encaja y el operador debe decidir).
                 * @enum {integer|null}
                 */
                suggested_ipv6_client_len?: 48 | 56 | 60 | 64 | null;
                suggested_role: components["schemas"]["ClientPrefixRole"];
            }[];
            read_at: components["schemas"]["Timestamp"];
            router_id: components["schemas"]["Uuid"];
            routeros_version: string;
            tls_fingerprint_sha256?: string;
        };
        /** @description RFC 9457 `application/problem+json`. Lo producen los módulos y el gateway con el mismo formato. */
        Problem: {
            code: components["schemas"]["ErrorCode"];
            /** @description Solo en 412; representación actual del recurso. */
            current?: {
                [key: string]: unknown;
            };
            detail?: string;
            errors?: components["schemas"]["FieldError"][];
            instance?: string;
            request_id?: string;
            status: number;
            /** @description Texto humano en español; puede cambiar. */
            title: string;
            trace_id?: string;
            /**
             * Format: uri
             * @example https://docs.horus-flow.local/errors/validation-failed
             */
            type: string;
        };
        /** @description Razón explicable `{code, detail, weight}` (código estable traducible + texto humano). */
        Reason: {
            code: string;
            /** @description Datos estructurados de la razón (puerto, nº de destinos, feed, ventana…). */
            data?: {
                [key: string]: unknown;
            } | null;
            detail: string;
            weight?: number | null;
        };
        /** @description D11. Acción recomendada explicada, lista para que el operador la aplique A MANO. Horus nunca escribe en el router (execution = manual). */
        RecommendedAction: {
            /** @enum {string} */
            audience: "noc" | "network_engineer" | "customer_support";
            /** @description Enum abierto. */
            code: ("contact_customer" | "quarantine_address_list" | "block_destination" | "block_outbound_port" | "block_smtp_outbound" | "rate_limit_customer" | "inspect_device" | "change_default_credentials" | "update_firmware" | "add_to_allowlist" | "monitor") | string;
            /** @description Texto sugerido para avisar al cliente (contact_customer). */
            customer_message?: string | null;
            /**
             * @description Siempre manual en v1 (ADR-0022, ADR-0024 §4).
             * @constant
             */
            execution: "manual";
            /** @description Por qué se recomienda, qué efecto tiene en el cliente y cómo deshacerlo. */
            explanation: string;
            /** @description 1 = primero. */
            priority: number;
            /**
             * @description Impacto sobre el servicio del cliente si se aplica.
             * @enum {string}
             */
            risk: "low" | "medium" | "high";
            /** @description Comandos RouterOS sugeridos (null si la acción no es en el router). */
            routeros: null | {
                commands: components["schemas"]["RouterosTemplate"][];
                /** @description D15: solo RouterOS 7.x (≥ 7.12). */
                min_version: string;
                notes?: string | null;
                placeholders: ("customer_address" | "finding_id" | "remote_ip" | "remote_prefix" | "remote_port" | "protocol" | "address_list" | "rate_limit")[];
                /** @description Solo API y con customers.read: plantillas con los valores sustituidos. */
                rendered_commands: string[] | null;
                rendered_undo_commands: string[] | null;
                undo_commands: components["schemas"]["RouterosTemplate"][];
            };
            title: string;
        };
        /** @enum {string} */
        RelativeRange: "15m" | "1h" | "6h" | "24h" | "7d" | "30d" | "90d";
        RemoteDestination: {
            bytes_pending?: number | null;
            enabled: boolean;
            files_pending?: number | null;
            has_credentials: boolean;
            host: string;
            /** @description Huella SHA256:… confirmada de la clave del servidor. */
            host_key_fingerprint?: string | null;
            id: components["schemas"]["Uuid"];
            /** @enum {string} */
            kind: "sftp" | "gdrive" | "dropbox" | "mega";
            lag_seconds?: number | null;
            last_attempt_at?: components["schemas"]["Timestamp"] | null;
            last_error?: string | null;
            last_success_at?: components["schemas"]["Timestamp"] | null;
            name: string;
            /** @description Directorio remoto donde se dejan las copias cifradas. */
            path: string;
            port: number;
            /** @description Días que se conservan las copias en el destino. */
            retention_days?: number;
            /**
             * @description pending = aún sin primera copia.
             * @enum {string}
             */
            status: "ok" | "lagging" | "failing" | "disabled" | "pending";
            username: string;
            version: components["schemas"]["Version"];
        };
        RemoteDestinationCreate: {
            host: string;
            /** @enum {string} */
            kind: "sftp";
            name: string;
            path: string;
            /** @default 22 */
            port?: number;
            /** @default 30 */
            retention_days?: number;
            username: string;
        };
        RemoteDestinationPatch: {
            /** @description Solo se habilita con credenciales y huella confirmada. */
            enabled?: boolean;
            host?: string;
            name?: string;
            path?: string;
            port?: number;
            retention_days?: number;
            username?: string;
        };
        RemoteDestinationTestResult: {
            destination?: components["schemas"]["RemoteDestination"];
            error?: string | null;
            host_key_confirmed?: boolean;
            host_key_fingerprint?: string | null;
            ok: boolean;
            /**
             * @description Último paso alcanzado.
             * @enum {string}
             */
            stage: "connect" | "host_key" | "auth" | "write" | "done";
        };
        ReportCreate: {
            /** @description Por defecto PDF y CSV. */
            formats?: components["schemas"]["ReportFormat"][];
            /**
             * Format: date-time
             * @description Inicio (con `to`); omitidos = última semana ISO completa.
             */
            from?: string;
            kind?: components["schemas"]["ReportKind"];
            /** Format: date-time */
            to?: string;
        };
        ReportDefinition: {
            created_at: components["schemas"]["Timestamp"];
            /** @description La instalación tiene SMTP para el aviso a los destinatarios. */
            email_available: boolean;
            enabled: boolean;
            formats: components["schemas"]["ReportFormat"][];
            hour: number;
            id: components["schemas"]["Uuid"];
            kind: components["schemas"]["ReportKind"];
            minute: number;
            /**
             * Format: date-time
             * @description Próxima ejecución (null si está deshabilitada).
             */
            next_run_at: string | null;
            recipients: string[];
            tenant_id: components["schemas"]["Uuid"];
            /** @description Zona IANA (por defecto la del ISP). */
            timezone: string;
            updated_at: components["schemas"]["Timestamp"];
            version: components["schemas"]["Version"];
            /** @description Día ISO de la ejecución: 1 = lunes … 7 = domingo. */
            weekday: number;
        };
        ReportDefinitionPatch: {
            enabled?: boolean;
            formats?: components["schemas"]["ReportFormat"][];
            hour?: number;
            minute?: number;
            recipients?: string[];
            timezone?: string;
            weekday?: number;
        };
        ReportFile: {
            /** @description Ruta de descarga (`/api/v1/reports/{id}/download?format=pdf`), autenticada. */
            download_path: string;
            format: components["schemas"]["ReportFormat"];
            sha256: string;
            /** Format: int64 */
            size_bytes: number;
        };
        /** @enum {string} */
        ReportFormat: "pdf" | "csv";
        /**
         * @description Tipo de reporte (enum abierto; I3 trae el semanal de seguridad).
         * @enum {string}
         */
        ReportKind: "security_weekly";
        ReportRun: {
            /** Format: date-time */
            completed_at: string | null;
            created_at: components["schemas"]["Timestamp"];
            /**
             * Format: uuid
             * @description Usuario que lo pidió; null = programado.
             */
            created_by: string | null;
            /**
             * Format: uuid
             * @description Definición que la programó; null = a petición.
             */
            definition_id: string | null;
            /** @description Motivo legible del fallo (sin detalles internos). */
            error: string | null;
            /** @description Los archivos ya se borraron por la retención. */
            expired: boolean;
            /**
             * Format: date-time
             * @description Cuándo se borran los archivos (retención, 365 días por defecto).
             */
            expires_at: string | null;
            files: components["schemas"]["ReportFile"][];
            formats: components["schemas"]["ReportFormat"][];
            id: components["schemas"]["Uuid"];
            /** @description Semana ISO del inicio del periodo. */
            iso_week: string;
            kind: components["schemas"]["ReportKind"];
            period_from: components["schemas"]["Timestamp"];
            /** @description Fin excluyente del periodo `[period_from, period_to)`. */
            period_to: components["schemas"]["Timestamp"];
            scheduled: boolean;
            /** Format: int64 */
            size_bytes: number;
            /** Format: date-time */
            started_at: string | null;
            status: components["schemas"]["ReportStatus"];
            tenant_id: components["schemas"]["Uuid"];
            /** @description Zona IANA con la que se calculó y se muestra el periodo. */
            timezone: string;
        };
        /** @enum {string} */
        ReportStatus: "queued" | "running" | "completed" | "failed";
        /**
         * @description Igual que `reputation_category` de ClickHouse (C3).
         * @enum {string}
         */
        ReputationCategory: "botnet_cc" | "scanner" | "malware_dist" | "mining_pool" | "proxy_vpn" | "tor_exit" | "blocklist";
        ReputationSource: {
            category: components["schemas"]["ReputationCategory"];
            /**
             * @description `approved` (catálogo base, D20) u `operator_acknowledged` (personalizada: términos confirmados por quien la dio de alta).
             * @enum {string}
             */
            commercial_use: "approved" | "operator_acknowledged";
            confidence: number;
            consecutive_failures: number;
            created_at: components["schemas"]["Timestamp"];
            /** @description user id; null en el catálogo base. */
            created_by?: string | null;
            enabled: boolean;
            entries: number;
            format: components["schemas"]["ReputationSourceFormat"];
            frequency: components["schemas"]["ReputationSourceFrequency"];
            has_auth: boolean;
            id: components["schemas"]["Uuid"];
            /** @example abuse_ch_feodo */
            key: string;
            last_attempt_at: components["schemas"]["Timestamp"] | null;
            /** @description Sin secretos. */
            last_error: string | null;
            last_success_at: components["schemas"]["Timestamp"] | null;
            license?: string | null;
            name: string;
            notes?: string | null;
            /** @description = reputation_source_id en ClickHouse (C3); asignado por detection. */
            numeric_id: number;
            origin: components["schemas"]["ReputationSourceOrigin"];
            /** @enum {string} */
            status: "pending" | "ok" | "failing" | "disabled";
            updated_at: components["schemas"]["Timestamp"];
            /** Format: uri */
            url: string;
            version: number;
        };
        ReputationSourceCreate: {
            /** @example Auth-Key */
            auth_header_name?: string | null;
            auth_header_value?: string | null;
            category: components["schemas"]["ReputationCategory"];
            /** @description Confianza de sus indicadores (= reputation_confidence). */
            confidence: number;
            /** @default true */
            enabled?: boolean;
            format: components["schemas"]["ReputationSourceFormat"];
            frequency: components["schemas"]["ReputationSourceFrequency"];
            /** @description Identificador estable e inmutable (aparece en hallazgos y snapshots). */
            key: string;
            license?: string | null;
            /** @default 67108864 */
            max_bytes?: number;
            /**
             * @description Menos entradas → carga rechazada (se conserva la anterior).
             * @default 1
             */
            min_entries?: number;
            name: string;
            notes?: string | null;
            /** @constant */
            terms_acknowledged: true;
            /** Format: uri */
            url: string;
        };
        /**
         * @description Formatos que sabe leer `detection` (enum abierto; uno nuevo exige parser y versión del contrato).
         *     `netset`: una IP o CIDR por línea, `#` comentarios (también listas de salida de Tor).
         *     `abusech-feodo-csv`, `abusech-threatfox-csv`: exportaciones CSV de abuse.ch. `spamhaus-drop-json`: DROP en JSON Lines.
         * @enum {string}
         */
        ReputationSourceFormat: "netset" | "abusech-feodo-csv" | "abusech-threatfox-csv" | "spamhaus-drop-json";
        /**
         * @description Intervalo entre cargas (mínimo 15 min, máximo 7 d).
         * @example 1h
         * @example 6h
         * @example 24h
         */
        ReputationSourceFrequency: string;
        /**
         * @description `catalog`: catálogo base de la instalación (D20: uso comercial aprobado). `custom`: agregada por el superadministrador.
         * @enum {string}
         */
        ReputationSourceOrigin: "catalog" | "custom";
        ReputationSourcePatch: {
            auth_header_name?: string | null;
            auth_header_value?: string | null;
            category?: components["schemas"]["ReputationCategory"];
            confidence?: number;
            enabled?: boolean;
            format?: components["schemas"]["ReputationSourceFormat"];
            frequency?: components["schemas"]["ReputationSourceFrequency"];
            license?: string | null;
            max_bytes?: number;
            min_entries?: number;
            name?: string;
            notes?: string | null;
            /** Format: uri */
            url?: string;
        };
        Role: {
            /** @description Asignaciones activas de este rol en el ISP. */
            assignments_count?: number;
            description?: string | null;
            id: components["schemas"]["Uuid"];
            is_system: boolean;
            key: string;
            name: string;
            permissions: string[];
            requires_mfa?: boolean;
            tenant_id?: components["schemas"]["Uuid"] | null;
            /** @description Solo en roles propios (If-Match de PATCH/DELETE); los de sistema no lo traen. */
            version?: components["schemas"]["Version"];
        };
        RoleAssignment: {
            role_id: components["schemas"]["Uuid"];
            /** @example noc */
            role_key?: string;
            scope: string;
        };
        RoleCreate: {
            description?: string | null;
            /** @description Único en el ISP; no puede coincidir con un rol de sistema. */
            key: string;
            name: string;
            permissions: string[];
        };
        RolePatch: {
            description?: string | null;
            name?: string;
            permissions?: string[];
        };
        Router: components["schemas"]["TenantScopedResource"] & components["schemas"]["RouterInput"] & {
            /** @enum {string} */
            admin_state: "active" | "maintenance" | "decommissioned";
            credentials?: components["schemas"]["CredentialStatus"][];
            /**
             * @description Pasos del asistente (I1-19): `pending_configuration` → `key_received` (clave pública enrolada)
             *     → `tunnel_up` (primer handshake) → `exporting` (primer flujo). Enum abierto.
             * @enum {string}
             */
            onboarding_state: "pending_configuration" | "key_received" | "tunnel_up" | "exporting";
            routeros_version_detected?: string | null;
            /** @description false si la versión (detectada o declarada) es < 7.12 (D15); null si se desconoce. */
            routeros_version_supported: boolean | null;
            /** @description IP /32 de túnel WireGuard asignada por la IPAM de plataforma = identidad del exportador. */
            tunnel_address: string | null;
            warnings: ("routeros_version_unsupported" | "traffic_flow_target_drift" | "hardware_offload_suspected")[];
            wireguard_peer_id?: components["schemas"]["Uuid"] | null;
        };
        /** @description Última muestra del dispositivo. Cada campo es null si el sondeo no lo obtuvo (modelo sin sensor, OID ausente). */
        RouterDeviceSample: {
            cpu_cores?: number | null;
            /** @description Núcleo más cargado (Traffic Flow carga uno). */
            cpu_max_core_percent?: number | null;
            /** @description Media de los núcleos. */
            cpu_percent?: number | null;
            cpu_temperature_celsius?: number | null;
            /** @example 7.16.2 */
            firmware?: string | null;
            icmp_loss_ratio?: number | null;
            icmp_rtt_ms?: number | null;
            memory_percent?: number | null;
            memory_total_bytes?: components["schemas"]["Uint64String"] | null;
            memory_used_bytes?: components["schemas"]["Uint64String"] | null;
            /** @example CCR2116-12G-4S+ */
            model?: string | null;
            poll_duration_ms?: number | null;
            sampled_at: components["schemas"]["Timestamp"] | null;
            /** @description Sensores propios del fabricante (mtxrGaugeTable) tal cual los expone. */
            sensors?: {
                name: string;
                unit?: string | null;
                value: number;
            }[];
            serial_number?: string | null;
            /** @description Placa (mtxrHlTemperature). */
            temperature_celsius?: number | null;
            uptime_seconds?: number | null;
            voltage?: number | null;
        };
        /**
         * @description Estado efectivo (api.md §2.6). `stale` = la observación es vieja o el sondeador no late; `unknown` = nunca observado.
         * @enum {string}
         */
        RouterEffectiveStatus: "online" | "warning" | "critical" | "degraded" | "offline" | "stale" | "unknown" | "maintenance";
        RouterInput: {
            display_name?: string | null;
            /** @default true */
            is_primary?: boolean;
            /** @example CCR2116-12G-4S+ */
            model?: string | null;
            /** @description Hostname / identidad del router. */
            name: string;
            /** @description Declarada por el admin (D15: solo RouterOS 7.x). Se sobrescribe con la detectada. */
            routeros_version?: string | null;
            site_id: components["schemas"]["Uuid"];
            tags?: string[];
            /**
             * @default mikrotik
             * @enum {string}
             */
            vendor?: "mikrotik";
        };
        RouterInterface: {
            admin_up: boolean | null;
            /** @example UPLINK-TRANSIT-A */
            alias: string | null;
            id: components["schemas"]["Uuid"];
            if_index: number;
            last_seen_at: components["schemas"]["Timestamp"] | null;
            /** @description Se sondean sus contadores cada intervalo. */
            monitored: boolean;
            /** @example sfp-sfpplus1 */
            name: string;
            oper_status: components["schemas"]["InterfaceOperStatus"] | null;
            speed_bps: number | null;
            /** @description ifType como nombre IANA: ethernetCsmacd, l2vlan, bridge, ieee8023adLag, ppp, tunnel, other. */
            type: string;
            uplink: boolean;
        };
        RouterLiveMetrics: {
            interfaces: components["schemas"]["InterfaceSample"][];
            interval_seconds: number;
            status: components["schemas"]["RouterStatus"];
        };
        /** @enum {string} */
        RouterObservedState: "online" | "warning" | "critical" | "degraded" | "offline";
        /** @description Línea de comando RouterOS con placeholders {{nombre}}; todo lo que crea lleva comment="horus finding {{finding_id}}" para poder deshacerlo. */
        RouterosTemplate: string;
        RouterPatch: {
            /** @enum {string} */
            admin_state?: "active" | "maintenance" | "decommissioned";
            display_name?: string | null;
            model?: string | null;
            name?: string;
            routeros_version?: string | null;
            tags?: string[];
        };
        /** @description Las señales que sostienen el estado; `unknown` mientras no haya observación. */
        RouterSignals: {
            /** @description Estado del exportador de flujos (flows.yaml FlowExporterState) o null si no aplica. */
            exporter: string | null;
            /** @enum {string} */
            icmp: "ok" | "fail" | "unknown";
            /** @enum {string} */
            snmp: "ok" | "fail" | "auth_failed" | "unknown";
            /** @enum {string} */
            wireguard: "ok" | "stale" | "never" | "unknown";
        };
        /**
         * @description Enum abierto (events.md §8.4).
         * @example ok
         * @example cpu_high
         * @example memory_high
         * @example temperature_high
         * @example snmp_unreachable
         * @example snmp_auth_failed
         * @example tunnel_down
         * @example host_unreachable_via_tunnel
         * @example exporter_silent
         * @example interface_down
         * @example psu_failed
         * @example fan_failed
         */
        RouterStateReason: string;
        RouterStatus: {
            device: components["schemas"]["RouterDeviceSample"];
            interfaces_total: number | null;
            interfaces_up: number | null;
            last_observed_at: components["schemas"]["Timestamp"] | null;
            name: string;
            observed_state: components["schemas"]["RouterObservedState"] | null;
            reason: components["schemas"]["RouterStateReason"] | null;
            router_id: components["schemas"]["Uuid"];
            signals: components["schemas"]["RouterSignals"];
            /** @description Desde cuándo está en el estado efectivo actual. */
            since: components["schemas"]["Timestamp"] | null;
            site_id: components["schemas"]["Uuid"];
            site_name: string;
            status: components["schemas"]["RouterEffectiveStatus"];
            thresholds: components["schemas"]["RouterThresholdHit"][];
            uplink: components["schemas"]["RouterUplink"] | null;
        };
        RouterThresholdHit: {
            interface_name?: string | null;
            /** @enum {string} */
            level: "warning" | "critical";
            /** @enum {string} */
            metric: "cpu_percent" | "cpu_max_core_percent" | "memory_percent" | "temperature_celsius" | "cpu_temperature_celsius" | "interface_errors" | "interface_down";
            threshold: number;
            value: number;
        };
        RouterThresholds: components["schemas"]["RouterThresholdValues"] & {
            /** @description false = rigen los de la instalación. */
            customized: boolean;
            defaults: components["schemas"]["RouterThresholdValues"];
            /** @description Ciclos seguidos sobre el umbral para CPU y memoria. */
            sustained_cycles: number;
            updated_at: components["schemas"]["Timestamp"] | null;
            version: number;
        };
        RouterThresholdValues: {
            cpu_critical: number;
            /** @description % sostenido. */
            cpu_warning: number;
            memory_critical: number;
            /** @description % sostenido. */
            memory_warning: number;
            temperature_critical: number;
            /** @description °C. */
            temperature_warning: number;
        };
        /** @description Interfaz que resume el tráfico del router (la de más subida hacia fuera; `flow_role` upstream si está marcado). */
        RouterUplink: {
            interface_id: components["schemas"]["Uuid"];
            name: string;
            rx_bps?: number | null;
            speed_bps?: number | null;
            tx_bps?: number | null;
            /** @description max(rx, tx) / velocidad. */
            utilization?: number | null;
        };
        ScoringFeature: {
            /** @description false si no hay datos (p. ej. sin mediana residencial). */
            available: boolean;
            /** @description inbound_services | upload_ratio_high | upload_vs_residential_median | business_hours_pattern | always_on | many_remote_asns (enum abierto). */
            code: string;
            /** @description Puntos (0..1) que aporta a la probabilidad comercial. */
            contribution: number;
            /** @description Texto humano con los números reales. */
            detail: string;
            normalized: number;
            /** @description ports | ratio | x | share | asns */
            unit: string;
            /** @description Valor bruto en `unit`. */
            value: number;
            /** @description Peso configurado por el ISP. */
            weight: number;
        };
        /**
         * @description Proyectado desde detection. Etiquetas de presentación (D18; `dashboard/v0/widget-data.schema.json#/$defs/SecurityState`):
         *     `clean` "Limpio", `suspected` "Sospechoso", `infected` **"Infectado"**, `mitigated` "Mitigado". "Infectado" se
         *     muestra siempre con las razones y el nivel de confianza de los hallazgos que lo sostienen.
         * @enum {string}
         */
        SecurityState: "clean" | "suspected" | "infected" | "mitigated";
        SecuritySummary: {
            affected_customers: number;
            by_kind: {
                [key: string]: number;
            };
            by_security_state: {
                [key: string]: number;
            };
            /** @description Clientes afectados por señal (c2_contact, beaconing, fan_out, scanning, watched_ports, sustained_upload, smtp, ddos). */
            by_signal: {
                [key: string]: number;
            };
            by_site: {
                customers_with_signals: number;
                open_findings: number;
                site_id: components["schemas"]["Uuid"];
            }[];
            generated_at: components["schemas"]["Timestamp"];
            new_last_24h: number;
            open_by_severity: {
                [key: string]: number;
            };
            watched_ports?: {
                customers: number;
                flows: components["schemas"]["Uint64String"];
                port: number;
                /** @enum {string} */
                protocol: "tcp" | "udp";
            }[];
        };
        SeriesData: {
            /** @constant */
            kind: "series";
            series: {
                group?: string | null;
                metric: string;
                points: [
                    string,
                    number | null
                ][];
                unit: string;
            }[];
        };
        SeriesResponse: {
            data: {
                series: {
                    group?: string | null;
                    /** @example down_bps */
                    metric: string;
                    /** @description [timestamp, valor|null]; huecos como null. */
                    points: [
                        string,
                        number | null
                    ][];
                    /** @example bps */
                    unit: string;
                }[];
            };
            meta: components["schemas"]["AnalyticsMeta"];
        };
        /**
         * @description Severidad (enum abierto; el cliente trata valores desconocidos como "otro").
         * @enum {string}
         */
        Severity: "info" | "low" | "medium" | "high" | "critical";
        Site: components["schemas"]["TenantScopedResource"] & components["schemas"]["SiteInput"] & {
            /** @description true si el nodo no tiene prefijos de clientes (el tráfico cuenta para el nodo, no crea clientes). */
            discovery_mode: boolean;
            primary_router_id: components["schemas"]["Uuid"] | null;
            /** @description Realm `node_private` del nodo (RFC1918 y 100.64.0.0/10). */
            private_realm_id: components["schemas"]["Uuid"];
        };
        SiteInput: {
            address?: string | null;
            code?: string | null;
            /**
             * @default node
             * @enum {string}
             */
            kind?: "node" | "region" | "datacenter" | "other";
            latitude?: number | null;
            longitude?: number | null;
            name: string;
            parent_id?: components["schemas"]["Uuid"] | null;
            tags?: string[];
            timezone?: string | null;
        };
        Size: {
            h: number;
            w: number;
        };
        StateData: {
            /** @constant */
            kind: "state";
            /** @description Clave → valor (número, string, booleano, null o mapa de conteos). */
            values: {
                [key: string]: number | string | boolean | null | Record<string, unknown>;
            };
        };
        SystemStatus: {
            capabilities: {
                analytics: components["schemas"]["CapabilityState"];
                auth: components["schemas"]["CapabilityState"];
                classification?: components["schemas"]["CapabilityState"];
                detection: components["schemas"]["CapabilityState"];
                ingest: components["schemas"]["CapabilityState"];
                inventory: components["schemas"]["CapabilityState"];
                monitoring?: components["schemas"]["CapabilityState"];
                notifications: components["schemas"]["CapabilityState"];
                realtime: components["schemas"]["CapabilityState"];
                reports?: components["schemas"]["CapabilityState"];
                wireguard: components["schemas"]["CapabilityState"];
            } & {
                [key: string]: components["schemas"]["CapabilityState"];
            };
            checked_at: components["schemas"]["Timestamp"];
            /** @description Vacío para usuarios de tenant y kioscos; detalle solo con `platform.status.read`. */
            components: {
                detail?: string | null;
                /**
                 * @example postgres
                 * @example valkey
                 * @example nats
                 * @example clickhouse
                 * @example local_storage
                 * @example remote_storage
                 * @example collector
                 * @example wg_agent
                 */
                name: string;
                since?: components["schemas"]["Timestamp"] | null;
                /** @enum {string} */
                status: "up" | "degraded" | "down" | "not_configured";
            }[];
            /** @description Solo plataforma; aviso si > 0.85 (I1-23). */
            disk_usage_ratio?: number | null;
            /** @enum {string} */
            status: "ok" | "degraded" | "down";
        };
        TableData: {
            columns: {
                key: string;
                /** @default false */
                personal_data?: boolean;
                /** @enum {string} */
                type: "string" | "number" | "bytes" | "bps" | "percent" | "timestamp" | "ip" | "severity" | "state";
            }[];
            /** @constant */
            kind: "table";
            /** @description Fila 'Otros' de los top N. */
            others?: Record<string, unknown> | null;
            rows: Record<string, unknown>[];
        };
        TelegramBotInfo: {
            can_join_groups?: boolean;
            can_read_all_group_messages?: boolean;
            /** @example NOC Fibra Norte */
            first_name: string;
            /** Format: int64 */
            id: number;
            /** @example noc_isp_bot */
            username: string;
        };
        /** TelegramChannelConfig */
        TelegramChannelConfig: {
            /** @description Id numérico (usuarios positivos, grupos negativos, supergrupos y canales `-100…`) o `@usuario_del_canal`. */
            chat_id: string;
            /**
             * @default es
             * @enum {string}
             */
            language?: "es" | "en";
            /** @description Tema de un supergrupo con temas (foro); sin valor, el chat general. */
            message_thread_id?: number;
            /** @default false */
            readonly uses_own_bot?: boolean;
        };
        TelegramChatCandidate: {
            /**
             * @description chat_id para la configuración del canal.
             * @example -1001234567890
             */
            id: string;
            title: string;
            /** @enum {string} */
            type: "private" | "group" | "supergroup" | "channel";
            username?: string;
        };
        TelegramLinkCode: {
            /** @example @horus_alertas_bot */
            bot_username: string | null;
            code: string;
            expires_at: components["schemas"]["Timestamp"];
            /** @description Enlace de t.me al chat privado con el bot y el código como parámetro start. */
            start_url: string | null;
        };
        Tenant: {
            country: string;
            counts?: {
                members?: number;
                routers?: number;
                sites?: number;
            };
            created_at: components["schemas"]["Timestamp"];
            id: components["schemas"]["Uuid"];
            name: string;
            quotas: components["schemas"]["TenantQuotas"];
            settings: components["schemas"]["TenantSettings"];
            slug: string;
            /** @enum {string} */
            status: "active" | "suspended" | "offboarding";
            /** @enum {string} */
            support_access_policy?: "notify" | "require_approval" | "deny";
            timezone: string;
            updated_at: components["schemas"]["Timestamp"];
            version: components["schemas"]["Version"];
        };
        TenantCreate: {
            /** @description ISO-3166 (ley aplicable). */
            country: string;
            /** Format: email */
            initial_admin_email: string;
            name: string;
            quotas?: components["schemas"]["TenantQuotas"];
            settings?: components["schemas"]["TenantSettings"];
            slug: string;
            /** @example America/Mexico_City */
            timezone: string;
        };
        TenantOverview: {
            exporters_exporting: number;
            exporters_silent: number;
            flow_coverage_ratio?: number | null;
            flows_dropped_quota_last_hour?: components["schemas"]["Uint64String"];
            ingest_flows_per_second: number;
            name: string;
            /** @description Solo conteo; nunca clientes. */
            open_findings_critical: number;
            routers_total: number;
            slug: string;
            status: string;
            tenant_id: components["schemas"]["Uuid"];
            tunnels_down?: number;
        };
        TenantPatch: {
            country?: string;
            name?: string;
            quotas?: components["schemas"]["TenantQuotas"];
            settings?: components["schemas"]["TenantSettings"];
            /** @enum {string} */
            support_access_policy?: "notify" | "require_approval" | "deny";
            timezone?: string;
        };
        TenantQuotas: {
            max_customers?: number;
            max_flows_per_second?: number;
            max_routers?: number;
        };
        TenantScopedResource: {
            created_at: components["schemas"]["Timestamp"];
            id: components["schemas"]["Uuid"];
            /** @description Redundante con `tid`; permite al cliente detectar un token equivocado. */
            tenant_id: components["schemas"]["Uuid"];
            updated_at: components["schemas"]["Timestamp"];
            version: components["schemas"]["Version"];
        };
        /** @description Ajustes del ISP (también editables por el ISP con `settings.manage`). */
        TenantSettings: {
            /**
             * @default residential
             * @enum {string}
             */
            customer_default_kind?: "residential" | "commercial" | "unknown";
            /** @default 30 */
            customer_inactivity_days?: number;
            /** @default 80 */
            kind_auto_apply_min_confidence?: number;
            /** @description Retenciones más cortas que las de plataforma (días), por dato. */
            retention_overrides?: {
                [key: string]: number;
            };
        };
        /**
         * Format: date-time
         * @description UTC, RFC 3339 con `Z` y milisegundos.
         * @example 2026-10-07T14:03:11.123Z
         */
        Timestamp: string;
        /**
         * @description `acme`: certificado automático (Let's Encrypt) para el nombre; obligatorio por defecto en `domain`/`subdomain`.
         *     `acme_ip`: certificado para IP si el emisor ACME lo soporta (solo `ip_only`, opcional).
         *     `self_signed`: certificado autogenerado en la instalación con SAN = IP (por defecto en `ip_only`).
         *     `provided`: certificado aportado por el operador (CA interna del ISP), en cualquier modo.
         *     `external`: TLS terminado por un proxy inverso de la persona (Nginx Proxy Manager, nginx, Caddy…) delante
         *     de Horus; Traefik sirve HTTP solo a `--trusted-proxies` y Horus no emite HSTS (cuarto modo de D19).
         * @enum {string}
         */
        TlsMode: "acme" | "acme_ip" | "self_signed" | "provided" | "external";
        TopResult: {
            dimension: string;
            others: {
                down_bytes: components["schemas"]["Uint64String"];
                up_bytes: components["schemas"]["Uint64String"];
            };
            rows: components["schemas"]["TopRow"][];
            totals: {
                down_bytes: components["schemas"]["Uint64String"];
                up_bytes: components["schemas"]["Uint64String"];
            };
        };
        TopRow: {
            /** @description Solo con dimension=customers. */
            customer?: {
                /** @description IP completa o enmascarada (`10.20.0.•••`) según permisos/kiosco. */
                address?: string;
                address_masked?: boolean;
                alias?: string | null;
                customer_id?: components["schemas"]["Uuid"];
                kind?: string;
            } | null;
            down_bytes: components["schemas"]["Uint64String"];
            /** @description ID de servicio/categoría/organización, ASN o customer_id. */
            key: string;
            label: string;
            share: number;
            up_bytes: components["schemas"]["Uint64String"];
        };
        /** @description Contador que puede superar 2^53, serializado como string decimal. */
        Uint64String: string;
        UpdateStatus: {
            /** @enum {string} */
            channel: "stable" | "beta";
            checked_at: components["schemas"]["Timestamp"] | null;
            /**
             * @description Versión instalada (HORUS_VERSION, semver).
             * @example 1.2.3
             */
            current_version: string;
            /** @description Motivo de `unchecked` (texto para la consola; sin secretos). */
            error?: string | null;
            latest?: null | {
                /** @description Notas del cambio (Markdown de la release), recortadas a 8 KiB. */
                notes?: string | null;
                /** Format: uri */
                notes_url: string;
                /** @description true si solo cambia Z (X.Y iguales): la actualización automática, si está activada, la aplicará en la ventana. */
                patch_only: boolean;
                published_at?: components["schemas"]["Timestamp"] | null;
                /** @example sudo horus-ctl upgrade --to 1.2.4 */
                upgrade_command: string;
                /** @example 1.2.4 */
                version: string;
            };
            /**
             * @description `up_to_date`: no hay versión mayor en el canal. `available`: hay una (ver `latest`). `unchecked`: no se
             *     pudo consultar la fuente (sin Internet, fuente caída) o la comprobación está desactivada.
             * @enum {string}
             */
            status: "up_to_date" | "available" | "unchecked";
            update_available: boolean;
        };
        /**
         * Format: uuid
         * @description UUIDv7 canónico.
         * @example 0192f0c4-7a1e-7c3a-9b1d-2f6e8a4c1d55
         */
        Uuid: string;
        /** @description Variables comunes de la toolbar que heredan los widgets que las aceptan. */
        Variables: {
            range?: components["schemas"]["RelativeRange"];
            site_id?: components["schemas"]["$defs-Uuid"] | null;
        };
        /** @description Versión para concurrencia optimista (= ETag, = `aggregate_version` de los eventos). */
        Version: number;
        /** @enum {string} */
        Visibility: "system" | "tenant" | "private" | "platform";
        /**
         * WebhookChannelConfig
         * @description Webhook firmado (ADR-0030, p. ej. el receptor de ArgosISP). Cada entrega es un `POST` con
         *     `{id, event, occurred_at, tenant, data}` y las cabeceras `X-Webhook-Id` (= `id`, estable entre reintentos),
         *     `X-Webhook-Timestamp` y `X-Webhook-Signature: v1=hex(HMAC-SHA256(secreto, timestamp + "." + cuerpo))`. El
         *     secreto lo genera el receptor y va por `PUT …/credentials` (`webhook_secret`, write-only). `event` sigue el
         *     contrato §3.4: `finding.opened` (finding_opened, finding_reopened), `finding.resolved`,
         *     `customer.security_state_changed`, `router.status_changed` (router_*: offline, recovered, umbrales, hardware) e
         *     `infra_service.state_changed` (service_*); los demás tipos no se envían por webhook. Con identidad ArgosISP del
         *     cliente, `data.external` lleva sus ids. La IP y el alias del cliente solo con `include_personal_data`.
         *     Reintentos: 1 min, 5 min, 30 min, 2 h y 12 h. Solo https y destinos públicos (privados o loopback solo con
         *     HORUS_ALERTS_WEBHOOK_ALLOW_PRIVATE=true, laboratorios); cada redirección se valida igual.
         */
        WebhookChannelConfig: {
            /** @default 10 */
            timeout_seconds?: number;
            /**
             * Format: uri
             * @description URL del receptor (sin credenciales en la URL).
             * @example https://argos.isp.example/api/v1/integraciones/horus/webhook
             */
            url: string;
        };
        Widget: {
            config: Record<string, unknown>;
            id: string;
            position: components["schemas"]["Position"];
            refresh_seconds?: number | null;
            title: string | null;
            /** @description Debe existir en GET /widget-types; config valida contra su config_schema. */
            type: string;
        };
        /**
         * WidgetData
         * @description Respuesta de GET /dashboards/{id}/widgets/{wid}/data y POST /widget-data/preview (C9). La forma de `data` depende de data_endpoint_kind del tipo: state (valores actuales), series (series temporales con huecos null) o table (filas). Huecos como ausencia, nunca ceros.
         */
        "widget-data.schema": {
            data: components["schemas"]["StateData"] | components["schemas"]["SeriesData"] | components["schemas"]["TableData"];
            meta: components["schemas"]["Meta"];
        };
        /**
         * WidgetType
         * @description Entrada del catálogo autoritativo de tipos de widget (C9, mitad servidor; api.md §2.11, frontend.md §6.2–§6.3). La sirve GET /widget-types desde widget-types.json.
         */
        "widget-type.schema": {
            additional_permissions?: string[];
            /** @enum {string} */
            category: "traffic" | "customers" | "security" | "infrastructure" | "platform" | "layout";
            /** @description JSON Schema (2020-12) de la config del widget; el servidor valida cada config guardada. */
            config_schema: Record<string, unknown>;
            /** @description true ⇒ IPs de clientes solo con customers.read; en kiosco enmascaradas salvo show_personal_data. */
            contains_personal_data: boolean;
            /** @enum {string} */
            data_endpoint_kind: "state" | "series" | "table" | "none";
            /** @description Empieza por verbo ('Muestra…'). */
            description: string;
            /** @enum {string} */
            increment: "I1" | "I2" | "I3" | "I4";
            kiosk_allowed: boolean;
            /** @description Topic WebSocket (C6) que actualiza el widget, o null si solo REST. */
            realtime_topic: string | null;
            /** @description Permiso que necesita quien mira (usuario). Los kioscos se rigen por kiosk_allowed + dashboards asignados. */
            required_permission: string;
            sizes: {
                allowed: components["schemas"]["Size"][];
                default: components["schemas"]["Size"];
            };
            title: string;
            type: string;
            /** @default 1 */
            version?: number;
        };
        WidgetPatch: {
            config?: Record<string, unknown>;
            position?: components["schemas"]["Position"];
            refresh_seconds?: number | null;
            title?: string | null;
        };
        WireguardHub: {
            /** @example 65534 */
            addresses_total: number;
            /** @example 37 */
            addresses_used: number;
            /**
             * @description Host público (HORUS_WG_ENDPOINT, D14/D16).
             * @example horus.example.net
             */
            endpoint: string;
            id: components["schemas"]["Uuid"];
            /** @example 51820 */
            listen_port: number;
            /** @example 1420 */
            mtu?: number;
            name: string;
            peers_active?: number;
            /** @example 25 */
            persistent_keepalive_seconds?: number;
            /** @description Todos los rangos del hub (tunnel_cidr es el primero, por compatibilidad). */
            pools?: components["schemas"]["WireguardPool"][];
            public_key: string;
            services_cidr?: components["schemas"]["Cidr"];
            /** @enum {string} */
            status: "up" | "down" | "degraded";
            tenants?: number;
            tunnel_cidr: components["schemas"]["Cidr"];
            version?: components["schemas"]["Version"];
        };
        WireguardPool: {
            addresses_total: number;
            addresses_used: number;
            cidr: components["schemas"]["Cidr"];
            created_at?: components["schemas"]["Timestamp"];
            id: components["schemas"]["Uuid"];
        };
    };
    responses: {
        /** @description El proveedor de IA falló o no llegó a una respuesta (`ASSIST_PROVIDER_UNAVAILABLE`, `ASSIST_INCOMPLETE`). */
        BadGateway: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["Problem"];
            };
        };
        /** @description JSON mal formado, parámetro inválido, cursor corrupto, filtro u orden no permitido. */
        BadRequest: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["Problem"];
            };
        };
        /** @description Conflicto de estado o unicidad; `Idempotency-Key` en vuelo. */
        Conflict: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["Problem"];
            };
        };
        /** @description Sin permiso, ámbito de token inválido, `Origin` no permitido, 2FA/re-auth requerida, kiosco fuera de su lista blanca. */
        Forbidden: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["Problem"];
            };
        };
        /** @description Timeout (`TIMEOUT`). */
        GatewayTimeout: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["Problem"];
            };
        };
        /** @description Sin contenido. */
        NoContent: {
            headers: {
                [name: string]: unknown;
            };
            content?: never;
        };
        /** @description No existe o el llamante no puede saber que existe (otro tenant → siempre 404). */
        NotFound: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["Problem"];
            };
        };
        /** @description `If-Match` no coincide; incluye `current`. */
        PreconditionFailed: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["Problem"];
            };
        };
        /** @description Falta `If-Match` o `Idempotency-Key` obligatorio. */
        PreconditionRequired: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["Problem"];
            };
        };
        /** @description Módulo caído o degradado (`SERVICE_UNAVAILABLE`, `ANALYTICS_UNAVAILABLE`). */
        ServiceUnavailable: {
            headers: {
                "Retry-After": components["headers"]["RetryAfter"];
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["Problem"];
            };
        };
        /** @description Rate limit por sesión, IP o tenant (`RATE_LIMITED`, `TENANT_RATE_LIMITED`). */
        TooManyRequests: {
            headers: {
                RateLimit: components["headers"]["RateLimit"];
                "RateLimit-Policy": components["headers"]["RateLimitPolicy"];
                "Retry-After": components["headers"]["RetryAfter"];
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["Problem"];
            };
        };
        /** @description Sin token, token expirado (`TOKEN_EXPIRED`) o sesión revocada. */
        Unauthorized: {
            headers: {
                "WWW-Authenticate"?: string;
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["Problem"];
            };
        };
        /** @description Validación semántica (`errors[]`). */
        UnprocessableEntity: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/problem+json": components["schemas"]["Problem"];
            };
        };
    };
    parameters: {
        AsnRuleId: components["schemas"]["Uuid"];
        ChannelId: components["schemas"]["Uuid"];
        ClientPrefixId: components["schemas"]["Uuid"];
        /** @description Cursor opaco (`page.next_cursor`); ligado a filtros, orden y `tid`. Ajeno → `400 INVALID_CURSOR`. */
        Cursor: string;
        CustomerId: components["schemas"]["Uuid"];
        DashboardId: components["schemas"]["Uuid"];
        DestinationId: components["schemas"]["Uuid"];
        FindingId: components["schemas"]["Uuid"];
        /** @description Inicio RFC 3339 UTC, intervalo semiabierto `[from, to)`. Excluyente con `range`. */
        From: string;
        /** @description Recomendado en `POST`. */
        IdempotencyKeyOptional: string;
        /** @description UUID generado por el cliente; obligatorio en acciones con efecto externo (falta → 428). */
        IdempotencyKeyRequired: string;
        /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
        IfMatch: string;
        /** @description Solo en colecciones administrativas pequeñas; ignorado en las grandes. */
        IncludeTotal: boolean;
        InfraServiceId: components["schemas"]["Uuid"];
        KioskId: components["schemas"]["Uuid"];
        /** @description Tamaño de página (1–200, por defecto 50). Fuera de rango → 400. */
        Limit: number;
        /** @description Búsqueda libre por prefijo/trigram sobre los campos documentados. Nunca IPs de clientes (D1). */
        Q: string;
        /** @description Atajo relativo; el servidor lo resuelve y lo devuelve en `meta`. */
        Range: components["schemas"]["RelativeRange"];
        ReportDefinitionId: components["schemas"]["Uuid"];
        ReportId: components["schemas"]["Uuid"];
        ReputationSourceId: components["schemas"]["Uuid"];
        RouterId: components["schemas"]["Uuid"];
        SiteId: components["schemas"]["Uuid"];
        /** @description Filtro por nodo (multivalor por comas). */
        SiteIdFilter: string;
        /** @description Segundos por bucket; omitido → el servidor elige (≤ ~500 puntos/serie). */
        Step: number;
        TenantId: components["schemas"]["Uuid"];
        /** @description Fin RFC 3339 UTC (por defecto, ahora). */
        To: string;
        WidgetId: string;
        /** @description Defensa CSRF en rutas autenticadas por cookie; valor fijo `horus`. */
        XRequestedWith: "horus";
    };
    requestBodies: never;
    headers: {
        /** @description Versión del recurso (`"<version>"`). */
        ETag: string;
        /** @description URL absoluta o relativa del recurso creado; las absolutas se construyen con HORUS_PUBLIC_BASE_URL (D14), nunca con `Host`. */
        Location: string;
        /** @description Draft IETF ratelimit-headers, p. ej. `"default";r=412;t=23`. */
        RateLimit: string;
        /** @description p. ej. `"default";q=600;w=60`. */
        RateLimitPolicy: string;
        /** @description Segundos hasta reintentar. */
        RetryAfter: number;
    };
    pathItems: never;
}
export type $defs = Record<string, never>;
export interface operations {
    getCustomerTraffic: {
        parameters: {
            query?: {
                /** @description Inicio RFC 3339 UTC, intervalo semiabierto `[from, to)`. Excluyente con `range`. */
                from?: components["parameters"]["From"];
                group_by?: "none" | "service" | "category" | "asn";
                /** @description Atajo relativo; el servidor lo resuelve y lo devuelve en `meta`. */
                range?: components["parameters"]["Range"];
                /** @description Segundos por bucket; omitido → el servidor elige (≤ ~500 puntos/serie). */
                step?: components["parameters"]["Step"];
                /** @description Fin RFC 3339 UTC (por defecto, ahora). */
                to?: components["parameters"]["To"];
            };
            header?: never;
            path: {
                customer_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Series. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["SeriesResponse"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["ServiceUnavailable"];
        };
    };
    getRouterInterfaceMetrics: {
        parameters: {
            query?: {
                /** @description Inicio RFC 3339 UTC, intervalo semiabierto `[from, to)`. Excluyente con `range`. */
                from?: components["parameters"]["From"];
                /** @description UUID de interfaces separados por comas (máximo 8). */
                interface_id?: string;
                metrics?: string;
                /** @description Atajo relativo; el servidor lo resuelve y lo devuelve en `meta`. */
                range?: components["parameters"]["Range"];
                /** @description Segundos por bucket; omitido → el servidor elige (≤ ~500 puntos/serie). */
                step?: components["parameters"]["Step"];
                /** @description Fin RFC 3339 UTC (por defecto, ahora). */
                to?: components["parameters"]["To"];
            };
            header?: never;
            path: {
                router_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Series. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["SeriesResponse"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["ServiceUnavailable"];
        };
    };
    getRouterMetrics: {
        parameters: {
            query?: {
                /** @description Inicio RFC 3339 UTC, intervalo semiabierto `[from, to)`. Excluyente con `range`. */
                from?: components["parameters"]["From"];
                metrics?: string;
                /** @description Atajo relativo; el servidor lo resuelve y lo devuelve en `meta`. */
                range?: components["parameters"]["Range"];
                /** @description Segundos por bucket; omitido → el servidor elige (≤ ~500 puntos/serie). */
                step?: components["parameters"]["Step"];
                /** @description Fin RFC 3339 UTC (por defecto, ahora). */
                to?: components["parameters"]["To"];
            };
            header?: never;
            path: {
                router_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Series (`metric` = nombre pedido; `unit` percent | celsius | ms | ratio | seconds). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["SeriesResponse"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["ServiceUnavailable"];
        };
    };
    getTrafficAttribution: {
        parameters: {
            query?: {
                /** @description Atajo relativo; el servidor lo resuelve y lo devuelve en `meta`. */
                range?: components["parameters"]["Range"];
                /** @description Filtro por nodo (multivalor por comas). */
                site_id?: components["parameters"]["SiteIdFilter"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Proporciones. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: {
                            attributed_ratio: number;
                            infrastructure_ratio: number;
                            unattributed_ratio: number;
                        };
                        meta: components["schemas"]["AnalyticsMeta"];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            503: components["responses"]["ServiceUnavailable"];
        };
    };
    getTrafficTimeseries: {
        parameters: {
            query?: {
                /** @description Inicio RFC 3339 UTC, intervalo semiabierto `[from, to)`. Excluyente con `range`. */
                from?: components["parameters"]["From"];
                metrics?: string;
                /** @description Atajo relativo; el servidor lo resuelve y lo devuelve en `meta`. */
                range?: components["parameters"]["Range"];
                /** @description Filtro por nodo (multivalor por comas). */
                site_id?: components["parameters"]["SiteIdFilter"];
                /** @description Segundos por bucket; omitido → el servidor elige (≤ ~500 puntos/serie). */
                step?: components["parameters"]["Step"];
                /** @description Fin RFC 3339 UTC (por defecto, ahora). */
                to?: components["parameters"]["To"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Series. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["SeriesResponse"];
                };
            };
            401: components["responses"]["Unauthorized"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["ServiceUnavailable"];
            504: components["responses"]["GatewayTimeout"];
        };
    };
    getTrafficTop: {
        parameters: {
            query: {
                /** @description Ámbito de cliente (exige `traffic.customer.read`). */
                customer_id?: components["schemas"]["Uuid"];
                dimension: "customers" | "services" | "categories" | "organizations" | "asns";
                direction?: "both" | "download" | "upload";
                /** @description Inicio RFC 3339 UTC, intervalo semiabierto `[from, to)`. Excluyente con `range`. */
                from?: components["parameters"]["From"];
                n?: number;
                /** @description Atajo relativo; el servidor lo resuelve y lo devuelve en `meta`. */
                range?: components["parameters"]["Range"];
                /** @description Filtro por nodo (multivalor por comas). */
                site_id?: components["parameters"]["SiteIdFilter"];
                /** @description Fin RFC 3339 UTC (por defecto, ahora). */
                to?: components["parameters"]["To"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Top. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["TopResult"];
                        meta: components["schemas"]["AnalyticsMeta"];
                    };
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["ServiceUnavailable"];
            504: components["responses"]["GatewayTimeout"];
        };
    };
    listApiTokenTemplates: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Plantillas. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["ApiTokenTemplate"][];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    listApiTokens: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Tokens (más recientes primero; incluye revocados y caducados). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["ApiToken"][];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    createApiToken: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ApiTokenInput"];
            };
        };
        responses: {
            /** @description Token creado (incluye `token`, que no se vuelve a mostrar). */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ApiTokenIssued"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    revokeApiToken: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                api_token_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            204: components["responses"]["NoContent"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listAsnRules: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Reglas del ISP. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["AsnRule"][];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    createAsnRule: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["AsnRuleInput"];
            };
        };
        responses: {
            /** @description Creada. */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AsnRule"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    deleteAsnRule: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                asn_rule_id: components["parameters"]["AsnRuleId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            204: components["responses"]["NoContent"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    updateAsnRule: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                asn_rule_id: components["parameters"]["AsnRuleId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["AsnRulePatch"];
                "application/merge-patch+json": components["schemas"]["AsnRulePatch"];
            };
        };
        responses: {
            /** @description Actualizada. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AsnRule"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    getAsnRuleCatalog: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Opciones del catálogo vigente. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AsnRuleCatalog"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    getAsnInfo: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                asn: number;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Datos del ASN. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AsnInfo"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listAssistAnalyses: {
        parameters: {
            query: {
                /** @description Id del hallazgo o del router. */
                context_id: string;
                feature: components["schemas"]["AssistFeature"];
                limit?: number;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Análisis guardados del recurso. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["AssistAnalysis"][];
                    };
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            502: components["responses"]["BadGateway"];
            503: components["responses"]["ServiceUnavailable"];
        };
    };
    createAssistAnalysis: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["AssistAnalysisRequest"];
            };
        };
        responses: {
            /** @description Análisis en Markdown (enmascarado; sin HTML). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AssistAnalysis"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
            429: components["responses"]["TooManyRequests"];
            502: components["responses"]["BadGateway"];
            503: components["responses"]["ServiceUnavailable"];
        };
    };
    listAssistAudit: {
        parameters: {
            query?: {
                /** @description Cursor opaco (`page.next_cursor`); ligado a filtros, orden y `tid`. Ajeno → `400 INVALID_CURSOR`. */
                cursor?: components["parameters"]["Cursor"];
                /** @description Tamaño de página (1–200, por defecto 50). Fuera de rango → 400. */
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Página de auditoría. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["AssistAuditEntry"][];
                        page: components["schemas"]["PageInfo"];
                    };
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    assistMcp: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    [key: string]: unknown;
                };
            };
        };
        responses: {
            /** @description Respuesta JSON-RPC 2.0. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        [key: string]: unknown;
                    };
                };
            };
            /** @description Notificación aceptada (sin cuerpo). */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            503: components["responses"]["ServiceUnavailable"];
        };
    };
    listAssistMcpTokens: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Tokens. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["McpToken"][];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    createAssistMcpToken: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** @example Claude Desktop de Ana */
                    name: string;
                };
            };
        };
        responses: {
            /** @description Token creado (incluye `token`, que no se vuelve a mostrar). */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["McpTokenIssued"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    revokeAssistMcpToken: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                token_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            204: components["responses"]["NoContent"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    getAssistSettings: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Configuración. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AssistSettings"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    putAssistSettings: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["AssistSettingsUpdate"];
            };
        };
        responses: {
            /** @description Configuración guardada. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AssistSettings"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["ServiceUnavailable"];
        };
    };
    testAssistSettings: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Resultado (también cuando falla el proveedor; ver `ok`). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AssistTestResult"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            429: components["responses"]["TooManyRequests"];
            503: components["responses"]["ServiceUnavailable"];
        };
    };
    getAssistStatus: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Estado del asistente. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AssistStatus"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    authAcceptInvitation: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    display_name?: string | null;
                    password?: string | null;
                    token: string;
                };
            };
        };
        responses: {
            204: components["responses"]["NoContent"];
            422: components["responses"]["UnprocessableEntity"];
            429: components["responses"]["TooManyRequests"];
        };
    };
    authInspectInvitation: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    token: string;
                };
            };
        };
        responses: {
            /** @description Invitación vigente. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        /** Format: email */
                        email: string;
                        /** Format: date-time */
                        expires_at: string;
                        /** @description true si la cuenta es nueva (debe elegir contraseña); false si ya existía. */
                        needs_password: boolean;
                        tenant_name: string;
                    };
                };
            };
            422: components["responses"]["UnprocessableEntity"];
            429: components["responses"]["TooManyRequests"];
        };
    };
    authLogin: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    password: string;
                    username: string;
                };
            };
        };
        responses: {
            /** @description Sesión creada o 2FA requerida. */
            200: {
                headers: {
                    /** @description `__Secure-hf_rt` (solo si no se requiere 2FA). */
                    "Set-Cookie"?: string;
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AccessTokenResponse"] | components["schemas"]["MfaChallenge"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            429: components["responses"]["TooManyRequests"];
        };
    };
    authLogout: {
        parameters: {
            query?: never;
            header: {
                /** @description Defensa CSRF en rutas autenticadas por cookie; valor fijo `horus`. */
                "X-Requested-With": components["parameters"]["XRequestedWith"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            204: components["responses"]["NoContent"];
            403: components["responses"]["Forbidden"];
        };
    };
    authMfaVerify: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    code: string;
                    mfa_token: string;
                };
            };
        };
        responses: {
            /** @description Sesión creada. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AccessTokenResponse"];
                };
            };
            401: components["responses"]["Unauthorized"];
            429: components["responses"]["TooManyRequests"];
        };
    };
    authForgotPassword: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** Format: email */
                    email: string;
                };
            };
        };
        responses: {
            /** @description Aceptado (siempre). */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        /** @description true si la instalación envía correo; false si no hay SMTP configurado. */
                        email_delivery: boolean;
                    };
                };
            };
            422: components["responses"]["UnprocessableEntity"];
            429: components["responses"]["TooManyRequests"];
        };
    };
    authResetPassword: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    new_password: string;
                    token: string;
                };
            };
        };
        responses: {
            204: components["responses"]["NoContent"];
            422: components["responses"]["UnprocessableEntity"];
            429: components["responses"]["TooManyRequests"];
        };
    };
    authInspectPasswordReset: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    token: string;
                };
            };
        };
        responses: {
            /** @description Enlace vigente. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        display_name: string;
                        /** Format: email */
                        email: string;
                        /** Format: date-time */
                        expires_at: string;
                    };
                };
            };
            /** @description Enlace inválido, caducado o ya usado (`errors[0].field = token`, code INVALID_TOKEN | EXPIRED | USED). */
            422: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["Problem"];
                };
            };
            429: components["responses"]["TooManyRequests"];
        };
    };
    authReauth: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    code?: string | null;
                    password: string;
                };
            };
        };
        responses: {
            /** @description Token renovado con `auth_time` reciente (mismo ámbito que el presentado). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AccessTokenResponse"];
                };
            };
            401: components["responses"]["Unauthorized"];
        };
    };
    authRefresh: {
        parameters: {
            query?: never;
            header: {
                /** @description Defensa CSRF en rutas autenticadas por cookie; valor fijo `horus`. */
                "X-Requested-With": components["parameters"]["XRequestedWith"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Nuevo par (cookie rotada). */
            200: {
                headers: {
                    "Set-Cookie"?: string;
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AccessTokenResponse"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    authIssueToken: {
        parameters: {
            query?: never;
            header?: {
                /** @description Recomendado en `POST`. */
                "Idempotency-Key"?: components["parameters"]["IdempotencyKeyOptional"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    tenant_id: components["schemas"]["Uuid"];
                } | {
                    /** @constant */
                    scope: "platform";
                };
            };
        };
        responses: {
            /** @description Token emitido. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AccessTokenResponse"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    getClassificationStatus: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Estado de los datasets. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: {
                            asn_dataset: components["schemas"]["DatasetStatus"];
                            catalog: components["schemas"]["DatasetStatus"];
                            reputation: components["schemas"]["DatasetStatus"];
                        };
                        /** @enum {string} */
                        source: "ingester" | "unknown";
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    deleteClientPrefix: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                client_prefix_id: components["parameters"]["ClientPrefixId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            204: components["responses"]["NoContent"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    updateClientPrefix: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                client_prefix_id: components["parameters"]["ClientPrefixId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ClientPrefixPatch"];
                "application/merge-patch+json": components["schemas"]["ClientPrefixPatch"];
            };
        };
        responses: {
            /** @description Actualizado. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ClientPrefix"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    confirmClientPrefix: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                client_prefix_id: components["parameters"]["ClientPrefixId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Confirmado. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ClientPrefix"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    listCustomers: {
        parameters: {
            query?: {
                client_prefix_id?: string;
                /** @description Posibles comerciales: sugerencia comercial del scoring o uso comercial sospechado, sin tipo fijado a mano (I2). */
                commercial_candidate?: boolean;
                commercial_use_suspected?: boolean;
                /** @description Cursor opaco (`page.next_cursor`); ligado a filtros, orden y `tid`. Ajeno → `400 INVALID_CURSOR`. */
                cursor?: components["parameters"]["Cursor"];
                has_open_findings?: boolean;
                kind?: string;
                kind_locked?: boolean;
                kind_source?: string;
                last_seen_gte?: string;
                last_seen_lte?: string;
                /** @description Tamaño de página (1–200, por defecto 50). Fuera de rango → 400. */
                limit?: components["parameters"]["Limit"];
                /** @description Búsqueda sobre `alias` (nunca IP). */
                q?: string;
                realm_id?: string;
                security_state?: string;
                /** @description Filtro por nodo (multivalor por comas). */
                site_id?: components["parameters"]["SiteIdFilter"];
                sort?: "last_seen" | "-last_seen" | "first_seen" | "-first_seen" | "kind_changed_at" | "-kind_changed_at";
                status?: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Página de clientes. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CustomerPage"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    getCustomer: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                customer_id: components["parameters"]["CustomerId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Cliente. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CustomerDetail"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
        };
    };
    updateCustomer: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                customer_id: components["parameters"]["CustomerId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CustomerPatch"];
                "application/merge-patch+json": components["schemas"]["CustomerPatch"];
            };
        };
        responses: {
            /** @description Actualizado. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CustomerDetail"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    getCustomerExternalIdentity: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                customer_id: components["parameters"]["CustomerId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Identidad vigente e historial (más reciente primero, máx. 50). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CustomerExternalIdentity"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
        };
    };
    refreshCustomerExternalIdentity: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                customer_id: components["parameters"]["CustomerId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Identidad tras la resolución. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CustomerExternalIdentity"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    listCustomerFindings: {
        parameters: {
            query?: {
                /** @description Cursor opaco (`page.next_cursor`); ligado a filtros, orden y `tid`. Ajeno → `400 INVALID_CURSOR`. */
                cursor?: components["parameters"]["Cursor"];
                /** @description Tamaño de página (1–200, por defecto 50). Fuera de rango → 400. */
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path: {
                customer_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Página de hallazgos. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["finding.schema"][];
                        page: components["schemas"]["PageInfo"];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
        };
    };
    listCustomerKindHistory: {
        parameters: {
            query?: {
                /** @description Cursor opaco (`page.next_cursor`); ligado a filtros, orden y `tid`. Ajeno → `400 INVALID_CURSOR`. */
                cursor?: components["parameters"]["Cursor"];
                /** @description Tamaño de página (1–200, por defecto 50). Fuera de rango → 400. */
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path: {
                customer_id: components["parameters"]["CustomerId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Historial (más reciente primero). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["KindChange"][];
                        page: components["schemas"]["PageInfo"];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
        };
    };
    resetCustomer: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                customer_id: components["parameters"]["CustomerId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    reason: string;
                };
            };
        };
        responses: {
            /** @description Reiniciado. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CustomerDetail"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    getCustomerScoring: {
        parameters: {
            query?: {
                /** @description Serie diaria hacia atrás (`90d`, `12w`, `48h`; máximo 400d). Sin range no hay serie. */
                range?: string;
            };
            header?: never;
            path: {
                customer_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Último scoring (o null) y serie diaria. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CustomerScoringResponse"];
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    setCustomerKind: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                customer_id: components["parameters"]["CustomerId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    kind: components["schemas"]["CustomerKind"];
                    reason: string;
                };
            };
        };
        responses: {
            /** @description Tipo aplicado (o sin cambio si ya estaba así y bloqueado). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CustomerDetail"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    unlockCustomerKind: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                customer_id: components["parameters"]["CustomerId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Desbloqueado. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CustomerDetail"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    lookupCustomers: {
        parameters: {
            query?: {
                /** @description Cursor opaco (`page.next_cursor`); ligado a filtros, orden y `tid`. Ajeno → `400 INVALID_CURSOR`. */
                cursor?: components["parameters"]["Cursor"];
                /** @description Tamaño de página (1–200, por defecto 50). Fuera de rango → 400. */
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    address: components["schemas"]["IpAddress"];
                    site_id?: components["schemas"]["Uuid"];
                } | {
                    prefix: components["schemas"]["Cidr"];
                    realm_id?: components["schemas"]["Uuid"];
                    site_id?: components["schemas"]["Uuid"];
                };
            };
        };
        responses: {
            /** @description Coincidencias (misma forma que la colección). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CustomerPage"];
                };
            };
            401: components["responses"]["Unauthorized"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    getCustomerStats: {
        parameters: {
            query?: {
                /** @description Filtro por nodo (multivalor por comas). */
                site_id?: components["parameters"]["SiteIdFilter"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Conteos (sin IPs; apto para kioscos). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CustomerStats"];
                };
            };
            401: components["responses"]["Unauthorized"];
        };
    };
    listDashboards: {
        parameters: {
            query?: {
                /** @description Cursor opaco (`page.next_cursor`); ligado a filtros, orden y `tid`. Ajeno → `400 INVALID_CURSOR`. */
                cursor?: components["parameters"]["Cursor"];
                /** @description Tamaño de página (1–200, por defecto 50). Fuera de rango → 400. */
                limit?: components["parameters"]["Limit"];
                visibility?: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Página de dashboards (sin widgets). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["DashboardSummary"][];
                        page: components["schemas"]["PageInfo"];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    createDashboard: {
        parameters: {
            query?: never;
            header?: {
                /** @description Recomendado en `POST`. */
                "Idempotency-Key"?: components["parameters"]["IdempotencyKeyOptional"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["DashboardInput"];
            };
        };
        responses: {
            /** @description Creado. */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["dashboard.schema"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    getDashboard: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                dashboard_id: components["parameters"]["DashboardId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Dashboard. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["dashboard.schema"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
        };
    };
    deleteDashboard: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                dashboard_id: components["parameters"]["DashboardId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            204: components["responses"]["NoContent"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    updateDashboard: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                dashboard_id: components["parameters"]["DashboardId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["DashboardPatch"];
                "application/merge-patch+json": components["schemas"]["DashboardPatch"];
            };
        };
        responses: {
            /** @description Actualizado. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["dashboard.schema"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    duplicateDashboard: {
        parameters: {
            query?: never;
            header?: {
                /** @description Recomendado en `POST`. */
                "Idempotency-Key"?: components["parameters"]["IdempotencyKeyOptional"];
            };
            path: {
                dashboard_id: components["parameters"]["DashboardId"];
            };
            cookie?: never;
        };
        requestBody?: {
            content: {
                "application/json": {
                    name?: string;
                };
            };
        };
        responses: {
            /** @description Copia. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["dashboard.schema"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
        };
    };
    replaceDashboardLayout: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                dashboard_id: components["parameters"]["DashboardId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["LayoutReplace"];
            };
        };
        responses: {
            /** @description Dashboard con el layout nuevo. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["dashboard.schema"];
                };
            };
            401: components["responses"]["Unauthorized"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    addDashboardWidget: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                dashboard_id: components["parameters"]["DashboardId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Widget"];
            };
        };
        responses: {
            /** @description Dashboard actualizado. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["dashboard.schema"];
                };
            };
            401: components["responses"]["Unauthorized"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    deleteDashboardWidget: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                dashboard_id: components["parameters"]["DashboardId"];
                widget_id: components["parameters"]["WidgetId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Dashboard actualizado. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["dashboard.schema"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    updateDashboardWidget: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                dashboard_id: components["parameters"]["DashboardId"];
                widget_id: components["parameters"]["WidgetId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["WidgetPatch"];
                "application/merge-patch+json": components["schemas"]["WidgetPatch"];
            };
        };
        responses: {
            /** @description Dashboard actualizado. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["dashboard.schema"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    getWidgetData: {
        parameters: {
            query?: {
                /** @description Inicio RFC 3339 UTC, intervalo semiabierto `[from, to)`. Excluyente con `range`. */
                from?: components["parameters"]["From"];
                /** @description Atajo relativo; el servidor lo resuelve y lo devuelve en `meta`. */
                range?: components["parameters"]["Range"];
                /** @description Variable de dashboard (si el tipo la acepta). */
                site_id?: components["schemas"]["Uuid"];
                /** @description Fin RFC 3339 UTC (por defecto, ahora). */
                to?: components["parameters"]["To"];
            };
            header?: never;
            path: {
                dashboard_id: components["parameters"]["DashboardId"];
                widget_id: components["parameters"]["WidgetId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Datos. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["widget-data.schema"];
                };
            };
            /** @description Sin cambios. */
            304: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["ServiceUnavailable"];
            504: components["responses"]["GatewayTimeout"];
        };
    };
    getDetectionConfig: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Parámetros vigentes. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        /** @description Detectores con ajustes propios del ISP. */
                        customized: string[];
                        /** @description Un objeto por detector (c2_contact, scanning, watched_ports, fanout, smtp, ddos, beaconing, sustained_upload) con enabled, window y sus umbrales; customer_scoring (scoring residencial/comercial: umbrales, histéresis, pesos); más auto_expire. */
                        detectors: {
                            [key: string]: unknown;
                        };
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    enrollWireguard: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** @description Clave pública WireGuard (44 caracteres base64). */
                    public_key: string;
                    token: string;
                };
            };
        };
        responses: {
            /** @description Clave aceptada; el hub añade el peer en < 10 s. */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        /** @constant */
                        peer_status: "pending_handshake";
                    };
                };
            };
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
            429: components["responses"]["TooManyRequests"];
        };
    };
    listFindings: {
        parameters: {
            query?: {
                /** @description Cursor opaco (`page.next_cursor`); ligado a filtros, orden y `tid`. Ajeno → `400 INVALID_CURSOR`. */
                cursor?: components["parameters"]["Cursor"];
                customer_id?: components["schemas"]["Uuid"];
                kind?: string;
                /** @description Tamaño de página (1–200, por defecto 50). Fuera de rango → 400. */
                limit?: components["parameters"]["Limit"];
                opened_at_gte?: string;
                severity?: string;
                /** @description Filtro por nodo (multivalor por comas). */
                site_id?: components["parameters"]["SiteIdFilter"];
                sort?: "-severity" | "-last_seen_at" | "-opened_at" | "opened_at";
                state?: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Página de hallazgos. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["finding.schema"][];
                        page: components["schemas"]["PageInfo"];
                    };
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    getFinding: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                finding_id: components["parameters"]["FindingId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Hallazgo. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["finding.schema"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
        };
    };
    acknowledgeFinding: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                finding_id: components["parameters"]["FindingId"];
            };
            cookie?: never;
        };
        requestBody?: {
            content: {
                "application/json": components["schemas"]["FindingTransition"];
            };
        };
        responses: {
            /** @description Hallazgo actualizado. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["finding.schema"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    getFindingEvidence: {
        parameters: {
            query?: {
                /** @description Cursor opaco (`page.next_cursor`); ligado a filtros, orden y `tid`. Ajeno → `400 INVALID_CURSOR`. */
                cursor?: components["parameters"]["Cursor"];
                /** @description Tamaño de página (1–200, por defecto 50). Fuera de rango → 400. */
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path: {
                finding_id: components["parameters"]["FindingId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Flujos. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: {
                            bytes: components["schemas"]["Uint64String"];
                            /** @enum {string} */
                            direction: "upload" | "download" | "internal" | "unknown";
                            packets: components["schemas"]["Uint64String"];
                            protocol: number;
                            remote_asn?: number | null;
                            remote_ip: components["schemas"]["IpAddress"];
                            remote_port: number;
                            reputation_category?: string | null;
                            tcp_flags?: number | null;
                            ts: components["schemas"]["Timestamp"];
                        }[];
                        page: components["schemas"]["PageInfo"];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["ServiceUnavailable"];
        };
    };
    markFindingFalsePositive: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                finding_id: components["parameters"]["FindingId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    comment: string;
                    /** @default 30 */
                    silence_days?: number;
                };
            };
        };
        responses: {
            /** @description Marcado como falso positivo. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["finding.schema"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    resolveFinding: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                finding_id: components["parameters"]["FindingId"];
            };
            cookie?: never;
        };
        requestBody?: {
            content: {
                "application/json": components["schemas"]["FindingTransition"];
            };
        };
        responses: {
            /** @description Hallazgo resuelto. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["finding.schema"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    bulkResolveFindings: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    comment: string;
                    kind?: components["schemas"]["FindingKind"];
                    /** @enum {string} */
                    severity?: "low" | "medium" | "high" | "critical";
                    site_id?: components["schemas"]["Uuid"];
                    /** @description Estados a cerrar (por defecto ambos: open y acknowledged). */
                    states?: ("open" | "acknowledged")[];
                };
            };
        };
        responses: {
            /** @description Hallazgos resueltos. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        resolved: number;
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    listFlowExporters: {
        parameters: {
            query?: {
                /** @description Filtro por nodo (multivalor por comas). */
                site_id?: components["parameters"]["SiteIdFilter"];
                state?: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Exportadores (uno por router principal). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["FlowExporter"][];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    getFlowExporter: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                router_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Exportador. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["FlowExporter"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
        };
    };
    listInfraServices: {
        parameters: {
            query?: {
                /** @description Filtro por nodo (multivalor por comas). */
                site_id?: components["parameters"]["SiteIdFilter"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Servicios. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["InfraService"][];
                        meta: {
                            generated_at: components["schemas"]["Timestamp"];
                            scheduler: components["schemas"]["InfraSchedulerHealth"];
                        };
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            503: components["responses"]["ServiceUnavailable"];
        };
    };
    createInfraService: {
        parameters: {
            query?: never;
            header?: {
                /** @description Recomendado en `POST`. */
                "Idempotency-Key"?: components["parameters"]["IdempotencyKeyOptional"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["InfraServiceInput"];
            };
        };
        responses: {
            /** @description Creado (estado `unknown` hasta el primer chequeo). */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["InfraService"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["ServiceUnavailable"];
        };
    };
    getInfraService: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                service_id: components["parameters"]["InfraServiceId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Servicio (cada chequeo incluye `recent`, los últimos resultados). */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["InfraService"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    deleteInfraService: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                service_id: components["parameters"]["InfraServiceId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            204: components["responses"]["NoContent"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    updateInfraService: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                service_id: components["parameters"]["InfraServiceId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["InfraServicePatch"];
                "application/merge-patch+json": components["schemas"]["InfraServicePatch"];
            };
        };
        responses: {
            /** @description Actualizado. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["InfraService"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    getInfraServiceRouterSnippet: {
        parameters: {
            query?: {
                /** @description Router por el que se accede (por defecto `probe_via_router_id` del servicio). */
                router_id?: components["schemas"]["Uuid"];
            };
            header?: never;
            path: {
                service_id: components["parameters"]["InfraServiceId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Script y notas. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["InfraRouterSnippet"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["ServiceUnavailable"];
        };
    };
    getArgosIntegration: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Conector (`configured = false` si nunca se configuró). */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ArgosIntegration"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    putArgosIntegration: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ArgosIntegrationInput"];
            };
        };
        responses: {
            /** @description Conector guardado. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ArgosIntegration"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    testArgosIntegration: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Resultado. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ArgosConnectionTest"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
        };
    };
    rotateArgosWebhookSecret: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description URL y secreto (el secreto no se vuelve a mostrar). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ArgosWebhookSecret"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
        };
    };
    receiveArgosWebhook: {
        parameters: {
            query?: never;
            header: {
                "X-Webhook-Id": string;
                "X-Webhook-Signature": string;
                "X-Webhook-Timestamp": string;
            };
            path: {
                /** @description Identificador opaco del conector (no es el ISP); forma parte de la URL que se copia en ArgosISP. */
                connector_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ArgosWebhookEvent"];
            };
        };
        responses: {
            /** @description Aceptado (o duplicado ya recibido). */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            /** @description Cuerpo de más de 1 MiB. */
            413: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            429: components["responses"]["TooManyRequests"];
        };
    };
    getIntegrationConsumption: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ConsumptionRequest"];
            };
        };
        responses: {
            /** @description Consumo por elemento, en el orden pedido. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ConsumptionResponse"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["ServiceUnavailable"];
        };
    };
    lookupIpIntegration: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["IpLookupRequest"];
            };
        };
        responses: {
            /** @description Todo sobre la IP. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["IpLookupResponse"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    getKioskConfig: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Configuración. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        critical_finding_banner: boolean;
                        /** @description Si la versión del frontend cambió, la TV se recarga en el siguiente cambio de dashboard. */
                        frontend_min_version: string | null;
                        items: components["schemas"]["PlaylistItem"][];
                        kiosk_id: components["schemas"]["Uuid"];
                        playlist_id?: components["schemas"]["Uuid"] | null;
                        show_personal_data: boolean;
                        tenant_id: components["schemas"]["Uuid"];
                        tenant_name?: string;
                        /** @enum {string} */
                        transition?: "fade" | "none";
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    kioskEnroll: {
        parameters: {
            query?: never;
            header: {
                /** @description Defensa CSRF en rutas autenticadas por cookie; valor fijo `horus`. */
                "X-Requested-With": components["parameters"]["XRequestedWith"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    code: string;
                };
            };
        };
        responses: {
            /** @description Enrolado; `Set-Cookie: __Secure-hf_kiosk`. */
            204: {
                headers: {
                    "Set-Cookie"?: string;
                    [name: string]: unknown;
                };
                content?: never;
            };
            422: components["responses"]["UnprocessableEntity"];
            429: components["responses"]["TooManyRequests"];
        };
    };
    kioskIssueToken: {
        parameters: {
            query?: never;
            header: {
                /** @description Defensa CSRF en rutas autenticadas por cookie; valor fijo `horus`. */
                "X-Requested-With": components["parameters"]["XRequestedWith"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description JWT de kiosco (10 min) y cookie rotada. */
            200: {
                headers: {
                    "Set-Cookie"?: string;
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AccessTokenResponse"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    listKiosks: {
        parameters: {
            query?: {
                /** @description Cursor opaco (`page.next_cursor`); ligado a filtros, orden y `tid`. Ajeno → `400 INVALID_CURSOR`. */
                cursor?: components["parameters"]["Cursor"];
                /** @description Tamaño de página (1–200, por defecto 50). Fuera de rango → 400. */
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Página de kioscos. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["Kiosk"][];
                        page: components["schemas"]["PageInfo"];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
        };
    };
    createKiosk: {
        parameters: {
            query?: never;
            header?: {
                /** @description Recomendado en `POST`. */
                "Idempotency-Key"?: components["parameters"]["IdempotencyKeyOptional"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["KioskInput"];
            };
        };
        responses: {
            /** @description Creado. */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Kiosk"];
                };
            };
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    getKiosk: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                kiosk_id: components["parameters"]["KioskId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Kiosco. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Kiosk"];
                };
            };
            404: components["responses"]["NotFound"];
        };
    };
    updateKiosk: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                kiosk_id: components["parameters"]["KioskId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["KioskInput"];
                "application/merge-patch+json": components["schemas"]["KioskInput"];
            };
        };
        responses: {
            /** @description Actualizado. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Kiosk"];
                };
            };
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    createKioskEnrollmentCode: {
        parameters: {
            query?: never;
            header: {
                /** @description UUID generado por el cliente; obligatorio en acciones con efecto externo (falta → 428). */
                "Idempotency-Key": components["parameters"]["IdempotencyKeyRequired"];
            };
            path: {
                kiosk_id: components["parameters"]["KioskId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Código (solo esta vez; `no-store`). */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        code: string;
                        expires_at: components["schemas"]["Timestamp"];
                        /** Format: uri */
                        qr_url: string;
                    };
                };
            };
            428: components["responses"]["PreconditionRequired"];
        };
    };
    revokeKiosk: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                kiosk_id: components["parameters"]["KioskId"];
            };
            cookie?: never;
        };
        requestBody?: {
            content: {
                "application/json": {
                    reason?: string;
                };
            };
        };
        responses: {
            /** @description Kiosco revocado. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Kiosk"];
                };
            };
            401: components["responses"]["Unauthorized"];
        };
    };
    listLibreNmsRouters: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Routers (sin secretos). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["LibreNmsRouter"][];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            503: components["responses"]["ServiceUnavailable"];
        };
    };
    setLibreNmsRouter: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                router_id: components["parameters"]["RouterId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["LibreNmsRouterInput"];
            };
        };
        responses: {
            /** @description Configuración guardada. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["LibreNmsRouter"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["ServiceUnavailable"];
        };
    };
    clearLibreNmsRouter: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                router_id: components["parameters"]["RouterId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            204: components["responses"]["NoContent"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
        };
    };
    putLibreNmsRouterSnmpCredentials: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                router_id: components["parameters"]["RouterId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["LibreNmsSnmpCredentials"];
            };
        };
        responses: {
            204: components["responses"]["NoContent"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    getMe: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Usuario. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Me"];
                };
            };
            401: components["responses"]["Unauthorized"];
        };
    };
    updateMe: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["MePatch"];
                "application/merge-patch+json": components["schemas"]["MePatch"];
            };
        };
        responses: {
            /** @description Actualizado. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Me"];
                };
            };
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    changeMyPassword: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    current_password: string;
                    new_password: string;
                };
            };
        };
        responses: {
            204: components["responses"]["NoContent"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    confirmMyTotp: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    code: string;
                };
            };
        };
        responses: {
            /** @description 10 códigos de recuperación (solo esta vez). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        recovery_codes: string[];
                    };
                };
            };
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    enrollMyTotp: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Secreto (solo esta vez; `Cache-Control: no-store`). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        otpauth_uri: string;
                        secret: string;
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
        };
    };
    listMembers: {
        parameters: {
            query?: {
                /** @description Cursor opaco (`page.next_cursor`); ligado a filtros, orden y `tid`. Ajeno → `400 INVALID_CURSOR`. */
                cursor?: components["parameters"]["Cursor"];
                /** @description Solo en colecciones administrativas pequeñas; ignorado en las grandes. */
                include_total?: components["parameters"]["IncludeTotal"];
                /** @description Tamaño de página (1–200, por defecto 50). Fuera de rango → 400. */
                limit?: components["parameters"]["Limit"];
                /** @description Búsqueda libre por prefijo/trigram sobre los campos documentados. Nunca IPs de clientes (D1). */
                q?: components["parameters"]["Q"];
                status?: "active" | "invited" | "revoked";
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Página de miembros. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["Member"][];
                        page: components["schemas"]["PageInfo"];
                    };
                };
            };
            403: components["responses"]["Forbidden"];
        };
    };
    inviteMember: {
        parameters: {
            query?: never;
            header?: {
                /** @description Recomendado en `POST`. */
                "Idempotency-Key"?: components["parameters"]["IdempotencyKeyOptional"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** Format: email */
                    email: string;
                    role_assignments: components["schemas"]["RoleAssignment"][];
                };
            };
        };
        responses: {
            /**
             * @description Miembro añadido (cuenta activa existente: `outcome=added`, sin enlace) o invitado
             *     (`outcome=invited`): el enlace de un solo uso se devuelve a quien invita para que lo
             *     comparta (compartir nativo o copiar) y, si hay SMTP configurado, también se envía por
             *     correo (`email_sent`). El token va en el fragmento (#token=…): nunca llega a un servidor.
             */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["InvitationIssued"];
                };
            };
            403: components["responses"]["Forbidden"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    removeMember: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                user_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            204: components["responses"]["NoContent"];
            404: components["responses"]["NotFound"];
        };
    };
    updateMember: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                user_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["MemberPatch"];
                "application/merge-patch+json": components["schemas"]["MemberPatch"];
            };
        };
        responses: {
            /** @description Miembro actualizado. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Member"];
                };
            };
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    reissueMemberInvitation: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                user_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Enlace nuevo (y reenvío por correo si hay SMTP). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["InvitationIssued"];
                };
            };
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    resetMemberPassword: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                user_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Enlace emitido. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["PasswordResetIssued"];
                };
            };
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    replaceMemberRoleAssignments: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                user_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RoleAssignment"][];
            };
        };
        responses: {
            /** @description Asignaciones vigentes. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["RoleAssignment"][];
                };
            };
            403: components["responses"]["Forbidden"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    listNotificationChannels: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Canales (sin secretos). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["NotificationChannel"][];
                        page: components["schemas"]["PageInfo"];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    createNotificationChannel: {
        parameters: {
            query?: never;
            header?: {
                /** @description Recomendado en `POST`. */
                "Idempotency-Key"?: components["parameters"]["IdempotencyKeyOptional"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["NotificationChannelInput"];
            };
        };
        responses: {
            /** @description Creado (estado `unverified` hasta la primera prueba correcta). */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["NotificationChannel"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    getNotificationChannel: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                channel_id: components["parameters"]["ChannelId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Canal. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["NotificationChannel"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
        };
    };
    deleteNotificationChannel: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                channel_id: components["parameters"]["ChannelId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            204: components["responses"]["NoContent"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    updateNotificationChannel: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                channel_id: components["parameters"]["ChannelId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["NotificationChannelPatch"];
                "application/merge-patch+json": components["schemas"]["NotificationChannelPatch"];
            };
        };
        responses: {
            /** @description Actualizado. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["NotificationChannel"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    testNotificationChannelConnection: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                channel_id: components["parameters"]["ChannelId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Resultado de la prueba (también cuando falla el destino; ver `ok`). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ConnectionTestResult"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    putNotificationChannelCredentials: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                channel_id: components["parameters"]["ChannelId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["NotificationChannelCredentials"];
            };
        };
        responses: {
            204: components["responses"]["NoContent"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    getLibreNmsIntegration: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                channel_id: components["parameters"]["ChannelId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Estado (status `not_provisioned` si aún no se sincronizó). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["LibreNmsIntegration"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    deleteLibreNmsRouterDevice: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                channel_id: components["parameters"]["ChannelId"];
                router_id: components["parameters"]["RouterId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            204: components["responses"]["NoContent"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    syncLibreNmsIntegration: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                channel_id: components["parameters"]["ChannelId"];
            };
            cookie?: never;
        };
        requestBody?: {
            content: {
                "application/json": components["schemas"]["LibreNmsSyncInput"];
            };
        };
        responses: {
            /** @description Resultado (también cuando falla LibreNMS; ver `ok`). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["LibreNmsSyncResult"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    testLibreNmsIntegration: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                channel_id: components["parameters"]["ChannelId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Resultado de la prueba (también cuando falla; ver `ok`). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["LibreNmsTestResult"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    testNotificationChannel: {
        parameters: {
            query?: never;
            header: {
                /** @description UUID generado por el cliente; obligatorio en acciones con efecto externo (falta → 428). */
                "Idempotency-Key": components["parameters"]["IdempotencyKeyRequired"];
            };
            path: {
                channel_id: components["parameters"]["ChannelId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Prueba encolada. */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["NotificationDelivery"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    validateTelegramBot: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    bot_token: string;
                };
            };
        };
        responses: {
            /** @description Bot válido. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["TelegramBotInfo"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    detectTelegramChats: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** @description Bot propio aún no guardado (no se guarda). */
                    bot_token?: string;
                    channel_id?: components["schemas"]["Uuid"];
                    /** @description Código de vinculación (bot de la instalación). */
                    code?: string;
                };
            };
        };
        responses: {
            /** @description Chats candidatos (vacío si nadie escribió todavía). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        /** @example @noc_isp_bot */
                        bot_username?: string | null;
                        data: components["schemas"]["TelegramChatCandidate"][];
                        /** @description true = bot de la instalación (filtrado por código). */
                        shared_bot: boolean;
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    createTelegramLinkCode: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Código generado. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["TelegramLinkCode"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    listNotificationDeliveries: {
        parameters: {
            query?: {
                channel_id?: components["schemas"]["Uuid"];
                channel_kind?: components["schemas"]["NotificationChannelKind"];
                /** @description Cursor opaco (`page.next_cursor`); ligado a filtros, orden y `tid`. Ajeno → `400 INVALID_CURSOR`. */
                cursor?: components["parameters"]["Cursor"];
                /** @description Tamaño de página (1–200, por defecto 50). Fuera de rango → 400. */
                limit?: components["parameters"]["Limit"];
                status?: "sent" | "failed" | "throttled";
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Entregas. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["NotificationDelivery"][];
                        page: components["schemas"]["PageInfo"];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
        };
    };
    listPermissions: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Catálogo (fuente: packages/schemas/permissions/v0/permissions.yaml). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["PermissionCatalog"];
                };
            };
            401: components["responses"]["Unauthorized"];
        };
    };
    platformListEvents: {
        parameters: {
            query?: {
                /** @description Cursor opaco (`page.next_cursor`); ligado a filtros, orden y `tid`. Ajeno → `400 INVALID_CURSOR`. */
                cursor?: components["parameters"]["Cursor"];
                /** @description Tipos separados por comas. */
                kind?: string;
                /** @description Tamaño de página (1–200, por defecto 50). Fuera de rango → 400. */
                limit?: components["parameters"]["Limit"];
                process?: string;
                role?: string;
                /** @description Severidad mínima. */
                severity?: "info" | "warn" | "error";
                since?: components["schemas"]["Timestamp"];
                until?: components["schemas"]["Timestamp"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Página de eventos. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["PlatformEvent"][];
                        page: components["schemas"]["PageInfo"];
                    };
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    platformListUnregisteredExporters: {
        parameters: {
            query?: {
                /** @description Cursor opaco (`page.next_cursor`); ligado a filtros, orden y `tid`. Ajeno → `400 INVALID_CURSOR`. */
                cursor?: components["parameters"]["Cursor"];
                /** @description Tamaño de página (1–200, por defecto 50). Fuera de rango → 400. */
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Exportadores no registrados. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: {
                            datagrams: components["schemas"]["Uint64String"];
                            exporter_ip: components["schemas"]["IpAddress"];
                            first_seen_at: components["schemas"]["Timestamp"];
                            last_seen_at: components["schemas"]["Timestamp"];
                        }[];
                        page: components["schemas"]["PageInfo"];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
        };
    };
    platformGetInstallation: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Configuración de acceso efectiva. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["InstallationAccess"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    platformOverview: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Resumen por tenant. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["TenantOverview"][];
                        generated_at: components["schemas"]["Timestamp"];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
        };
    };
    platformListRemoteDestinations: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Destinos (puede ser vacío → la UI muestra "Sin copia remota configurada"). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["RemoteDestination"][];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
        };
    };
    platformCreateRemoteDestination: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RemoteDestinationCreate"];
            };
        };
        responses: {
            /** @description Destino creado (deshabilitado hasta que tenga credenciales y huella del servidor confirmada). */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["RemoteDestination"];
                };
            };
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    platformDeleteRemoteDestination: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                destination_id: components["parameters"]["DestinationId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            204: components["responses"]["NoContent"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    platformUpdateRemoteDestination: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                destination_id: components["parameters"]["DestinationId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RemoteDestinationPatch"];
            };
        };
        responses: {
            /** @description Destino. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["RemoteDestination"];
                };
            };
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    platformPutRemoteDestinationCredentials: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                destination_id: components["parameters"]["DestinationId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    passphrase?: string | null;
                    password?: string;
                    /** @description Clave privada OpenSSH (PEM). */
                    private_key?: string;
                };
            };
        };
        responses: {
            /** @description Destino (has_credentials=true). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["RemoteDestination"];
                };
            };
            404: components["responses"]["NotFound"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    platformSyncRemoteDestination: {
        parameters: {
            query?: never;
            header: {
                /** @description UUID generado por el cliente; obligatorio en acciones con efecto externo (falta → 428). */
                "Idempotency-Key": components["parameters"]["IdempotencyKeyRequired"];
            };
            path: {
                destination_id: components["parameters"]["DestinationId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Copia encolada. */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["RemoteDestination"];
                };
            };
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    platformTestRemoteDestination: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                destination_id: components["parameters"]["DestinationId"];
            };
            cookie?: never;
        };
        requestBody?: {
            content: {
                "application/json": {
                    /** @description Huella SHA256:… que la persona confirma para fijarla. */
                    confirm_host_key?: string | null;
                };
            };
        };
        responses: {
            /** @description Resultado de la prueba (ok=false con error legible si falla; no es un 5xx). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["RemoteDestinationTestResult"];
                };
            };
            404: components["responses"]["NotFound"];
        };
    };
    platformListReputationSources: {
        parameters: {
            query?: {
                enabled?: boolean;
                origin?: components["schemas"]["ReputationSourceOrigin"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Fuentes (sin secretos). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["ReputationSource"][];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    platformCreateReputationSource: {
        parameters: {
            query?: never;
            header?: {
                /** @description Recomendado en `POST`. */
                "Idempotency-Key"?: components["parameters"]["IdempotencyKeyOptional"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ReputationSourceCreate"];
            };
        };
        responses: {
            /** @description Creada (`status=pending` hasta la primera carga). */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ReputationSource"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    platformGetReputationSource: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                source_id: components["parameters"]["ReputationSourceId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Fuente. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ReputationSource"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
        };
    };
    platformDeleteReputationSource: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                source_id: components["parameters"]["ReputationSourceId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            204: components["responses"]["NoContent"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    platformUpdateReputationSource: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                source_id: components["parameters"]["ReputationSourceId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ReputationSourcePatch"];
                "application/merge-patch+json": components["schemas"]["ReputationSourcePatch"];
            };
        };
        responses: {
            /** @description Actualizada. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ReputationSource"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    platformRefreshReputationSource: {
        parameters: {
            query?: never;
            header: {
                /** @description UUID generado por el cliente; obligatorio en acciones con efecto externo (falta → 428). */
                "Idempotency-Key": components["parameters"]["IdempotencyKeyRequired"];
            };
            path: {
                source_id: components["parameters"]["ReputationSourceId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Carga encolada. */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ReputationSource"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    platformListTenants: {
        parameters: {
            query?: {
                /** @description Cursor opaco (`page.next_cursor`); ligado a filtros, orden y `tid`. Ajeno → `400 INVALID_CURSOR`. */
                cursor?: components["parameters"]["Cursor"];
                /** @description Solo en colecciones administrativas pequeñas; ignorado en las grandes. */
                include_total?: components["parameters"]["IncludeTotal"];
                /** @description Tamaño de página (1–200, por defecto 50). Fuera de rango → 400. */
                limit?: components["parameters"]["Limit"];
                /** @description Búsqueda libre por prefijo/trigram sobre los campos documentados. Nunca IPs de clientes (D1). */
                q?: components["parameters"]["Q"];
                status?: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Página de tenants. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["Tenant"][];
                        page: components["schemas"]["PageInfo"];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    platformCreateTenant: {
        parameters: {
            query?: never;
            header: {
                /** @description UUID generado por el cliente; obligatorio en acciones con efecto externo (falta → 428). */
                "Idempotency-Key": components["parameters"]["IdempotencyKeyRequired"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["TenantCreate"];
            };
        };
        responses: {
            /**
             * @description Creado. `initial_admin_invitation` dice cómo quedó el primer administrador: si su cuenta ya
             *     existía (p. ej. el propio superadministrador) queda como miembro activo (`outcome=added`); si
             *     no, se invita (`outcome=invited`) con un enlace de un solo uso para compartir y, con SMTP,
             *     también por correo. El enlace solo aparece en esta respuesta.
             */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Tenant"] & {
                        initial_admin_invitation: components["schemas"]["InvitationIssued"];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    platformGetTenant: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                tenant_id: components["parameters"]["TenantId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Tenant. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Tenant"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
        };
    };
    platformUpdateTenant: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                tenant_id: components["parameters"]["TenantId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["TenantPatch"];
                "application/merge-patch+json": components["schemas"]["TenantPatch"];
            };
        };
        responses: {
            /** @description Actualizado. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Tenant"];
                };
            };
            401: components["responses"]["Unauthorized"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    platformResumeTenant: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                tenant_id: components["parameters"]["TenantId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Reactivado. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Tenant"];
                };
            };
            401: components["responses"]["Unauthorized"];
            409: components["responses"]["Conflict"];
        };
    };
    platformSuspendTenant: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                tenant_id: components["parameters"]["TenantId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** @default true */
                    pause_ingest?: boolean;
                    reason: string;
                };
            };
        };
        responses: {
            /** @description Suspendido. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Tenant"];
                };
            };
            401: components["responses"]["Unauthorized"];
            409: components["responses"]["Conflict"];
        };
    };
    platformGetUpdates: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Estado de la última comprobación. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["UpdateStatus"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    platformListUsers: {
        parameters: {
            query?: {
                /** @description Cursor opaco (`page.next_cursor`); ligado a filtros, orden y `tid`. Ajeno → `400 INVALID_CURSOR`. */
                cursor?: components["parameters"]["Cursor"];
                /** @description Tamaño de página (1–200, por defecto 50). Fuera de rango → 400. */
                limit?: components["parameters"]["Limit"];
                /** @description Búsqueda libre por prefijo/trigram sobre los campos documentados. Nunca IPs de clientes (D1). */
                q?: components["parameters"]["Q"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Página de usuarios. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["PlatformUser"][];
                        page: components["schemas"]["PageInfo"];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
        };
    };
    platformDeleteUser: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                user_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            204: components["responses"]["NoContent"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    platformUpdateUser: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                user_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["PlatformUserPatch"];
            };
        };
        responses: {
            /** @description Usuario. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["PlatformUser"];
                };
            };
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    platformResetUserPassword: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                user_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Enlace emitido. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["PasswordResetIssued"];
                };
            };
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    platformSetUserRoles: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                user_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    platform_roles: ("platform_admin" | "platform_operator" | "platform_auditor")[];
                };
            };
        };
        responses: {
            /** @description Usuario con sus roles nuevos. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["PlatformUser"];
                };
            };
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    platformListWireguardHubs: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Hubs. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["WireguardHub"][];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
        };
    };
    platformUpdateWireguardHub: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                hub_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** @description Host o IP pública que usan los routers para llegar al hub. */
                    endpoint?: string;
                    mtu?: number;
                    name?: string;
                    persistent_keepalive_seconds?: number;
                };
            };
        };
        responses: {
            /** @description Hub actualizado. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WireguardHub"];
                };
            };
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    platformAddWireguardPool: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                hub_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    cidr: components["schemas"]["Cidr"];
                };
            };
        };
        responses: {
            /** @description Rango añadido. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WireguardPool"];
                };
            };
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    platformDeleteWireguardPool: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                hub_id: components["schemas"]["Uuid"];
                pool_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            204: components["responses"]["NoContent"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    listPlaylists: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Playlists. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["playlist.schema"][];
                        page: components["schemas"]["PageInfo"];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
        };
    };
    createPlaylist: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["PlaylistInput"];
            };
        };
        responses: {
            /** @description Creada. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["playlist.schema"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    deletePlaylist: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                playlist_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            204: components["responses"]["NoContent"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    updatePlaylist: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                playlist_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["PlaylistInput"];
                "application/merge-patch+json": components["schemas"]["PlaylistInput"];
            };
        };
        responses: {
            /** @description Actualizada. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["playlist.schema"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    listReportDefinitions: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Definiciones. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["ReportDefinition"][];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    updateReportDefinition: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                definition_id: components["parameters"]["ReportDefinitionId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ReportDefinitionPatch"];
                "application/merge-patch+json": components["schemas"]["ReportDefinitionPatch"];
            };
        };
        responses: {
            /** @description Actualizada. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ReportDefinition"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    listReports: {
        parameters: {
            query?: {
                /** @description Cursor opaco (`page.next_cursor`); ligado a filtros, orden y `tid`. Ajeno → `400 INVALID_CURSOR`. */
                cursor?: components["parameters"]["Cursor"];
                kind?: components["schemas"]["ReportKind"];
                /** @description Tamaño de página (1–200, por defecto 50). Fuera de rango → 400. */
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Página de ejecuciones. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["ReportRun"][];
                        page: components["schemas"]["PageInfo"];
                    };
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    createReport: {
        parameters: {
            query?: never;
            header: {
                /** @description UUID generado por el cliente; obligatorio en acciones con efecto externo (falta → 428). */
                "Idempotency-Key": components["parameters"]["IdempotencyKeyRequired"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ReportCreate"];
            };
        };
        responses: {
            /** @description Encolado (o ya existente con la misma Idempotency-Key). */
            202: {
                headers: {
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ReportRun"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
            429: components["responses"]["TooManyRequests"];
        };
    };
    getReport: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                report_id: components["parameters"]["ReportId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Ejecución. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ReportRun"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    downloadReport: {
        parameters: {
            query?: {
                format?: components["schemas"]["ReportFormat"];
            };
            header?: never;
            path: {
                report_id: components["parameters"]["ReportId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Archivo. */
            200: {
                headers: {
                    /** @description `attachment; filename="horus-seguridad-2026-W41-0192f0c4.pdf"` */
                    "Content-Disposition"?: string;
                    /** @description SHA-256 del archivo (RFC 9530). */
                    "Repr-Digest"?: string;
                    [name: string]: unknown;
                };
                content: {
                    "application/pdf": string;
                    "application/zip": string;
                };
            };
            400: components["responses"]["BadRequest"];
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
        };
    };
    listAllowlist: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Entradas. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["AllowlistEntry"][];
                        page: components["schemas"]["PageInfo"];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
        };
    };
    createAllowlistEntry: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    asn?: number;
                    /** @description Vacío = todos. */
                    kinds?: string[];
                    prefix?: components["schemas"]["Cidr"];
                    reason: string;
                };
            };
        };
        responses: {
            /** @description Creada. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AllowlistEntry"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    deleteAllowlistEntry: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                entry_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            204: components["responses"]["NoContent"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
        };
    };
    listReputationSources: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Feeds. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: {
                            age_seconds?: number | null;
                            /** @enum {string} */
                            category: "botnet_cc" | "scanner" | "malware_dist" | "mining_pool" | "proxy_vpn" | "tor_exit" | "blocklist";
                            /** @description true en el catálogo base (D20) y en las personalizadas (términos confirmados al darlas de alta). */
                            commercial_use_allowed: boolean;
                            confidence?: number;
                            entries: number;
                            /** @example abuse_ch_feodo */
                            key: string;
                            last_success_at: components["schemas"]["Timestamp"] | null;
                            name: string;
                            /**
                             * @description D20: catálogo base o agregada por el superadministrador.
                             * @enum {string}
                             */
                            origin: "catalog" | "custom";
                        }[];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
        };
    };
    listRoles: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Roles. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["Role"][];
                        page: components["schemas"]["PageInfo"];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
        };
    };
    createRole: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RoleCreate"];
            };
        };
        responses: {
            /** @description Rol creado. */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Role"];
                };
            };
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    deleteRole: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                role_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            204: components["responses"]["NoContent"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    updateRole: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                role_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RolePatch"];
            };
        };
        responses: {
            /** @description Rol actualizado. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Role"];
                };
            };
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    listRouters: {
        parameters: {
            query?: {
                /** @description Cursor opaco (`page.next_cursor`); ligado a filtros, orden y `tid`. Ajeno → `400 INVALID_CURSOR`. */
                cursor?: components["parameters"]["Cursor"];
                /** @description Solo en colecciones administrativas pequeñas; ignorado en las grandes. */
                include_total?: components["parameters"]["IncludeTotal"];
                is_primary?: boolean;
                /** @description Tamaño de página (1–200, por defecto 50). Fuera de rango → 400. */
                limit?: components["parameters"]["Limit"];
                onboarding_state?: string;
                /** @description Búsqueda libre por prefijo/trigram sobre los campos documentados. Nunca IPs de clientes (D1). */
                q?: components["parameters"]["Q"];
                /** @description Filtro por nodo (multivalor por comas). */
                site_id?: components["parameters"]["SiteIdFilter"];
                sort?: "name" | "-name" | "created_at" | "-created_at";
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Página de routers. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["Router"][];
                        page: components["schemas"]["PageInfo"];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    createRouter: {
        parameters: {
            query?: never;
            header?: {
                /** @description Recomendado en `POST`. */
                "Idempotency-Key"?: components["parameters"]["IdempotencyKeyOptional"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RouterInput"];
            };
        };
        responses: {
            /** @description Creado. */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Router"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    getRouter: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                router_id: components["parameters"]["RouterId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Router. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Router"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
        };
    };
    deleteRouter: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                router_id: components["parameters"]["RouterId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            204: components["responses"]["NoContent"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    updateRouter: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                router_id: components["parameters"]["RouterId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RouterPatch"];
                "application/merge-patch+json": components["schemas"]["RouterPatch"];
            };
        };
        responses: {
            /** @description Actualizado. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Router"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    putRouterCredential: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                kind: "snmp" | "routeros_api";
                router_id: components["parameters"]["RouterId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    auth_password: string;
                    /** @enum {string} */
                    auth_protocol: "sha1" | "sha256";
                    priv_password: string;
                    /** @enum {string} */
                    priv_protocol: "aes128";
                    username: string;
                } | {
                    password: string;
                    /** @description Huella fijada en el primer contacto. */
                    tls_fingerprint_sha256?: string | null;
                    /** @example horus-ro */
                    username: string;
                };
            };
        };
        responses: {
            /** @description Estado de la credencial (sin secretos). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CredentialStatus"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    createDeprovisioningScript: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                router_id: components["parameters"]["RouterId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Script (`text/plain`, `no-store`). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "text/plain": string;
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    listRouterInterfaces: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                router_id: components["parameters"]["RouterId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Interfaces. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["RouterInterface"][];
                        discovered_at: components["schemas"]["Timestamp"] | null;
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    getRouterLiveMetrics: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                router_id: components["parameters"]["RouterId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Última muestra (los campos son null hasta el primer sondeo con éxito). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["RouterLiveMetrics"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
        };
    };
    pollRouterNow: {
        parameters: {
            query?: never;
            header: {
                /** @description UUID generado por el cliente; obligatorio en acciones con efecto externo (falta → 428). */
                "Idempotency-Key": components["parameters"]["IdempotencyKeyRequired"];
            };
            path: {
                router_id: components["parameters"]["RouterId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Encolado. */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        queued: boolean;
                        /** @description Si no se encola (sin túnel, sin credenciales SNMP, sondeador inactivo), por qué. */
                        reason?: string | null;
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            428: components["responses"]["PreconditionRequired"];
            429: components["responses"]["TooManyRequests"];
        };
    };
    previewRouterPrefixImport: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                router_id: components["parameters"]["RouterId"];
            };
            cookie?: never;
        };
        requestBody?: {
            content: {
                "application/json": {
                    /** @description Huella nueva que el admin confirma. */
                    accept_new_tls_fingerprint?: string | null;
                };
            };
        };
        responses: {
            /** @description Propuesta. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["PrefixImportPreview"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            /** @description Router inalcanzable por el túnel. */
            502: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/problem+json": components["schemas"]["Problem"];
                };
            };
        };
    };
    createProvisioningScript: {
        parameters: {
            query?: never;
            header: {
                /** @description UUID generado por el cliente; obligatorio en acciones con efecto externo (falta → 428). */
                "Idempotency-Key": components["parameters"]["IdempotencyKeyRequired"];
            };
            path: {
                router_id: components["parameters"]["RouterId"];
            };
            cookie?: never;
        };
        requestBody?: {
            content: {
                "application/json": {
                    /** @example pool.ntp.org */
                    ntp_server?: string | null;
                    routeros_version?: string | null;
                };
            };
        };
        responses: {
            /** @description Script (`text/plain`, `Cache-Control no-store`). Cabeceras con el token creado. */
            201: {
                headers: {
                    "X-Horus-Enrollment-Token-Expires-At"?: string;
                    /** @description ID del token de enrolamiento (para revocarlo). */
                    "X-Horus-Enrollment-Token-Id"?: string;
                    [name: string]: unknown;
                };
                content: {
                    "text/plain": string;
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    listRouterStatus: {
        parameters: {
            query?: {
                /** @description Filtro por nodo (multivalor por comas). */
                site_id?: components["parameters"]["SiteIdFilter"];
                /** @description Filtra por estado efectivo (lista separada por comas). */
                status?: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Routers con su estado. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["RouterStatus"][];
                        meta: {
                            generated_at: components["schemas"]["Timestamp"];
                            poller: components["schemas"]["PollerHealth"];
                        };
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    getRouterThresholds: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Umbrales vigentes. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["RouterThresholds"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    putRouterThresholds: {
        parameters: {
            query?: never;
            header?: {
                "If-Match"?: string;
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RouterThresholdValues"];
            };
        };
        responses: {
            /** @description Umbrales guardados. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["RouterThresholds"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["ServiceUnavailable"];
        };
    };
    resetRouterThresholds: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Umbrales de la instalación (customized=false). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["RouterThresholds"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            503: components["responses"]["ServiceUnavailable"];
        };
    };
    getSecuritySummary: {
        parameters: {
            query?: {
                /** @description Atajo relativo; el servidor lo resuelve y lo devuelve en `meta`. */
                range?: components["parameters"]["Range"];
                /** @description Filtro por nodo (multivalor por comas). */
                site_id?: components["parameters"]["SiteIdFilter"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Resumen. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["SecuritySummary"];
                };
            };
            401: components["responses"]["Unauthorized"];
        };
    };
    listSites: {
        parameters: {
            query?: {
                /** @description Cursor opaco (`page.next_cursor`); ligado a filtros, orden y `tid`. Ajeno → `400 INVALID_CURSOR`. */
                cursor?: components["parameters"]["Cursor"];
                /** @description Solo en colecciones administrativas pequeñas; ignorado en las grandes. */
                include_total?: components["parameters"]["IncludeTotal"];
                kind?: string;
                /** @description Tamaño de página (1–200, por defecto 50). Fuera de rango → 400. */
                limit?: components["parameters"]["Limit"];
                parent_id?: components["schemas"]["Uuid"];
                /** @description Búsqueda libre por prefijo/trigram sobre los campos documentados. Nunca IPs de clientes (D1). */
                q?: components["parameters"]["Q"];
                sort?: "name" | "-name" | "created_at" | "-created_at";
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Página de nodos. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["Site"][];
                        page: components["schemas"]["PageInfo"];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    createSite: {
        parameters: {
            query?: never;
            header?: {
                /** @description Recomendado en `POST`. */
                "Idempotency-Key"?: components["parameters"]["IdempotencyKeyOptional"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["SiteInput"];
            };
        };
        responses: {
            /** @description Creado. */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Site"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    getSite: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                site_id: components["parameters"]["SiteId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Nodo. */
            200: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Site"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
        };
    };
    deleteSite: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                site_id: components["parameters"]["SiteId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            204: components["responses"]["NoContent"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            412: components["responses"]["PreconditionFailed"];
        };
    };
    updateSite: {
        parameters: {
            query?: never;
            header: {
                /** @description ETag (`"<version>"`) del recurso; falta → 428, no coincide → 412 con `current`. */
                "If-Match": components["parameters"]["IfMatch"];
            };
            path: {
                site_id: components["parameters"]["SiteId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["SiteInput"];
                "application/merge-patch+json": components["schemas"]["SiteInput"];
            };
        };
        responses: {
            /** @description Actualizado. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Site"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            412: components["responses"]["PreconditionFailed"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    listClientPrefixes: {
        parameters: {
            query?: {
                /** @description Cursor opaco (`page.next_cursor`); ligado a filtros, orden y `tid`. Ajeno → `400 INVALID_CURSOR`. */
                cursor?: components["parameters"]["Cursor"];
                /** @description Tamaño de página (1–200, por defecto 50). Fuera de rango → 400. */
                limit?: components["parameters"]["Limit"];
                role?: string;
            };
            header?: never;
            path: {
                site_id: components["parameters"]["SiteId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Página de prefijos. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["ClientPrefix"][];
                        page: components["schemas"]["PageInfo"];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
        };
    };
    createClientPrefix: {
        parameters: {
            query?: never;
            header?: {
                /** @description Recomendado en `POST`. */
                "Idempotency-Key"?: components["parameters"]["IdempotencyKeyOptional"];
            };
            path: {
                site_id: components["parameters"]["SiteId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ClientPrefixInput"];
            };
        };
        responses: {
            /** @description Creado. */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ClientPrefix"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    batchCreateClientPrefixes: {
        parameters: {
            query?: never;
            header: {
                /** @description UUID generado por el cliente; obligatorio en acciones con efecto externo (falta → 428). */
                "Idempotency-Key": components["parameters"]["IdempotencyKeyRequired"];
            };
            path: {
                site_id: components["parameters"]["SiteId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    items: components["schemas"]["ClientPrefixInput"][];
                };
            };
        };
        responses: {
            /** @description Prefijos creados. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["ClientPrefix"][];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            409: components["responses"]["Conflict"];
            422: components["responses"]["UnprocessableEntity"];
            428: components["responses"]["PreconditionRequired"];
        };
    };
    listPrefixProposals: {
        parameters: {
            query?: {
                /** @description Atajo relativo; el servidor lo resuelve y lo devuelve en `meta`. */
                range?: components["parameters"]["Range"];
            };
            header?: never;
            path: {
                site_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Propuestas (se confirman con POST /sites/{site_id}/client-prefixes/batch). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: {
                            bytes: components["schemas"]["Uint64String"];
                            distinct_ips: number;
                            prefix: components["schemas"]["Cidr"];
                            /** @enum {string} */
                            reason?: "private_or_cgnat" | "isp_public_asn";
                            /** @enum {string} */
                            suggested_role: "customers" | "infrastructure" | "excluded";
                        }[];
                        meta: components["schemas"]["AnalyticsMeta"];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
            503: components["responses"]["ServiceUnavailable"];
        };
    };
    queryUnattributedAddresses: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                site_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** @default 50 */
                    n?: number;
                    /** @description Rango propuesto (IPv4 /24 o más amplio; IPv6 /48 o más amplio). */
                    prefix: components["schemas"]["Cidr"];
                    range?: components["schemas"]["RelativeRange"];
                };
            };
        };
        responses: {
            /** @description IPs del rango ordenadas por bytes. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: {
                            /**
                             * Format: ip
                             * @example 100.64.12.17
                             */
                            address: string;
                            bytes: components["schemas"]["Uint64String"];
                            last_seen_at: components["schemas"]["Timestamp"];
                            packets: components["schemas"]["Uint64String"];
                        }[];
                        meta: components["schemas"]["AnalyticsMeta"];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            404: components["responses"]["NotFound"];
            422: components["responses"]["UnprocessableEntity"];
            503: components["responses"]["ServiceUnavailable"];
        };
    };
    getSystemStatus: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Estado. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["SystemStatus"];
                };
            };
            401: components["responses"]["Unauthorized"];
        };
    };
    previewWidgetData: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    config: Record<string, unknown>;
                    range?: components["schemas"]["RelativeRange"];
                    type: string;
                };
            };
        };
        responses: {
            /** @description Datos. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["widget-data.schema"];
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
            422: components["responses"]["UnprocessableEntity"];
        };
    };
    listWidgetTypes: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Catálogo (fuente packages/schemas/dashboard/v0/widget-types.json). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["widget-type.schema"][];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
        };
    };
    revokeEnrollmentToken: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                token_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            204: components["responses"]["NoContent"];
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
        };
    };
    listWireguardPeers: {
        parameters: {
            query?: {
                /** @description Cursor opaco (`page.next_cursor`); ligado a filtros, orden y `tid`. Ajeno → `400 INVALID_CURSOR`. */
                cursor?: components["parameters"]["Cursor"];
                handshake_state?: string;
                /** @description Tamaño de página (1–200, por defecto 50). Fuera de rango → 400. */
                limit?: components["parameters"]["Limit"];
                router_id?: string;
                sort?: "last_handshake_at" | "-last_handshake_at";
                status?: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Página de peers. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        data: components["schemas"]["Peer"][];
                        page: components["schemas"]["PageInfo"];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            403: components["responses"]["Forbidden"];
        };
    };
    getWireguardPeer: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                peer_id: components["schemas"]["Uuid"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Peer. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Peer"];
                };
            };
            401: components["responses"]["Unauthorized"];
            404: components["responses"]["NotFound"];
        };
    };
    createWsTicket: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Ticket emitido. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        expires_at: components["schemas"]["Timestamp"];
                        /** @description 256 bits base64url, un uso. */
                        ticket: string;
                        /**
                         * @description URL absoluta del WebSocket construida con HORUS_PUBLIC_BASE_URL (D14).
                         * @example wss://horus.example.net/api/v1/ws
                         */
                        url: string;
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
            503: components["responses"]["ServiceUnavailable"];
        };
    };
}
