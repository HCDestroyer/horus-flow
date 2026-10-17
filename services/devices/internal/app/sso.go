package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// Autologin con ArgosISP (contrato docs/integrations/argosisp.md §1.5, H11).
const (
	PermSSOIssue                  = "sso.issue"
	PermDevicesRead               = "devices.read"
	CodeExternalIdentityNotFound  = "EXTERNAL_IDENTITY_NOT_FOUND"
	CodeSSOTargetNotFound         = "SSO_TARGET_NOT_FOUND"
	ssoEntryPath                  = "/sso/entrar"
	maxExternalIDLen              = 128
	maxExternalEmailLen           = 254
	maxExternalNameLen            = 120
	ssoTargetCliente              = "cliente"
	ssoTargetServicio             = "servicio"
	ssoTargetRouter               = "router"
	ssoTargetHallazgo             = "hallazgo"
	ssoKindCustomer               = "customer"
	ssoKindRouter                 = "router"
	ssoKindFinding                = "finding"
	ssoOpenTimeout                = 10 * time.Second
	errSSOUnavailableDetailPrefix = "ArgosISP "
)

var _ authapi.SSOConnectors = (*Integrations)(nil)

// SSOConnectorActive implementa authapi.SSOConnectors: el ticket solo se
// canjea si el conector que lo emitió es el del ISP y sigue activo.
func (s *Integrations) SSOConnectorActive(ctx context.Context, tenant, connector uuid.UUID) (bool, error) {
	c, err := s.store.GetArgosConnector(ctx, pgdb.TenantID(tenant))
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return c.ID == connector && c.Enabled, nil
}

// SSOTicketInput es SsoTicketRequest (claves del contrato).
type SSOTicketInput struct {
	Usuario *struct {
		IDExterno string  `json:"id_externo"`
		Correo    *string `json:"correo"`
		Nombre    *string `json:"nombre"`
	} `json:"usuario"`
	Destino *struct {
		Tipo      string `json:"tipo"`
		IDExterno string `json:"id_externo"`
	} `json:"destino"`
}

// cleanText recorta y rechaza caracteres de control.
func cleanText(s string, maxLen int) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || utf8.RuneCountInString(s) > maxLen || !utf8.ValidString(s) {
		return "", false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	return s, true
}

func optText(p *string, maxLen int) (*string, bool) {
	if p == nil || strings.TrimSpace(*p) == "" {
		return nil, true
	}
	v, ok := cleanText(*p, maxLen)
	if !ok {
		return nil, false
	}
	return &v, true
}

func validTargetType(t string) bool {
	switch t {
	case ssoTargetCliente, ssoTargetServicio, ssoTargetRouter, ssoTargetHallazgo:
		return true
	}
	return false
}

// SSOTicketIssued es la URL de un uso que abre el navegador.
type SSOTicketIssued struct {
	URL       string
	ExpiresAt time.Time
}

// IssueSSOTicket es POST /integrations/argosisp/sso/tickets: lo llama el
// servidor de ArgosISP con su hf_pat_ (sso.issue). Solo API tokens; si el
// conector se preparó con un enlace, solo el token emitido en ese canje.
func (s *Integrations) IssueSSOTicket(ctx context.Context, in SSOTicketInput) (*SSOTicketIssued, error) {
	p, t, err := scope(ctx, PermSSOIssue)
	if err != nil {
		return nil, err
	}
	if !p.IsAPIToken() {
		return nil, apperr.Forbidden(problem.CodePermissionDenied, "Solo el servidor del sistema integrado, con su API token, emite tickets.")
	}
	var fe fieldErrs
	var req authapi.SSOTicketRequest
	if in.Usuario == nil {
		fe.add("usuario", "REQUIRED", "Usuario del sistema de origen.")
	} else {
		var ok bool
		if req.ExternalUserID, ok = cleanText(in.Usuario.IDExterno, maxExternalIDLen); !ok {
			fe.add("usuario.id_externo", "INVALID_VALUE", "Id del usuario en ArgosISP (1–128 caracteres).")
		}
		if req.ExternalEmail, ok = optText(in.Usuario.Correo, maxExternalEmailLen); !ok {
			fe.add("usuario.correo", "INVALID_VALUE", "Correo de hasta 254 caracteres.")
		}
		if req.ExternalName, ok = optText(in.Usuario.Nombre, maxExternalNameLen); !ok {
			fe.add("usuario.nombre", "INVALID_VALUE", "Nombre de hasta 120 caracteres.")
		}
	}
	if in.Destino == nil {
		fe.add("destino", "REQUIRED", "Recurso a abrir en Horus.")
	} else {
		var ok bool
		if !validTargetType(in.Destino.Tipo) {
			fe.add("destino.tipo", "INVALID_VALUE", "cliente, servicio, router o hallazgo.")
		}
		if req.TargetID, ok = cleanText(in.Destino.IDExterno, maxExternalIDLen); !ok {
			fe.add("destino.id_externo", "INVALID_VALUE", "Id del recurso (1–128 caracteres).")
		}
		req.TargetType = in.Destino.Tipo
	}
	if err := fe.err(); err != nil {
		return nil, err
	}
	c, err := s.store.GetArgosConnector(ctx, t)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && !c.Enabled) {
		return nil, apperr.Conflict(CodeIntegrationNotConfig, "El conector ArgosISP de este ISP no está activo.")
	}
	if err != nil {
		return nil, err
	}
	if c.PeerAPITokenID != nil && *c.PeerAPITokenID != p.APITokenID {
		return nil, apperr.Forbidden(problem.CodePermissionDenied, "Este API token no es el del conector ArgosISP del ISP.")
	}
	if s.baseURL == "" {
		return nil, apperr.Conflict(CodeIntegrationNotConfig, "Configure HORUS_PUBLIC_BASE_URL.")
	}
	tickets, ok := s.eco.Tickets()
	if !ok {
		return nil, errUnavailable("El autologin")
	}
	req.TenantID, req.ConnectorID, req.APITokenID = t.UUID(), c.ID, p.APITokenID
	issued, err := tickets.IssueSSOTicket(ctx, req)
	if err != nil {
		return nil, err
	}
	return &SSOTicketIssued{URL: s.baseURL + ssoEntryPath + "#" + issued.Ticket, ExpiresAt: issued.ExpiresAt}, nil
}

