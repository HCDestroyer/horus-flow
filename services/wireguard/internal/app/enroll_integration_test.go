//go:build integration

package app_test

import (
	"context"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/wgagent/api/agentv1"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/app"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/domain"
)

// I1-01 criterios 2–6 e I1-02 criterios 1, 3, 5 y 6: script con token →
// enrolamiento → peer en el estado deseado del agente → handshake → activo;
// token de un uso; clave repetida; 5 fallos; reinicio del agente; baja.
func TestEnrollmentLifecycle(t *testing.T) {
	t.Parallel()
	e := setup(t)
	ctx := context.Background()
	tenant := uuid.Must(uuid.NewV7())
	r := e.dev.add(tenant, uuid.Must(uuid.NewV7()), "7.16.1")
	uctx := userCtx(tenant, "wireguard.write", "wireguard.read")

	old := "7.10"
	if _, err := e.svc.CreateProvisioningScript(uctx, r.ID, app.ProvisioningInput{RouterOSVersion: &old}); code(err) != domain.CodeRouterOSTooOld {
		t.Fatalf("7.10: %v", err)
	}
	if _, err := e.svc.CreateProvisioningScript(userCtx(tenant, "wireguard.read"), r.ID, app.ProvisioningInput{}); code(err) != "PERMISSION_DENIED" {
		t.Fatalf("viewer: %v", err)
	}
	if _, err := e.svc.CreateProvisioningScript(userCtx(uuid.Must(uuid.NewV7()), "wireguard.write"), r.ID, app.ProvisioningInput{}); code(err) != domain.CodeRouterNotFound {
		t.Fatalf("otro ISP: %v", err)
	}

	first, err := e.svc.CreateProvisioningScript(uctx, r.ID, app.ProvisioningInput{})
	if err != nil {
		t.Fatal(err)
	}
	addr := e.dev.tunnels[r.ID].Address
	if strings.Contains(first.Script, "<") || !strings.Contains(first.Script, addr.String()+"/32") {
		t.Fatal("script sin la IP de túnel o con placeholders")
	}
	tok1 := tokenOf(t, first.Script)
	second, err := e.svc.CreateProvisioningScript(uctx, r.ID, app.ProvisioningInput{})
	if err != nil {
		t.Fatal(err)
	}
	tok2 := tokenOf(t, second.Script)
	if tok1 == tok2 || e.dev.issued != 2 || e.audit.count("wireguard.provisioning_script.created") != 2 {
		t.Fatal("regenerar no crea token y credenciales nuevos (o no se audita)")
	}
	ip := netip.MustParseAddr("198.51.100.7")
	if err := e.svc.Enroll(ctx, app.EnrollInput{Token: tok1, PublicKey: key(1), IP: ip}); code(err) != domain.CodeTokenInvalid {
		t.Fatalf("token anterior: %v", err)
	}

	if err := e.svc.Enroll(ctx, app.EnrollInput{Token: tok2, PublicKey: key(1), IP: ip}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Push(ctx, false); err != nil {
		t.Fatal(err)
	}
	peers := e.agent.peers()
	if len(peers) != 1 || peers[0].GetPublicKey() != key(1) || peers[0].GetAllowedIps()[0] != addr.String()+"/32" ||
		peers[0].GetPersistentKeepaliveSeconds() != 25 {
		t.Fatalf("estado deseado = %v", peers)
	}
	if e.dev.tunnels[r.ID].OnboardingState != devapi.OnboardingKeyReceived || e.audit.count("wireguard.peer.enrolled") != 1 {
		t.Fatal("enrolamiento no proyectado o no auditado")
	}
	if err := e.svc.Enroll(ctx, app.EnrollInput{Token: tok2, PublicKey: key(2), IP: ip}); code(err) != domain.CodeTokenInvalid {
		t.Fatalf("token usado: %v", err)
	}

	// Otro router: clave ya registrada → 409 y fallo; 5 fallos invalidan.
	r2 := e.dev.add(tenant, uuid.Must(uuid.NewV7()), "7.20")
	s2, err := e.svc.CreateProvisioningScript(uctx, r2.ID, app.ProvisioningInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s2.Script, "long-term") {
		t.Fatal("7.20 sin plantilla long-term")
	}
	tok3 := tokenOf(t, s2.Script)
	for i := range 4 {
		if err := e.svc.Enroll(ctx, app.EnrollInput{Token: tok3, PublicKey: key(1), IP: ip}); code(err) != domain.CodePublicKeyInUse {
			t.Fatalf("intento %d con clave repetida: %v", i, err)
		}
	}
	if err := e.svc.Enroll(ctx, app.EnrollInput{Token: tok3, PublicKey: "no-es-una-clave", IP: ip}); code(err) != "VALIDATION_FAILED" {
		t.Fatalf("clave inválida: %v", err)
	}
	if err := e.svc.Enroll(ctx, app.EnrollInput{Token: tok3, PublicKey: key(3), IP: ip}); code(err) != domain.CodeTokenInvalid {
		t.Fatalf("tras 5 fallos el token sigue válido: %v", err)
	}
	s3, err := e.svc.CreateProvisioningScript(uctx, r2.ID, app.ProvisioningInput{})
	if err != nil {
		t.Fatal(err)
	}
	*e.now = e.now.Add(25 * time.Hour)
	if err := e.svc.Enroll(ctx, app.EnrollInput{Token: tokenOf(t, s3.Script), PublicKey: key(3), IP: ip}); code(err) != domain.CodeTokenInvalid {
		t.Fatalf("token caducado: %v", err)
	}
	if err := e.svc.Enroll(ctx, app.EnrollInput{Token: strings.Repeat("x", 43), PublicKey: key(3), IP: ip}); code(err) != domain.CodeTokenInvalid {
		t.Fatalf("token inexistente: %v", err)
	}

	// Handshake → activo; reporte con applied_version 0 (agente reiniciado)
	// → se reenvía el estado completo.
	before := e.agent.n
	hs := *e.now
	resp, err := e.svc.ReportStatus(ctx, &agentv1.ReportStatusRequest{HubId: e.hubID.String(), AppliedVersion: 0, InterfaceUp: true,
		Peers: []*agentv1.ObservedPeer{{PublicKey: key(1), LastHandshakeAt: timestamppb.New(hs), RxBytes: 1000, TxBytes: 2000}}})
	if err != nil || resp.GetDesiredVersion() < 2 {
		t.Fatalf("report = %v, %v", resp, err)
	}
	if err := e.svc.Push(ctx, false); err != nil || e.agent.n != before+1 {
		t.Fatalf("no se reenvió el estado tras el reinicio del agente: %v (%d)", err, e.agent.n-before)
	}
	if e.dev.tunnels[r.ID].OnboardingState != devapi.OnboardingTunnelUp {
		t.Fatal("tunnel_up no proyectado")
	}
	peer, err := e.svc.GetPeer(uctx, e.dev.tunnels[r.ID].PeerID)
	if err != nil || peer.Status != domain.StatusActive || peer.RxBytes != 1000 || e.svc.HandshakeState(peer) != domain.HandshakeOK {
		t.Fatalf("peer = %+v, %v", peer, err)
	}
	if _, err := e.svc.GetPeer(userCtx(uuid.Must(uuid.NewV7()), "wireguard.read"), peer.ID); code(err) != "NOT_FOUND" {
		t.Fatalf("peer de otro ISP: %v", err)
	}
	*e.now = e.now.Add(10 * time.Minute)
	if _, err := e.svc.ReportStatus(ctx, &agentv1.ReportStatusRequest{HubId: e.hubID.String(), AppliedVersion: resp.GetDesiredVersion(), InterfaceUp: true,
		Peers: []*agentv1.ObservedPeer{{PublicKey: key(1), LastHandshakeAt: timestamppb.New(hs)}}}); err != nil {
		t.Fatal(err)
	}
	var stale, activated, leaked int
	_ = e.admin.QueryRow(ctx, `SELECT count(*) FILTER (WHERE subject LIKE 'horus.wireguard.peer.handshake_stale.%'),
		count(*) FILTER (WHERE subject LIKE 'horus.wireguard.peer.activated.%'),
		count(*) FILTER (WHERE payload::text LIKE '%' || $1 || '%') FROM wireguard.outbox`, tok2).Scan(&stale, &activated, &leaked)
	if stale != 1 || activated != 1 || leaked != 0 {
		t.Fatalf("eventos stale=%d activated=%d con token=%d", stale, activated, leaked)
	}

	// Router dado de baja → peer revocado y fuera del estado deseado.
	e.dev.del(r.ID)
	if err := e.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Push(ctx, false); err != nil {
		t.Fatal(err)
	}
	for _, p := range e.agent.peers() {
		if p.GetPublicKey() == key(1) {
			t.Fatal("peer de un router borrado sigue en el hub")
		}
	}
}

func TestRevokeToken(t *testing.T) {
	t.Parallel()
	e := setup(t)
	tenant := uuid.Must(uuid.NewV7())
	r := e.dev.add(tenant, uuid.Must(uuid.NewV7()), "")
	uctx := userCtx(tenant, "wireguard.write")
	res, err := e.svc.CreateProvisioningScript(uctx, r.ID, app.ProvisioningInput{})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RevokeEnrollmentToken(userCtx(uuid.Must(uuid.NewV7()), "wireguard.write"), res.TokenID); code(err) != "NOT_FOUND" {
		t.Fatalf("revocar token de otro ISP: %v", err)
	}
	if err := e.svc.RevokeEnrollmentToken(uctx, res.TokenID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Enroll(context.Background(), app.EnrollInput{Token: tokenOf(t, res.Script), PublicKey: key(9)}); code(err) != domain.CodeTokenInvalid {
		t.Fatalf("token revocado: %v", err)
	}
	if err := e.svc.RevokeEnrollmentToken(uctx, res.TokenID); code(err) != "NOT_FOUND" {
		t.Fatalf("revocar dos veces: %v", err)
	}
}
