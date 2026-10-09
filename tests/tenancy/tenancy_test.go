//go:build integration

package tenancy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"path/filepath"
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
	}
	pending := func(owner string, ids ...string) {
		for _, id := range ids {
			c[id] = tenanttest.Case{Kind: tenanttest.Pending, Owner: owner}
		}
	}
	pending("CORE I1 (devices: clientes, credenciales, importación y lotes de prefijos)",
		"listCustomers", "getCustomerStats", "getCustomer", "listCustomerKindHistory", "updateCustomer", "confirmClientPrefix",
		"lookupCustomers", "resetCustomer", "setCustomerKind", "unlockCustomerKind", "previewRouterPrefixImport",
		"batchCreateClientPrefixes", "putRouterCredential")
	pending("CORE I1 (auth: miembros, roles y kioscos)",
		"removeMember", "listKiosks", "getKiosk", "listMembers", "listRoles", "updateKiosk", "createKiosk",
		"createKioskEnrollmentCode", "revokeKiosk", "inviteMember", "replaceMemberRoleAssignments")
	pending("CORE I1 (alerts: canales de notificación, D13/D17/D21)",
		"deleteNotificationChannel", "listNotificationChannels", "getNotificationChannel", "listNotificationDeliveries",
		"updateNotificationChannel", "createNotificationChannel", "testNotificationChannelConnection", "testNotificationChannel",
		"putNotificationChannelCredentials")
	pending("analytics (dashboards, widgets, playlists, tráfico)",
		"listDashboards", "getDashboard", "deleteDashboard", "deleteDashboardWidget", "deletePlaylist", "getCustomerTraffic",
		"getTrafficAttribution", "getTrafficTimeseries", "getTrafficTop", "getWidgetData", "getKioskConfig", "listPlaylists",
		"listPrefixProposals", "updateDashboard", "updateDashboardWidget", "updatePlaylist", "createDashboard",
		"duplicateDashboard", "addDashboardWidget", "createPlaylist", "previewWidgetData", "replaceDashboardLayout")
	pending("SEC I1 (detection: hallazgos y reputación)",
		"deleteAllowlistEntry", "listCustomerFindings", "listFindings", "getFinding", "getFindingEvidence", "listAllowlist",
		"listReputationSources", "getSecuritySummary", "acknowledgeFinding", "markFindingFalsePositive", "resolveFinding",
		"createAllowlistEntry")
	pending("FLOW I1 (flows: exportadores)", "listFlowExporters", "getFlowExporter")
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
	return map[string]string{"site_id": site, "router_id": router, "client_prefix_id": cp, "peer_id": peer,
		"token_id": script.Header.Get("X-Horus-Enrollment-Token-Id")}
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
	for _, v := range paramsB {
		foreign = append(foreign, v)
	}

	root, err := archtest.FindRepoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	ops, err := tenanttest.LoadOperations(filepath.Join(root, "packages/schemas/openapi/dist/horus-api.v0.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	suite := &tenanttest.Suite{
		Ops: ops, Cases: cases(), TokenA: A.token, SessionA: A.admin.token, TenantB: B.id,
		ParamsA: paramsA, ParamsB: paramsB, ForeignIDs: foreign,
		Do: func(token, method, path string, body any, header map[string]string) (int, []byte) {
			r := a.do(req{Method: method, Path: path, Token: token, Body: body, Header: header})
			return r.Status, r.Raw
		},
		Mounted: func(method, tmpl string) bool {
			r, _ := http.NewRequestWithContext(context.Background(), method, strings.NewReplacer(
				"{site_id}", paramsA["site_id"], "{router_id}", paramsA["router_id"], "{client_prefix_id}", paramsA["client_prefix_id"],
				"{peer_id}", paramsA["peer_id"], "{token_id}", paramsA["token_id"],
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
