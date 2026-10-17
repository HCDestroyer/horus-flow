//go:build integration

package tenancy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/archtest"
	"github.com/hcdestroyer/horus-flow/packages/go/tenanttest"
)

// cases declara el caso de aislamiento de CADA operación `x-scope: tenant`
// del contrato (packages/schemas/openapi/dist/horus-api.v0.yaml). Una
// operación nueva sin caso hace fallar TestTenantIsolationMatrix (I0-08
// criterio 3). Quien implemente un endpoint Pending debe sustituirlo por su
// caso real: la suite falla si un Pending está montado.
func cases() map[string]tenanttest.Case {
	c := map[string]tenanttest.Case{
		// devices (I0-09, CORE)
		"listSites":          {Kind: tenanttest.List},
		"createSite":         {Kind: tenanttest.Create, Body: map[string]any{"name": "Nodo fantasma"}},
		"getSite":            {Kind: tenanttest.ByID},
		"updateSite":         {Kind: tenanttest.ByID, Body: map[string]any{"name": "pirata"}},
		"deleteSite":         {Kind: tenanttest.ByID},
		"listRouters":        {Kind: tenanttest.List},
		"createRouter":       {Kind: tenanttest.Create, Body: map[string]any{"name": "rt-pirata", "is_primary": false}},
		"getRouter":          {Kind: tenanttest.ByID},
		"updateRouter":       {Kind: tenanttest.ByID, Body: map[string]any{"name": "pirata"}},
		"deleteRouter":       {Kind: tenanttest.ByID},
		"listClientPrefixes": {Kind: tenanttest.List},
		"createClientPrefix": {Kind: tenanttest.Create, Body: map[string]any{"prefix": "10.99.0.0/24", "role": "customers"}},
		"updateClientPrefix": {Kind: tenanttest.ByID, Body: map[string]any{"role": "excluded"}},
		"deleteClientPrefix": {Kind: tenanttest.ByID},
		// wireguard (I1-01, I1-02, CORE)
		"listWireguardPeers":         {Kind: tenanttest.List},
		"getWireguardPeer":           {Kind: tenanttest.ByID},
		"createProvisioningScript":   {Kind: tenanttest.ByID, Body: map[string]any{"routeros_version": "7.16"}},
		"createDeprovisioningScript": {Kind: tenanttest.ByID},
		"revokeEnrollmentToken":      {Kind: tenanttest.ByID},
		// devices: importación del MikroTik (I1-28, CORE)
		"previewRouterPrefixImport": {Kind: tenanttest.ByID},
		// devices: huella TLS del router confirmada por un administrador
		"getRouterTlsFingerprint":     {Kind: tenanttest.ByID},
		"captureRouterTlsFingerprint": {Kind: tenanttest.ByID},
		"confirmRouterTlsFingerprint": {Kind: tenanttest.ByID, Body: map[string]any{
			"fingerprint_sha256": "AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA"}},
		// devices: alta directa de Huawei VRP (sin túnel; docs/vendors/huawei.md §5)
		"createDirectOnboarding":    {Kind: tenanttest.ByID, Body: map[string]any{"management_address": "198.51.100.20"}},
		"batchCreateClientPrefixes": {Kind: tenanttest.Create, Body: map[string]any{"items": []any{map[string]any{"prefix": "10.98.0.0/24", "role": "customers"}}}},
	}
	pending := func(owner string, ids ...string) {
		for _, id := range ids {
			c[id] = tenanttest.Case{Kind: tenanttest.Pending, Owner: owner}
		}
	}
	// devices: clientes (I1-06, CORE)
	c["listCustomers"] = tenanttest.Case{Kind: tenanttest.List}
	c["getCustomerStats"] = tenanttest.Case{Kind: tenanttest.List}
	c["getCustomer"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["listCustomerKindHistory"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["updateCustomer"] = tenanttest.Case{Kind: tenanttest.ByID, Body: map[string]any{"alias": "pirata"}}
	c["lookupCustomers"] = tenanttest.Case{Kind: tenanttest.Create, Body: map[string]any{"address": "10.10.0.41"}}
	c["resetCustomer"] = tenanttest.Case{Kind: tenanttest.ByID, Body: map[string]any{"reason": "intento de otro ISP"}}
	c["setCustomerKind"] = tenanttest.Case{Kind: tenanttest.ByID, Body: map[string]any{"kind": "commercial", "reason": "intento de otro ISP"}}
	c["unlockCustomerKind"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["confirmClientPrefix"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["putRouterCredential"] = tenanttest.Case{Kind: tenanttest.ByID, Body: map[string]any{
		"username": "horus-ro", "auth_protocol": "sha256", "auth_password": "clave-auth-pirata", "priv_protocol": "aes128", "priv_password": "clave-priv-pirata"}}
	// auth: kioscos (I1-14, CORE)
	c["listKiosks"] = tenanttest.Case{Kind: tenanttest.List}
	c["createKiosk"] = tenanttest.Case{Kind: tenanttest.Create, Body: map[string]any{"name": "TV pirata"}}
	c["getKiosk"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["updateKiosk"] = tenanttest.Case{Kind: tenanttest.ByID, Body: map[string]any{"name": "pirata"}}
	c["createKioskEnrollmentCode"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["revokeKiosk"] = tenanttest.Case{Kind: tenanttest.ByID, Body: map[string]any{"reason": "pirata"}}
	// auth: miembros y roles (I1, CORE)
	c["listMembers"] = tenanttest.Case{Kind: tenanttest.List}
	c["listRoles"] = tenanttest.Case{Kind: tenanttest.List}
	c["inviteMember"] = tenanttest.Case{Kind: tenanttest.Create, Body: map[string]any{"email": "pirata@isp.test",
		"role_assignments": []any{map[string]any{"role_id": "0192f000-0000-7000-8000-00000000a001", "scope": "tenant"}}}}
	c["removeMember"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["updateMember"] = tenanttest.Case{Kind: tenanttest.ByID, Body: map[string]any{"display_name": "pirata"}}
	c["resetMemberPassword"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["reissueMemberInvitation"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["replaceMemberRoleAssignments"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["createRole"] = tenanttest.Case{Kind: tenanttest.Create, Body: map[string]any{"key": "pirata", "name": "Pirata", "permissions": []string{"devices.read"}}}
	c["updateRole"] = tenanttest.Case{Kind: tenanttest.ByID, Body: map[string]any{"name": "pirata"}}
	c["deleteRole"] = tenanttest.Case{Kind: tenanttest.ByID}
	// alerts: canal mínimo (D13/D17/D21, CORE)
	c["listNotificationChannels"] = tenanttest.Case{Kind: tenanttest.List}
	c["createNotificationChannel"] = tenanttest.Case{Kind: tenanttest.Create, Body: map[string]any{"name": "pirata", "kind": "telegram",
		"config": map[string]any{"chat_id": "12345"}, "subscription": map[string]any{"event_types": []string{"finding_opened"}}}}
	c["getNotificationChannel"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["updateNotificationChannel"] = tenanttest.Case{Kind: tenanttest.ByID, Body: map[string]any{"name": "pirata"}}
	c["deleteNotificationChannel"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["putNotificationChannelCredentials"] = tenanttest.Case{Kind: tenanttest.ByID, Body: map[string]any{"telegram_bot_token": "123456:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef0123"}}
	c["testNotificationChannelConnection"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["testNotificationChannel"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["listNotificationDeliveries"] = tenanttest.Case{Kind: tenanttest.List}
	// analytics: dashboards, playlists y kiosco (I1-15, CORE)
	widget := map[string]any{"id": "w-pirata", "type": "traffic_now", "title": "x", "position": map[string]any{"x": 0, "y": 0, "w": 2, "h": 2}, "config": map[string]any{}}
	layout := map[string]any{"grid": "12-col", "columns": 12, "row_height_px": 80}
	c["listDashboards"] = tenanttest.Case{Kind: tenanttest.List}
	c["getDashboard"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["createDashboard"] = tenanttest.Case{Kind: tenanttest.Create, Body: map[string]any{"name": "pirata", "layout": layout, "widgets": []any{}}}
	c["updateDashboard"] = tenanttest.Case{Kind: tenanttest.ByID, Body: map[string]any{"name": "pirata"}}
	c["deleteDashboard"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["duplicateDashboard"] = tenanttest.Case{Kind: tenanttest.ByID, Body: map[string]any{"name": "pirata"}}
	c["replaceDashboardLayout"] = tenanttest.Case{Kind: tenanttest.ByID, Body: map[string]any{"layout": layout, "positions": map[string]any{}}}
	c["addDashboardWidget"] = tenanttest.Case{Kind: tenanttest.ByID, Body: widget}
	c["updateDashboardWidget"] = tenanttest.Case{Kind: tenanttest.ByID, Body: map[string]any{"title": "pirata"}}
	c["deleteDashboardWidget"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["getWidgetData"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["previewWidgetData"] = tenanttest.Case{Kind: tenanttest.Create, Body: map[string]any{"type": "noc_header", "config": map[string]any{}}}
	c["listPlaylists"] = tenanttest.Case{Kind: tenanttest.List}
	c["createPlaylist"] = tenanttest.Case{Kind: tenanttest.Create, Body: map[string]any{"name": "pirata", "items": []any{map[string]any{"dashboard_id": "0192f000-0000-7000-8000-00000000d001", "duration_seconds": 30}}}}
	c["updatePlaylist"] = tenanttest.Case{Kind: tenanttest.ByID, Body: map[string]any{"name": "pirata"}}
	c["deletePlaylist"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["getKioskConfig"] = tenanttest.Case{Kind: tenanttest.KioskOnly}
	pending("FLOW I1 (analytics: tráfico y propuestas; su suite usa ClickHouse)",
		"getCustomerTraffic", "getTrafficAttribution", "getTrafficTimeseries", "getTrafficTop", "listPrefixProposals",
		"queryUnattributedAddresses")
	// detection (I1-12, SEC)
	c["listFindings"] = tenanttest.Case{Kind: tenanttest.List}
	c["listCustomerFindings"] = tenanttest.Case{Kind: tenanttest.List}
	// detection: scoring residencial/comercial (I2). Sin scoring del cliente
	// devuelve latest = null sin eco del id: no revela si existe en otro ISP.
	c["getCustomerScoring"] = tenanttest.Case{Kind: tenanttest.List}
	c["getFinding"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["getFindingEvidence"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["acknowledgeFinding"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["resolveFinding"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["markFindingFalsePositive"] = tenanttest.Case{Kind: tenanttest.ByID, Body: map[string]any{"comment": "intento de otro ISP"}}
	c["bulkResolveFindings"] = tenanttest.Case{Kind: tenanttest.Create, Body: map[string]any{"kind": "outbound_scanning", "comment": "intento de otro ISP"}}
	c["getSecuritySummary"] = tenanttest.Case{Kind: tenanttest.List}
	c["listAllowlist"] = tenanttest.Case{Kind: tenanttest.List}
	c["createAllowlistEntry"] = tenanttest.Case{Kind: tenanttest.Create, Body: map[string]any{"prefix": "198.51.100.0/24", "reason": "pirata"}}
	c["deleteAllowlistEntry"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["listReputationSources"] = tenanttest.Case{Kind: tenanttest.List}
	c["getDetectionConfig"] = tenanttest.Case{Kind: tenanttest.List}
	// devices: ASN propios (Administración › ASN propios)
	c["listAsnRules"] = tenanttest.Case{Kind: tenanttest.List}
	c["getAsnRuleCatalog"] = tenanttest.Case{Kind: tenanttest.List}
	c["createAsnRule"] = tenanttest.Case{Kind: tenanttest.Create, Body: map[string]any{"asn": 64999, "name": "pirata", "category": "web"}}
	c["updateAsnRule"] = tenanttest.Case{Kind: tenanttest.ByID, Body: map[string]any{"name": "pirata"}}
	c["deleteAsnRule"] = tenanttest.Case{Kind: tenanttest.ByID}
	pending("FLOW I1 (analytics: nombre de un ASN; el arnés solo monta los dashboards de analytics)", "getAsnInfo")
	// assist: el arnés no monta el asistente (necesita el proceso completo para sus herramientas);
	// su aislamiento lo cubren services/assist/internal/adapters/postgres (RLS forzada, cruce de ISP).
	pending("ASSIST (asistente IA: suite propia de aislamiento en services/assist)",
		"getAssistStatus", "getAssistSettings", "putAssistSettings", "testAssistSettings", "createAssistAnalysis", "listAssistAnalyses",
		"listAssistAudit", "listAssistMcpTokens", "createAssistMcpToken", "revokeAssistMcpToken")
	pending("FLOW I1 (flows: exportadores)", "listFlowExporters", "getFlowExporter", "getClassificationStatus")
	// snmp (EP-08): el arnés no monta snmp y de analytics solo los dashboards.
	pending("FLOW I2 (snmp: estado y series de routers; suite propia con ClickHouse)",
		"listRouterStatus", "getRouterLiveMetrics", "listRouterInterfaces", "pollRouterNow", "getRouterMetrics",
		"getRouterInterfaceMetrics")
	// snmp: servicios de infraestructura. El arnés de la matriz no monta snmp; su aislamiento (listado, detalle,
	// edición, baja y script de router de otro ISP → 404; RLS del esquema snmp) lo cubre TestInfraServicesAPI.
	pending("FLOW I2 (snmp: servicios de infraestructura; TestInfraServicesAPI)",
		"listInfraServices", "createInfraService", "getInfraService", "updateInfraService", "deleteInfraService",
		"getInfraServiceRouterSnippet")
	// alerts: integración LibreNMS (D17) y asistentes de Telegram (D13); el detalle (RLS de alerts.librenms_*,
	// códigos de vinculación por ISP) está en TestLibreNMSIntegration y TestAlertsRouterCategoriesAndTelegramLink.
	c["getLibreNmsIntegration"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["syncLibreNmsIntegration"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["testLibreNmsIntegration"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["deleteLibreNmsRouterDevice"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["listLibreNmsRouters"] = tenanttest.Case{Kind: tenanttest.List}
	c["setLibreNmsRouter"] = tenanttest.Case{Kind: tenanttest.ByID, Body: map[string]any{"mgmt_ip": "192.0.2.10", "mode": "ping"}}
	c["clearLibreNmsRouter"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["putLibreNmsRouterSnmpCredentials"] = tenanttest.Case{Kind: tenanttest.ByID, Body: map[string]any{"community": "pirata-comunidad"}}
	c["validateTelegramBot"] = tenanttest.Case{Kind: tenanttest.Create, Body: map[string]any{"bot_token": "123456:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef0123"}}
	c["createTelegramLinkCode"] = tenanttest.Case{Kind: tenanttest.Create}
	c["detectTelegramChats"] = tenanttest.Case{Kind: tenanttest.Create, Body: map[string]any{"code": "hfabcdefghij"}}
	// analytics (rol reporting, I3): reportes y su programación.
	c["listReports"] = tenanttest.Case{Kind: tenanttest.List}
	c["createReport"] = tenanttest.Case{Kind: tenanttest.Create, Body: map[string]any{"kind": "security_weekly"}}
	c["getReport"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["downloadReport"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["listReportDefinitions"] = tenanttest.Case{Kind: tenanttest.List}
	c["updateReportDefinition"] = tenanttest.Case{Kind: tenanttest.ByID, Body: map[string]any{"enabled": false}}
	// Umbrales de routers por ISP (B no ve ni cambia los de A; RLS de snmp.router_thresholds) los cubre
	// TestSNMPThresholdsPerTenant.
	// auth: API tokens de integración hf_pat_ (H1, ADR-0030).
	c["listApiTokens"] = tenanttest.Case{Kind: tenanttest.List}
	c["listApiTokenTemplates"] = tenanttest.Case{Kind: tenanttest.List}
	c["createApiToken"] = tenanttest.Case{Kind: tenanttest.Create, Body: map[string]any{"name": "pirata", "template": "argosisp"}}
	c["revokeApiToken"] = tenanttest.Case{Kind: tenanttest.ByID}
	// devices: integraciones (H2, H3, H6). La IP va en el cuerpo; el cruce con un hf_pat_ de otro ISP lo
	// cubre TestAPITokenTenantIsolation.
	c["lookupIpIntegration"] = tenanttest.Case{Kind: tenanttest.Create, Body: map[string]any{"address": "10.10.0.41"}}
	c["getIntegrationConsumption"] = tenanttest.Case{Kind: tenanttest.Create, Body: map[string]any{"from": "2026-10-01T00:00:00Z",
		"to": "2026-10-02T00:00:00Z", "items": []any{map[string]any{"address": "10.10.0.41"}}}}
	// H9 (contrato 1.2.0): estado de seguridad por lotes; el cruce con un hf_pat_ de otro ISP lo cubre
	// TestAPITokenTenantIsolation.
	c["getIntegrationSecurityStates"] = tenanttest.Case{Kind: tenanttest.Create, Body: map[string]any{
		"items": []any{map[string]any{"address": "10.10.0.41"}}}}
	c["getArgosIntegration"] = tenanttest.Case{Kind: tenanttest.List}
	c["putArgosIntegration"] = tenanttest.Case{Kind: tenanttest.Create, Body: map[string]any{"base_url": "https://argos.example/api/v1", "enabled": false}}
	c["rotateArgosWebhookSecret"] = tenanttest.Case{Kind: tenanttest.Create}
	c["testArgosIntegration"] = tenanttest.Case{Kind: tenanttest.Create}
	c["getCustomerExternalIdentity"] = tenanttest.Case{Kind: tenanttest.ByID}
	c["refreshCustomerExternalIdentity"] = tenanttest.Case{Kind: tenanttest.ByID}
	// H10–H11 (contrato 1.4.0): enlace de conexión y autologin. Los canjes públicos (conexion/canjear,
	// sso/redeem) y /me/sso-links no son de ISP; su aislamiento (código y ticket ligados a su ISP, un
	// usuario de otro ISP no vincula) lo cubren TestArgosConnectionLinkAndSSO y TestArgosRedeemLinkAndOpen.
	c["createArgosConnectionLink"] = tenanttest.Case{Kind: tenanttest.Create}
	c["redeemArgosConnectionLink"] = tenanttest.Case{Kind: tenanttest.Create, Body: map[string]any{
		"link": "https://argos.example/conectar#AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}}
	c["issueArgosSsoTicket"] = tenanttest.Case{Kind: tenanttest.Create, Body: map[string]any{
		"usuario": map[string]any{"id_externo": "u-pirata"}, "destino": map[string]any{"tipo": "servicio", "id_externo": "812"}}}
	c["resolveArgosSsoTarget"] = tenanttest.Case{Kind: tenanttest.Create, Body: map[string]any{"type": "servicio", "external_id": "812"}}
	c["openArgosSso"] = tenanttest.Case{Kind: tenanttest.Create, Body: map[string]any{"customer_id": "0192f000-0000-7000-8000-00000000c001"}}
	c["listArgosSsoLinks"] = tenanttest.Case{Kind: tenanttest.List}
	c["revokeAllArgosSsoLinks"] = tenanttest.Case{Kind: tenanttest.List}
	c["revokeArgosSsoLink"] = tenanttest.Case{Kind: tenanttest.ByID}
	pending("EP-08 (snmp: umbrales por ISP; TestSNMPThresholdsPerTenant)",
		"getRouterThresholds", "putRouterThresholds", "resetRouterThresholds")
	// snmp: capacidad de enlaces (el arnés de la matriz no monta snmp). Ajustes y enlaces de otro ISP → 404, RLS de las
	// tablas nuevas y visor sin escritura: TestCapacityAPI.
	pending("FLOW I3 (snmp: capacidad de enlaces; TestCapacityAPI)",
		"getCapacityOverview", "getCapacityEgress", "getCapacityUplink", "putCapacityUplink", "deleteCapacityUplink",
		"getCapacitySettings", "putCapacitySettings", "resetCapacitySettings", "getCapacityTrafficSplit", "getRouterNetwatchSnippet")
	return c
}

// fixtureISP crea un nodo con router y prefijo y devuelve sus IDs.
func (a *app) fixtureISP(x isp, cidr string) map[string]string {
	a.t.Helper()
	site := a.must(a.post(x.token, "/api/v1/sites", map[string]any{"name": "Nodo"}), 201).str("id")
	router := a.must(a.post(x.token, "/api/v1/routers", map[string]any{"site_id": site, "name": "rt-1", "routeros_version": "7.16"}), 201).str("id")
	cp := a.must(a.post(x.token, "/api/v1/sites/"+site+"/client-prefixes", map[string]any{"prefix": cidr, "role": "customers"}), 201).str("id")
	script := a.must(a.do(req{Method: "POST", Path: "/api/v1/routers/" + router + "/provisioning-script", Token: x.token,
		Header: map[string]string{"Idempotency-Key": uuid.NewString()}}), 201)
	peers := a.must(a.do(req{Method: "GET", Path: "/api/v1/wireguard/peers?router_id=" + router, Token: x.token}), 200)
	data, _ := peers.Body["data"].([]any)
	if len(data) != 1 {
		a.t.Fatalf("peers del router = %s", peers.Raw)
	}
	peer, _ := data[0].(map[string]any)["id"].(string)
	out := map[string]string{"site_id": site, "router_id": router, "client_prefix_id": cp, "peer_id": peer,
		"token_id": script.Header.Get("X-Horus-Enrollment-Token-Id")}
	for k, v := range a.fixtureDetection(x, site, router) {
		out[k] = v
	}
	// Cliente real (I1-06) en el prefijo del nodo.
	realm := a.realmOf(x.token, site)
	out["customer_id"] = a.insertCustomer(x.id, site, realm, cp, "10.10.0.41")
	out["kiosk_id"] = a.must(a.post(x.token, "/api/v1/kiosks", map[string]any{"name": "TV NOC"}), 201).str("id")
	dash := a.must(a.post(x.token, "/api/v1/dashboards", map[string]any{"name": "NOC propio", "visibility": "tenant",
		"layout":  map[string]any{"grid": "12-col", "columns": 12, "row_height_px": 80},
		"widgets": []any{map[string]any{"id": "w-1", "type": "noc_header", "title": nil, "position": map[string]any{"x": 0, "y": 0, "w": 12, "h": 1}, "config": map[string]any{}}},
	}), 201).str("id")
	out["dashboard_id"], out["widget_id"] = dash, "w-1"
	out["channel_id"] = a.must(a.post(x.token, "/api/v1/notification-channels", map[string]any{"name": "NOC", "kind": "telegram",
		"config": map[string]any{"chat_id": "-100123"}, "subscription": map[string]any{"event_types": []string{"finding_opened"}}}), 201).str("id")
	out["playlist_id"] = a.must(a.post(x.token, "/api/v1/playlists", map[string]any{"name": "Rotación",
		"items": []any{map[string]any{"dashboard_id": dash, "duration_seconds": 30}}}), 201).str("id")
	// Miembro del ISP (su administrador) para /members/{user_id}.
	out["user_id"] = a.must(a.do(req{Method: "GET", Path: "/api/v1/me", Token: x.admin.token}), 200).str("id")
	// Rol propio del ISP para /roles/{role_id}.
	out["role_id"] = a.must(a.post(x.token, "/api/v1/roles", map[string]any{"key": "soporte_nodo", "name": "Soporte de nodo",
		"permissions": []string{"sites.read", "devices.read"}}), 201).str("id")
	out["kind"] = "snmp" // /routers/{router_id}/credentials/{kind}: no es un ID (fuera de ForeignIDs)
	// Regla ASN propia del ISP para /asn-rules/{asn_rule_id}.
	out["asn_rule_id"] = a.must(a.post(x.token, "/api/v1/asn-rules", map[string]any{"asn": 263237, "name": "Tránsito local",
		"category": "cdn_infra"}), 201).str("id")
	// Reporte a petición y programación semanal (rol reporting) para /reports/{report_id} y
	// /report-definitions/{definition_id}.
	out["report_id"] = a.must(a.do(req{Method: "POST", Path: "/api/v1/reports", Token: x.token, Body: map[string]any{"kind": "security_weekly"},
		Header: map[string]string{"Idempotency-Key": uuid.NewString()}}), 202).str("id")
	defs, _ := a.must(a.do(req{Method: "GET", Path: "/api/v1/report-definitions", Token: x.token}), 200).Body["data"].([]any)
	if len(defs) != 1 {
		a.t.Fatalf("definiciones de reportes = %v", defs)
	}
	out["definition_id"], _ = defs[0].(map[string]any)["id"].(string)
	// API token de integración (H1) para /api-tokens/{api_token_id}.
	out["api_token_id"] = a.must(a.post(x.token, "/api/v1/api-tokens", map[string]any{"name": "ArgosISP", "template": "argosisp"}), 201).str("id")
	// Vinculación de autologin (H11) para /integrations/argosisp/sso/links/{link_id}.
	out["link_id"] = uuid.Must(uuid.NewV7()).String()
	if _, err := a.admin.Exec(context.Background(), `INSERT INTO auth.sso_link (id, tenant_id, connector_id, external_user_id, user_id)
		VALUES ($1, $2, $3, 'u-17', $4)`, out["link_id"], x.id, uuid.New(), out["user_id"]); err != nil {
		a.t.Fatal(err)
	}
	if _, err := a.admin.Exec(context.Background(), `UPDATE detection.finding SET customer_id = $1 WHERE id = $2`,
		out["customer_id"], out["finding_id"]); err != nil {
		a.t.Fatal(err)
	}
	return out
}

// I0-08: matriz de aislamiento sobre el contrato. Para cada operación de ISP
// implementada: sin token → 401; token sin ISP → 403 TOKEN_SCOPE_INVALID sin
// tocar la base; A sobre IDs de B → 404; listados de A sin IDs de B;
// tenant_id de B en el cuerpo → 403 TENANT_MISMATCH.
func TestTenantIsolationMatrix(t *testing.T) {
	t.Parallel()
	a := startApp(t)
	pt := a.superadmin().platformToken()
	A, B := a.newISP(pt, "isp-a"), a.newISP(pt, "isp-b")
	paramsA := a.fixtureISP(A, "10.10.0.0/24")
	paramsB := a.fixtureISP(B, "10.10.0.0/24") // mismo rango, otro ISP: válido
	// Otro router de B para el listado.
	a.must(a.post(B.token, "/api/v1/routers", map[string]any{"site_id": paramsB["site_id"], "name": "rt-2", "is_primary": false}), 201)
	// Versiones de B tras el alta (la proyección del túnel ya subió la del router).
	versionsB := map[string]float64{}
	for _, path := range []string{"/api/v1/sites/" + paramsB["site_id"], "/api/v1/routers/" + paramsB["router_id"]} {
		versionsB[path], _ = a.must(a.do(req{Method: "GET", Path: path, Token: B.token}), 200).Body["version"].(float64)
	}
	var foreign []string
	for k, v := range paramsB {
		if k != "kind" {
			foreign = append(foreign, v)
		}
	}

	root, err := archtest.FindRepoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	ops, err := tenanttest.LoadOperations(filepath.Join(root, "packages/schemas/openapi/dist/horus-api.v0.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// Rutas que, sin su rol en el proceso, casan con el comodín de otra ruta.
	shadowed := map[string]bool{"GET /api/v1/routers/status": true, "GET /api/v1/routers/thresholds": true,
		"PUT /api/v1/routers/thresholds": true, "DELETE /api/v1/routers/thresholds": true}
	suite := &tenanttest.Suite{
		Ops: ops, Cases: cases(), TokenA: A.token, SessionA: A.admin.token, TenantB: B.id,
		ParamsA: paramsA, ParamsB: paramsB, ForeignIDs: foreign,
		Do: func(token, method, path string, body any, header map[string]string) (int, []byte) {
			r := a.do(req{Method: method, Path: path, Token: token, Body: body, Header: header})
			return r.Status, r.Raw
		},
		Mounted: func(method, tmpl string) bool {
			// Patrón exacto primero: por comodín, /routers/status casaría con
			// GET /routers/{router_id} (devices) y daría por montada una ruta
			// del rol snmp que el arnés no monta.
			if slices.Contains(a.mux.Patterns(), method+" "+tmpl) {
				return true
			}
			if shadowed[method+" "+tmpl] {
				return false
			}
			r, _ := http.NewRequestWithContext(context.Background(), method, strings.NewReplacer(
				"{site_id}", paramsA["site_id"], "{router_id}", paramsA["router_id"], "{client_prefix_id}", paramsA["client_prefix_id"],
				"{peer_id}", paramsA["peer_id"], "{token_id}", paramsA["token_id"],
				"{finding_id}", paramsA["finding_id"], "{customer_id}", paramsA["customer_id"], "{entry_id}", paramsA["entry_id"],
				"{kiosk_id}", paramsA["kiosk_id"], "{dashboard_id}", paramsA["dashboard_id"], "{widget_id}", paramsA["widget_id"],
				"{playlist_id}", paramsA["playlist_id"], "{channel_id}", paramsA["channel_id"], "{user_id}", paramsA["user_id"],
				"{kind}", paramsA["kind"], "{role_id}", paramsA["role_id"], "{asn_rule_id}", paramsA["asn_rule_id"], "{asn}", "263237",
				"{report_id}", paramsA["report_id"], "{definition_id}", paramsA["definition_id"], "{api_token_id}", paramsA["api_token_id"],
				"{link_id}", paramsA["link_id"],
			).Replace(tmpl), nil)
			return a.mux.Matches(r)
		},
	}
	suite.Run(t)

	// B sigue intacto tras todos los intentos de A.
	for _, path := range []string{"/api/v1/sites/" + paramsB["site_id"], "/api/v1/routers/" + paramsB["router_id"]} {
		r := a.must(a.do(req{Method: "GET", Path: path, Token: B.token}), 200)
		if r.Body["version"].(float64) != versionsB[path] {
			t.Errorf("%s modificado por A: %s", path, r.Raw)
		}
	}
}

// docs/security.md §3.9: POST /auth/token con el ISP B para un usuario solo de A → 404.
func TestTokenForForeignTenant(t *testing.T) {
	t.Parallel()
	a := startApp(t)
	pt := a.superadmin().platformToken()
	A, B := a.newISP(pt, "isp-a"), a.newISP(pt, "isp-b")
	r := a.post(A.admin.token, "/api/v1/auth/token", map[string]string{"tenant_id": B.id})
	if r.Status != 404 || r.code() != "TENANT_NOT_FOUND" {
		t.Fatalf("token de B para usuario de A: %d %s", r.Status, r.Raw)
	}
	ghost := a.post(A.admin.token, "/api/v1/auth/token", map[string]string{"tenant_id": "0192f0c4-7a1e-7c3a-9b1d-2f6e8a4c1d55"})
	if ghost.Status != r.Status || ghost.code() != r.code() || ghost.str("detail") != r.str("detail") {
		t.Fatalf("ISP inexistente distinguible de ISP ajeno: %s vs %s", ghost.Raw, r.Raw)
	}
	// Un token de A con el tid alterado no valida (firma).
	parts := strings.Split(A.token, ".")
	claims, _ := jsonB64(parts[1])
	claims["tid"] = B.id
	parts[1] = b64JSON(claims)
	if r := a.do(req{Method: "GET", Path: "/api/v1/sites", Token: strings.Join(parts, ".")}); r.Status != 401 {
		t.Fatalf("token manipulado: %d", r.Status)
	}
}

// I0-08 criterio 4: el superadmin accede a datos de un ISP (via_platform) y
// queda registrado con actor, ISP y recurso.
func TestSuperadminAccessAudited(t *testing.T) {
	t.Parallel()
	a := startApp(t)
	root := a.superadmin()
	pt := root.platformToken()
	A := a.newISP(pt, "isp-a")
	ids := a.fixtureISP(A, "100.64.0.0/22")
	tok := root.tenantToken(A.id)
	r := a.must(a.do(req{Method: "GET", Path: "/api/v1/routers/" + ids["router_id"], Token: tok}), 200)
	if r.str("tenant_id") != A.id {
		t.Fatalf("router = %s", r.Raw)
	}
	var actor, tenant, resource, rtype string
	var via bool
	err := a.admin.QueryRow(context.Background(), `SELECT actor_id, tenant_id::text, resource_id, resource_type, via_platform
		FROM auth.audit_log WHERE action = 'platform.tenant_data.accessed' ORDER BY occurred_at DESC LIMIT 1`).Scan(&actor, &tenant, &resource, &rtype, &via)
	if err != nil {
		t.Fatalf("acceso no auditado: %v", err)
	}
	var rootID string
	_ = a.admin.QueryRow(context.Background(), `SELECT id::text FROM auth."user" WHERE email = $1`, seedEmail).Scan(&rootID)
	if actor != rootID || tenant != A.id || resource != ids["router_id"] || !via || !strings.Contains(rtype, "/routers/{router_id}") {
		t.Fatalf("auditoría = actor %s tenant %s recurso %s (%s) via %v", actor, tenant, resource, rtype, via)
	}
	if a.auditCount("platform.support_access.token_issued") != 2 { // en el ISP y en la plataforma
		t.Fatal("emisión del token de soporte no auditada en ambos lados")
	}
	// La cadena de hashes del tenant es continua.
	rows, err := a.admin.Query(context.Background(), `SELECT prev_hash, hash FROM auth.audit_log WHERE tenant_id = $1 ORDER BY occurred_at, id`, A.id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var prev []byte
	for rows.Next() {
		var p, h []byte
		if err := rows.Scan(&p, &h); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(p, prev) {
			t.Fatal("cadena de hashes de auditoría rota")
		}
		prev = h
	}
}

func jsonB64(s string) (map[string]any, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	return m, json.Unmarshal(b, &m)
}

func b64JSON(m map[string]any) string {
	b, _ := json.Marshal(m)
	return base64.RawURLEncoding.EncodeToString(b)
}
