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
        /** Nombre, idioma, zona horaria, tenant por defecto */
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
         */
        put: operations["putNotificationChannelCredentials"];
        post?: never;
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
        /** Destinos remotos de copias (lectura en I1; cero destinos es válido; escritura en I3) */
        get: operations["platformListRemoteDestinations"];
        put?: never;
        post?: never;
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
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
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
        /** Declara un prefijo (solape en el realm → 409 CLIENT_PREFIX_OVERLAP) */
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
        /** Confirma una selección (importación del MikroTik o propuestas del modo descubrimiento), todo o nada */
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
        /** @enum {string} */
        CapabilityState: "ok" | "degraded" | "stale" | "unavailable";
        ChannelSubscription: {
            event_types: components["schemas"]["NotificationEventType"][];
            min_severity?: components["schemas"]["Severity"];
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
            channel_id: components["schemas"]["Uuid"];
            channel_kind: components["schemas"]["NotificationChannelKind"];
            checked_at: components["schemas"]["Timestamp"];
            /** @description Sin secretos ni cuerpo de la respuesta remota. */
            error?: string | null;
            /** @description Sin valor si `ok`. Enum abierto. */
            error_code?: ("dns_failed" | "connect_failed" | "tls_invalid" | "auth_failed" | "forbidden" | "timeout" | "unexpected_response" | "missing_credentials") | null;
            latency_ms?: number | null;
            ok: boolean;
            /** @description librenms: versión que informa la API, si la expone. */
            remote_version?: string | null;
        };
        CredentialStatus: {
            configured: boolean;
            /** @enum {string} */
            kind: "snmp" | "routeros_api";
            /** @enum {string|null} */
            last_result?: "ok" | "auth_failed" | "unreachable" | "tls_fingerprint_changed" | null;
            last_used_at?: components["schemas"]["Timestamp"] | null;
            updated_at?: components["schemas"]["Timestamp"] | null;
        };
        Customer: {
            /** @description IP canónica (IPv6 truncada al prefijo del cliente). Dato personal. */
            address: components["schemas"]["IpAddress"];
            /** @description Dato personal. */
            alias: string | null;
            /** @enum {string|null} */
            alias_source: "manual" | "routeros_ppp" | null;
            client_prefix_id: components["schemas"]["Uuid"] | null;
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
            suggested_confidence?: number | null;
            suggested_kind?: components["schemas"]["CustomerKind"] | null;
            suggested_reasons?: components["schemas"]["Reason"][];
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
        ErrorCode: ("VALIDATION_FAILED" | "UNAUTHENTICATED" | "TOKEN_EXPIRED" | "SESSION_REVOKED" | "PERMISSION_DENIED" | "ORIGIN_NOT_ALLOWED" | "MFA_REQUIRED" | "MFA_ENROLLMENT_REQUIRED" | "REAUTH_REQUIRED" | "NOT_FOUND" | "ALREADY_EXISTS" | "CONFLICT" | "PRECONDITION_FAILED" | "PRECONDITION_REQUIRED" | "IDEMPOTENCY_KEY_REUSED" | "IDEMPOTENCY_IN_PROGRESS" | "RATE_LIMITED" | "TENANT_RATE_LIMITED" | "INTERNAL" | "SERVICE_UNAVAILABLE" | "ANALYTICS_UNAVAILABLE" | "TIMEOUT" | "INVALID_CREDENTIALS" | "INVALID_CURSOR" | "INVALID_FILTER" | "INVALID_SORT_FIELD" | "TIME_RANGE_TOO_LARGE" | "TENANT_NOT_FOUND" | "TENANT_MISMATCH" | "TENANT_SUSPENDED" | "TOKEN_SCOPE_INVALID" | "KIOSK_FORBIDDEN" | "ROUTER_NOT_FOUND" | "ROUTER_PRIMARY_EXISTS" | "ROUTEROS_VERSION_UNSUPPORTED" | "ROUTER_UNREACHABLE" | "ROUTER_TLS_FINGERPRINT_CHANGED" | "SITE_NOT_FOUND" | "SITE_NOT_EMPTY" | "PEER_ALREADY_REVOKED" | "WIREGUARD_IP_POOL_EXHAUSTED" | "WIREGUARD_PUBLIC_KEY_IN_USE" | "ENROLLMENT_TOKEN_INVALID" | "CUSTOMER_NOT_FOUND" | "CUSTOMER_KIND_LOCKED" | "CLIENT_PREFIX_OVERLAP" | "CLIENT_PREFIX_NOT_FOUND" | "FINDING_NOT_FOUND" | "FINDING_STATE_INVALID" | "DASHBOARD_NOT_FOUND" | "DASHBOARD_READ_ONLY" | "WIDGET_TYPE_NOT_ALLOWED" | "WIDGET_TYPE_UNKNOWN" | "WIDGET_CONFIG_INVALID" | "KIOSK_ENROLLMENT_CODE_INVALID" | "NOTIFICATION_CHANNEL_NOT_FOUND" | "NOTIFICATION_CHANNEL_KIND_NOT_AVAILABLE" | "NOTIFICATION_CHANNEL_UNREACHABLE" | "NOTIFICATION_CHANNEL_AUTH_FAILED" | "NOTIFICATION_CHANNEL_CREDENTIALS_MISSING" | "STORAGE_TARGET_UNREACHABLE" | "REPUTATION_SOURCE_NOT_FOUND" | "REPUTATION_SOURCE_READ_ONLY" | "REPUTATION_SOURCE_URL_NOT_ALLOWED") | string;
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
                code: ("ip_only_access" | "self_signed_certificate" | "certificate_expiring" | "acme_renewal_failed") | string;
                message: string;
                /** @enum {string} */
                severity: "info" | "warning" | "critical";
            }[];
            /** @description HORUS_WG_ENDPOINT (nombre o IP). */
            wireguard_endpoint: string;
        };
        /**
         * @description IPv4 o IPv6. **Dato personal** cuando es la IP de un cliente (nunca en URLs).
         * @example 100.64.12.34
         */
        IpAddress: string;
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
        };
        NotificationChannelInput: {
            config: components["schemas"]["EmailChannelConfig"] | components["schemas"]["TelegramChannelConfig"] | components["schemas"]["LibreNmsChannelConfig"];
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
         * @description `email`, `telegram` y `librenms` (D17, por API) disponibles. Enum abierto (después: webhook, whatsapp,
         *     sms); un `kind` conocido por el contrato pero aún no implementado → `422 NOTIFICATION_CHANNEL_KIND_NOT_AVAILABLE`.
         * @enum {string}
         */
        NotificationChannelKind: "email" | "telegram" | "librenms";
        NotificationChannelPatch: {
            config?: components["schemas"]["EmailChannelConfig"] | components["schemas"]["TelegramChannelConfig"] | components["schemas"]["LibreNmsChannelConfig"];
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
            sent_at?: components["schemas"]["Timestamp"] | null;
            source_event_id: components["schemas"]["Uuid"] | null;
            /** @example horus.detection.finding.opened */
            source_event_type: string | null;
            /** @enum {string} */
            status: "queued" | "sent" | "failed" | "throttled";
            tenant_id: components["schemas"]["Uuid"];
        };
        /**
         * @description Tipos de evento a los que se suscribe un canal en el I1 (enum abierto).
         * @enum {string}
         */
        NotificationEventType: "finding_opened" | "finding_reopened" | "exporter_silent" | "exporter_recovered" | "tunnel_down" | "tunnel_recovered";
        PageInfo: {
            has_more: boolean;
            limit: number;
            next_cursor: string | null;
            prev_cursor: string | null;
            /** @description Solo con `include_total=true` en colecciones administrativas. */
            total?: number | null;
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
            id: components["schemas"]["Uuid"];
            is_system: boolean;
            key: string;
            name: string;
            permissions: string[];
            requires_mfa?: boolean;
            tenant_id?: components["schemas"]["Uuid"] | null;
        };
        RoleAssignment: {
            role_id: components["schemas"]["Uuid"];
            /** @example noc */
            role_key?: string;
            scope: string;
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
        /** TelegramChannelConfig */
        TelegramChannelConfig: {
            chat_id: string;
            /**
             * @default es
             * @enum {string}
             */
            language?: "es" | "en";
            /** @default false */
            readonly uses_own_bot?: boolean;
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
         * @enum {string}
         */
        TlsMode: "acme" | "acme_ip" | "self_signed" | "provided";
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
            name: string;
            peers_active?: number;
            public_key: string;
            services_cidr?: components["schemas"]["Cidr"];
            /** @enum {string} */
            status: "up" | "down" | "degraded";
            tenants?: number;
            tunnel_cidr: components["schemas"]["Cidr"];
        };
    };
    responses: {
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
        ChannelId: components["schemas"]["Uuid"];
        ClientPrefixId: components["schemas"]["Uuid"];
        /** @description Cursor opaco (`page.next_cursor`); ligado a filtros, orden y `tid`. Ajeno → `400 INVALID_CURSOR`. */
        Cursor: string;
        CustomerId: components["schemas"]["Uuid"];
        DashboardId: components["schemas"]["Uuid"];
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
        KioskId: components["schemas"]["Uuid"];
        /** @description Tamaño de página (1–200, por defecto 50). Fuera de rango → 400. */
        Limit: number;
        /** @description Búsqueda libre por prefijo/trigram sobre los campos documentados. Nunca IPs de clientes (D1). */
        Q: string;
        /** @description Atajo relativo; el servidor lo resuelve y lo devuelve en `meta`. */
        Range: components["schemas"]["RelativeRange"];
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
            /** @description Invitación aceptada para envío. */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
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
                        data: {
                            id: components["schemas"]["Uuid"];
                            /** @enum {string} */
                            kind: "sftp" | "gdrive" | "dropbox" | "mega";
                            lag_seconds?: number | null;
                            last_success_at?: components["schemas"]["Timestamp"] | null;
                            name: string;
                            /** @enum {string} */
                            status: "ok" | "lagging" | "failing" | "disabled";
                        }[];
                    };
                };
            };
            401: components["responses"]["Unauthorized"];
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
            /** @description Creado. */
            201: {
                headers: {
                    ETag: components["headers"]["ETag"];
                    Location: components["headers"]["Location"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Tenant"];
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