// SSOTarget es el recurso de Horus al que lleva un autologin.
type SSOTarget struct {
	Kind string
	ID   uuid.UUID
}

// ResolveSSOTarget es POST /integrations/argosisp/sso/resolve: aplica el ACL
// del usuario (permiso y alcance por nodo) como si abriera el recurso.
func (s *Integrations) ResolveSSOTarget(ctx context.Context, typ, externalID string) (*SSOTarget, error) {
	p := authz.FromContext(ctx)
	tid, err := authz.TenantOf(ctx)
	if err != nil || p.Type == authz.TypeKiosk {
		return nil, apperr.Forbidden(problem.CodeTokenScopeInvalid, "")
	}
	t := pgdb.TenantID(tid)
	id, ok := cleanText(externalID, maxExternalIDLen)
	if !validTargetType(typ) || !ok {
		return nil, apperr.Validation(apperr.Field("type", "INVALID_VALUE", "cliente, servicio, router o hallazgo con su id."))
	}
	switch typ {
	case ssoTargetCliente, ssoTargetServicio:
		if !p.Has(PermCustomersRead) {
			return nil, apperr.Forbidden(problem.CodePermissionDenied, "")
		}
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil || n < 1 {
			return nil, apperr.NotFound(domain.CodeCustomerNotFound)
		}
		var cs []domain.Customer
		if typ == ssoTargetServicio {
			cs, err = s.store.CustomersByExternal(ctx, t, &n, nil)
		} else {
			cs, err = s.store.CustomersByExternal(ctx, t, nil, &n)
		}
		if err != nil {
			return nil, err
		}
		var best *domain.Customer
		for i := range cs {
			c := &cs[i]
			if siteAllowed(p, PermCustomersRead, c.SiteID) && (best == nil || c.LastSeen.After(best.LastSeen)) {
				best = c
			}
		}
		if best == nil {
			return nil, apperr.NotFound(domain.CodeCustomerNotFound)
		}
		return &SSOTarget{Kind: ssoKindCustomer, ID: best.ID}, nil
	case ssoTargetRouter:
		if !p.Has(PermDevicesRead) {
			return nil, apperr.Forbidden(problem.CodePermissionDenied, "")
		}
		rid, err := uuid.Parse(id)
		if err != nil || s.eco.Router == nil {
			return nil, apperr.NotFound(domain.CodeRouterNotFound)
		}
		r, err := s.eco.Router(ctx, t, rid)
		if errors.Is(err, domain.ErrNotFound) || (err == nil && !siteAllowed(p, PermDevicesRead, r.SiteID)) {
			return nil, apperr.NotFound(domain.CodeRouterNotFound)
		}
		if err != nil {
			return nil, err
		}
		return &SSOTarget{Kind: ssoKindRouter, ID: r.ID}, nil
	default: // hallazgo: el id es el de Horus; la ficha aplica el alcance del hallazgo.
		if !p.Has(PermFindingsRead) {
			return nil, apperr.Forbidden(problem.CodePermissionDenied, "")
		}
		fid, err := uuid.Parse(id)
		if err != nil {
			return nil, apperr.NotFound(CodeSSOTargetNotFound)
		}
		return &SSOTarget{Kind: ssoKindFinding, ID: fid}, nil
	}
}

// OpenArgosInput es el cuerpo de POST /integrations/argosisp/sso/open.
type OpenArgosInput struct {
	CustomerID uuid.UUID `json:"customer_id"`
	Target     *string   `json:"target"`
}

// OpenArgos es "Ver en ArgosISP": el servidor de Horus pide a ArgosISP un
// ticket para el servicio (o cliente) de la identidad vigente del cliente,
// con el token atk_ del conector. El navegador solo recibe la URL.
func (s *Integrations) OpenArgos(ctx context.Context, in OpenArgosInput) (*SSOTicketIssued, error) {
	target := ssoTargetServicio
	if in.Target != nil {
		target = *in.Target
	}
	if target != ssoTargetServicio && target != ssoTargetCliente {
		return nil, apperr.Validation(apperr.Field("target", "INVALID_VALUE", "servicio o cliente."))
	}
	p, t, c, err := s.customer(ctx, PermCustomersRead, in.CustomerID)
	if err != nil {
		return nil, err
	}
	if p.Type != authz.TypeUser || p.UserID == uuid.Nil {
		return nil, apperr.Forbidden(problem.CodePermissionDenied, "Solo una persona abre ArgosISP.")
	}
	conn, err := s.store.GetArgosConnector(ctx, t)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && !conn.Active()) {
		return nil, apperr.Conflict(CodeIntegrationNotConfig, "El conector ArgosISP no está activo.")
	}
	if err != nil {
		return nil, err
	}
	cur, err := s.store.CurrentIdentities(ctx, t, []uuid.UUID{c.ID})
	if err != nil {
		return nil, err
	}
	ident, ok := cur[c.ID]
	var ext *int64
	if ok {
		ext = ident.ExternalServiceID
		if target == ssoTargetCliente {
			ext = ident.ExternalCustomerID
		}
	}
	if ext == nil {
		return nil, apperr.NotFound(CodeExternalIdentityNotFound)
	}
	if s.eco.Peer == nil {
		return nil, errUnavailable("El autologin")
	}
	profiles, ok := s.eco.Profiles()
	if !ok {
		return nil, errUnavailable("El perfil del usuario")
	}
	prof, err := profiles.UserProfile(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{
		"usuario": map[string]any{"id_externo": p.UserID.String(), "correo": prof.Email, "nombre": prof.DisplayName},
		"destino": map[string]any{"tipo": target, "id_externo": strconv.FormatInt(*ext, 10)},
	})
	tok, err := s.sealer.Open(conn.TokenCiphertext, conn.TokenDEK, connAAD(conn, "token"))
	if err != nil {
		return nil, fmt.Errorf("devices: open argos token: %w", err)
	}
	rctx, cancel := context.WithTimeout(ctx, ssoOpenTimeout)
	defer cancel()
	status, raw, err := s.eco.Peer.RequestSSOTicket(rctx, t.UUID(), conn.BaseURL, string(tok), body)
	res, ferr := s.parseArgosTicket(status, raw, err)
	s.record(ctx, t, "integrations.argosisp.sso.opened", "customer", c.ID.String(), map[string]any{"target": target,
		"external_id": *ext, "ok": ferr == nil})
	if ferr != nil {
		return nil, ferr
	}
	return res, nil
}

func (s *Integrations) parseArgosTicket(status int, raw []byte, err error) (*SSOTicketIssued, error) {
	gw := func(detail string) error {
		return apperr.New(apperr.KindBadGateway, CodeIntegrationUnavailble, errSSOUnavailableDetailPrefix+detail)
	}
	switch {
	case err != nil:
		return nil, gw("no respondió.")
	case status == 401 || status == 403:
		return nil, gw("rechazó el token del conector (¿tiene el alcance sso.emitir?).")
	case status == 404:
		return nil, gw("no tiene ese recurso o no ofrece autologin.")
	case status != 200 && status != 201:
		return nil, gw("respondió HTTP " + strconv.Itoa(status) + ".")
	}
	var out struct {
		URL      string     `json:"url"`
		ExpiraEn *time.Time `json:"expira_en"`
	}
	if json.Unmarshal(raw, &out) != nil || !domain.ValidSSOEntryURL(out.URL, s.allowPrivate) {
		return nil, gw("devolvió una URL de acceso no válida.")
	}
	res := &SSOTicketIssued{URL: out.URL}
	if out.ExpiraEn != nil {
		res.ExpiresAt = out.ExpiraEn.UTC()
	}
	return res, nil
}
