package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	alertsapi "github.com/hcdestroyer/horus-flow/services/alerts/api"
	"github.com/hcdestroyer/horus-flow/services/alerts/internal/librenms"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
)

// Códigos de error de la integración con LibreNMS.
const (
	CodeLibreNMSChannelRequired = "LIBRENMS_CHANNEL_REQUIRED"
	CodeLibreNMSNotProvisioned  = "LIBRENMS_NOT_PROVISIONED"
	CodeLibreNMSDeviceNotOwned  = "LIBRENMS_DEVICE_NOT_OWNED"
	codeRouterNotFound          = "ROUTER_NOT_FOUND"
	// SyslogCommand es lo que la persona ejecuta en LibreNMS para guardar el syslog.
	SyslogCommand  = "lnms config:set enable_syslog true"
	librenmsSyncTO = 90 * time.Second
	librenmsTestTO = 25 * time.Second
)

// ErrNotProvisioned: el canal librenms aún no se sincronizó (sin dispositivo
// «Horus Flow» no hay dónde dejar las notificaciones).
var ErrNotProvisioned = errors.New(librenms.CodeNotProvisioned + ": sincronice la integración con LibreNMS (Conectar con LibreNMS)")

// DevicesLookup da acceso perezoso al contrato en proceso de devices.
type DevicesLookup struct {
	Onboarding func() (devapi.Onboarding, bool)
	SNMP       func() (devapi.SNMPCredentialReader, bool)
}

// SetDevices conecta el inventario (lista de routers y credenciales SNMPv3 de Horus).
func (s *Service) SetDevices(d DevicesLookup) { s.devices = d }

func (s *Service) channelLock(id uuid.UUID) *sync.Mutex {
	m, _ := s.syncLocks.LoadOrStore(id, &sync.Mutex{})
	return m.(*sync.Mutex) //nolint:forcetypeassert // solo se guardan *sync.Mutex
}

// defaultHorusHost es el host de HORUS_PUBLIC_BASE_URL.
func (s *Service) defaultHorusHost() string {
	if s.baseURL == "" {
		return ""
	}
	u, err := url.Parse(s.baseURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func (s *Service) recordAs(ctx context.Context, t pgdb.TenantID, action, resType, resID string, changes map[string]any) {
	p := authz.FromContext(ctx)
	rec, ok := s.audit()
	if !ok || p == nil {
		s.logger.WarnContext(ctx, "alerts audit not recorded: auth not local", slog.String("action", action))
		return
	}
	if changes == nil {
		changes = map[string]any{}
	}
	if err := rec.Record(ctx, authapi.AuditEntry{TenantID: t.UUID(), OccurredAt: s.ts(), ActorType: p.Type, ActorID: p.Subject,
		ViaPlatform: p.ViaPlatform, Action: action, ResourceType: resType, ResourceID: resID, Scope: "tenant",
		Outcome: "success", Changes: changes}); err != nil {
		s.logger.ErrorContext(ctx, "alerts audit failed", slog.Any("error", err))
	}
}

// librenmsChannel carga un canal librenms (perm) con su config y credenciales.
func (s *Service) librenmsChannel(ctx context.Context, perm string, id uuid.UUID) (pgdb.TenantID, *Channel, LibreNMSConfig, Credentials, error) {
	t, c, err := s.get(ctx, perm, id)
	if err != nil {
		return t, nil, LibreNMSConfig{}, Credentials{}, err
	}
	if c.Kind != KindLibreNMS {
		return t, nil, LibreNMSConfig{}, Credentials{}, &apperr.Error{Kind: apperr.KindInvalid, Code: CodeLibreNMSChannelRequired,
			Detail: "La integración con LibreNMS solo existe en canales librenms."}
	}
	var lc LibreNMSConfig
	_ = json.Unmarshal(c.Config, &lc)
	cr, err := s.credentials(c)
	return t, c, lc, cr, err
}

func (s *Service) librenmsClient(lc LibreNMSConfig, cr Credentials) *librenms.Client {
	verify := lc.TLSVerify == nil || *lc.TLSVerify
	timeout := time.Duration(lc.TimeoutSeconds) * time.Second
	return librenms.New(librenms.Config{BaseURL: lc.BaseURL, Token: cr.APIToken, Username: lc.Username, Password: cr.Password,
		TLSVerify: verify, Timeout: timeout, HTTPClient: s.senders.client(verify, timeout)})
}

func (s *Service) integrationOf(ctx context.Context, t pgdb.TenantID, channelID uuid.UUID) (*Integration, []librenms.Object, error) {
	in, objs, err := s.st.getIntegration(ctx, t, channelID)
	if errors.Is(err, errNotFound) {
		return &Integration{ChannelID: channelID, TenantID: t.UUID(), CPU: librenms.DefaultCPUThreshold, Memory: librenms.DefaultMemoryThreshold,
			Status: integrationNotProvisioned}, nil, nil
	}
	return in, objs, err
}

// IntegrationView es LibreNmsIntegration.
type IntegrationView struct {
	Integration
	Objects     []librenms.Object
	DefaultHost string
}

// LibreNMSIntegration es GET …/librenms.
func (s *Service) LibreNMSIntegration(ctx context.Context, channelID uuid.UUID) (*IntegrationView, error) {
	t, _, _, _, err := s.librenmsChannel(ctx, PermRead, channelID)
	if err != nil {
		return nil, err
	}
	in, objs, err := s.integrationOf(ctx, t, channelID)
	if err != nil {
		return nil, err
	}
	return &IntegrationView{Integration: *in, Objects: objs, DefaultHost: s.defaultHorusHost()}, nil
}

// SyncInput es LibreNmsSyncInput.
type SyncInput struct {
	HorusHostname *string `json:"horus_hostname"`
	CPUThreshold  *int    `json:"cpu_threshold"`
	MemThreshold  *int    `json:"memory_threshold"`
}

// SyncOutcome es LibreNmsSyncResult.
type SyncOutcome struct {
	OK        bool
	ErrorCode *string
	Error     *string
	Steps     []librenms.Step
	Warnings  []string
	View      *IntegrationView
}

var hostRe = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

func validHost(h string) bool {
	if _, err := netip.ParseAddr(h); err == nil {
		return true
	}
	return len(h) <= 253 && hostRe.MatchString(h)
}

// routerTargets arma los routers del plan: los del tenant con IP de gestión.
func (s *Service) routerTargets(ctx context.Context, t pgdb.TenantID) ([]librenms.Target, []string, error) {
	cfgs, err := s.st.listRouterConfigs(ctx, t)
	if err != nil {
		return nil, nil, err
	}
	targets := []librenms.Target{}
	if len(cfgs) == 0 {
		return targets, nil, nil
	}
	var warnings []string
	names := map[uuid.UUID]string{}
	if s.devices.Onboarding != nil {
		if dev, ok := s.devices.Onboarding(); ok {
			for _, cfg := range cfgs {
				r, err := dev.GetRouter(ctx, t.UUID(), cfg.RouterID)
				if err == nil {
					names[cfg.RouterID] = r.Name
				}
			}
		}
	}
	for i := range cfgs {
		cfg := &cfgs[i]
		name, ok := names[cfg.RouterID]
		if !ok {
			name = "Router " + cfg.RouterID.String()[:8]
		}
		tg := librenms.Target{Key: cfg.RouterID.String(), Name: name, Host: cfg.MgmtIP}
		switch cfg.Mode {
		case librenms.ModeSNMPv2c, librenms.ModeSNMPv3:
			sc, err := s.routerSecret(cfg)
			if err != nil || (sc.Community == "" && sc.V3Username == "") {
				warnings = append(warnings, fmt.Sprintf("«%s»: sin credenciales SNMP para LibreNMS; se monitorea solo por ping.", name))
				break
			}
			if cfg.Mode == librenms.ModeSNMPv2c {
				tg.Community = sc.Community
			} else {
				tg.V3 = sc.v3()
			}
		case librenms.ModeHorusSNMP3:
			if s.devices.SNMP == nil {
				warnings = append(warnings, fmt.Sprintf("«%s»: credenciales SNMPv3 de Horus no disponibles en este proceso; solo ping.", name))
				break
			}
			rd, ok := s.devices.SNMP()
			if !ok {
				warnings = append(warnings, fmt.Sprintf("«%s»: credenciales SNMPv3 de Horus no disponibles en este proceso; solo ping.", name))
				break
			}
			c, err := rd.RevealSNMPCredential(ctx, t.UUID(), cfg.RouterID)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("«%s»: el router no tiene credenciales SNMPv3 de Horus; solo ping.", name))
				break
			}
			tg.V3 = horusV3(c)
		}
		targets = append(targets, tg)
	}
	return targets, warnings, nil
}

func horusV3(c devapi.SNMPCredential) *librenms.SNMPv3 {
	auth := map[string]string{"sha1": "SHA", "sha256": "SHA-256", "md5": "MD5"}[strings.ToLower(c.AuthProtocol)]
	if auth == "" {
		auth = "SHA"
	}
	v := &librenms.SNMPv3{AuthLevel: "authNoPriv", AuthName: c.Username, AuthPass: c.AuthPassword, AuthAlgo: auth}
	if c.PrivPassword != "" {
		v.AuthLevel, v.CryptoPass, v.CryptoAlgo = "authPriv", c.PrivPassword, "AES"
		if strings.EqualFold(c.PrivProtocol, "des") {
			v.CryptoAlgo = "DES"
		}
	}
	return v
}

// LibreNMSSync es POST …/librenms/sync.
func (s *Service) LibreNMSSync(ctx context.Context, channelID uuid.UUID, in SyncInput) (*SyncOutcome, error) {
	t, c, lc, cr, err := s.librenmsChannel(ctx, PermManage, channelID)
	if err != nil {
		return nil, err
	}
	var f fieldErrs
	if in.HorusHostname != nil {
		h := strings.TrimSpace(*in.HorusHostname)
		in.HorusHostname = &h
		if !validHost(h) {
			f.add("horus_hostname", "INVALID_FORMAT", "Nombre de host o IP sin esquema ni puerto.")
		}
	}
	for field, v := range map[string]*int{"cpu_threshold": in.CPUThreshold, "memory_threshold": in.MemThreshold} {
		if v != nil && (*v < 50 || *v > 99) {
			f.add(field, "OUT_OF_RANGE", "Entre 50 y 99.")
		}
	}
	if err := f.err(); err != nil {
		return nil, err
	}
	mu := s.channelLock(channelID)
	mu.Lock()
	defer mu.Unlock()
	integ, objs, err := s.integrationOf(ctx, t, channelID)
	if err != nil {
		return nil, err
	}
	if in.HorusHostname != nil {
		integ.HorusHostname = in.HorusHostname
	}
	if integ.HorusHostname == nil || *integ.HorusHostname == "" {
		if h := s.defaultHorusHost(); h != "" {
			integ.HorusHostname = &h
		}
	}
	if in.CPUThreshold != nil {
		integ.CPU = *in.CPUThreshold
	}
	if in.MemThreshold != nil {
		integ.Memory = *in.MemThreshold
	}
	now := s.ts()
	integ.LastSyncAt = &now
	out := &SyncOutcome{}
	fail := func(code, msg string) (*SyncOutcome, error) {
		integ.Status, integ.LastErrorCode, integ.LastError = integrationFailed, &code, truncate(msg)
		integ.Steps, integ.Warnings = nil, nil
		if err := s.st.saveIntegration(context.WithoutCancel(ctx), t, integ, nil); err != nil {
			return nil, err
		}
		out.ErrorCode, out.Error = integ.LastErrorCode, integ.LastError
		out.View = &IntegrationView{Integration: *integ, Objects: objs, DefaultHost: s.defaultHorusHost()}
		return out, nil
	}
	if cr.APIToken == "" && cr.Password == "" {
		return fail(librenms.CodeMissingCredentials, "Configure el token de API de LibreNMS en el canal (credenciales).")
	}
	routers, warnings, err := s.routerTargets(ctx, t)
	if err != nil {
		return nil, err
	}
	host := ""
	if integ.HorusHostname != nil {
		host = *integ.HorusHostname
	}
	sctx, cancel := context.WithTimeout(ctx, librenmsSyncTO)
	defer cancel()
	cl := s.librenmsClient(lc, cr)
	res := librenms.NewProvisioner(cl).Sync(sctx, objs, librenms.Plan{HorusHost: host,
		Thresholds: librenms.Thresholds{CPU: integ.CPU, Memory: integ.Memory}, Routers: routers})
	if res.Err != nil {
		msg := res.Err.Message
		if msg == "" {
			msg = res.Err.Error()
		}
		return fail(res.Err.Code, msg)
	}
	res.Warnings = append(warnings, res.Warnings...)
	if dev, ok := res.Find(librenms.KindDevice, librenms.KeyHorusDevice); ok && dev.RemoteID != 0 {
		if off, err := cl.Calibrate(sctx, dev.RemoteID, s.now); err == nil {
			integ.TZOffset = &off
		} else {
			res.Warnings = append(res.Warnings, "No se pudo medir la hora de LibreNMS (se asume UTC): "+librenms.CodeOf(err)+".")
		}
	}
	if res.Version != "" {
		integ.RemoteVersion = &res.Version
	}
	integ.Status, integ.LastErrorCode, integ.LastError = integrationOK, nil, nil
	if n := res.Failed(); n > 0 {
		integ.Status = integrationPartial
		code := "partial"
		for _, st := range res.Steps {
			if st.Action == librenms.ActionFailed {
				code = st.Code
				break
			}
		}
		integ.LastErrorCode, integ.LastError = &code, truncate(fmt.Sprintf("%d objeto(s) no se pudieron sincronizar.", n))
	}
	integ.Steps, integ.Warnings = res.Steps, res.Warnings
	if err := s.st.saveIntegration(context.WithoutCancel(ctx), t, integ, res.Objects); err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, st := range res.Steps {
		counts[st.Action]++
	}
	s.record(ctx, t, "alerts.librenms.synced", c, map[string]any{"status": integ.Status, "actions": counts,
		"horus_hostname": host, "cpu_threshold": integ.CPU, "memory_threshold": integ.Memory})
	if lc.TLSVerify != nil && !*lc.TLSVerify {
		s.record(ctx, t, "alerts.channel.connection_test.tls_unverified", c, nil)
	}
	out.OK = integ.Status == integrationOK
	out.ErrorCode, out.Error = integ.LastErrorCode, integ.LastError
	out.Steps, out.Warnings = res.Steps, res.Warnings
	out.View = &IntegrationView{Integration: *integ, Objects: res.Objects, DefaultHost: s.defaultHorusHost()}
	return out, nil
}

func horusTarget(integ *Integration, objs []librenms.Object) (librenms.HorusTarget, librenms.ID, bool) {
	var h librenms.HorusTarget
	var rule librenms.ID
	for _, o := range objs {
		switch {
		case o.Kind == librenms.KindDevice && o.Key == librenms.KeyHorusDevice:
			h.Host, h.DeviceID = o.Hostname, o.RemoteID
		case o.Kind == librenms.KindRule && o.Key == librenms.KeyHorusEvent:
			rule = o.RemoteID
		}
	}
	if integ.TZOffset != nil {
		h.OffsetMinutes = *integ.TZOffset
	}
	return h, rule, h.DeviceID != 0 && h.Host != ""
}

// LibreNMSTest es POST …/librenms/test: prueba de extremo a extremo.
func (s *Service) LibreNMSTest(ctx context.Context, channelID uuid.UUID) (*LibreTestReport, error) {
	t, c, lc, cr, err := s.librenmsChannel(ctx, PermManage, channelID)
	if err != nil {
		return nil, err
	}
	integ, objs, err := s.integrationOf(ctx, t, channelID)
	if err != nil {
		return nil, err
	}
	h, rule, ok := horusTarget(integ, objs)
	if !ok || rule == 0 {
		return nil, apperr.Conflict(CodeLibreNMSNotProvisioned, "Primero conecte Horus con LibreNMS (Sincronizar).")
	}
	tctx, cancel := context.WithTimeout(ctx, librenmsTestTO)
	defer cancel()
	start := time.Now()
	ev := librenms.Event{Severity: "info", EventType: "test", Subject: "Prueba de integración de Horus Flow",
		Text: "Si ves esta alerta en LibreNMS, la integración funciona (se resuelve sola en 5 minutos).", Link: s.baseURL,
		DeliveryID: uuid.Must(uuid.NewV7()).String()}
	var r librenms.TestResult
	if cr.APIToken == "" && cr.Password == "" {
		r = librenms.TestResult{FailedStep: librenms.StepAuth, Code: librenms.CodeMissingCredentials,
			Error: "Configure el token de API de LibreNMS en el canal (credenciales).",
			Steps: []librenms.TestStep{{Step: librenms.StepAuth, Code: librenms.CodeMissingCredentials}}}
	} else {
		r = s.librenmsClient(lc, cr).EndToEnd(tctx, h, rule, ev, librenms.TestOptions{Now: s.now})
	}
	ms := int(time.Since(start).Milliseconds())
	rep := &LibreTestReport{OK: r.OK, ChannelID: c.ID, CheckedAt: ts(s.ts()), LatencyMS: &ms, Steps: r.Steps}
	if r.Steps == nil {
		rep.Steps = []librenms.TestStep{}
	}
	if r.Version != "" {
		rep.RemoteVersion = &r.Version
	}
	if r.Line != "" {
		rep.Line = &r.Line
	}
	if !r.OK {
		rep.FailedStep, rep.ErrorCode, rep.Error = &r.FailedStep, &r.Code, truncate(r.Error)
	}
	integ.LastTest = rep
	if err := s.st.saveIntegration(context.WithoutCancel(ctx), t, integ, nil); err != nil {
		return nil, err
	}
	status := statusOK
	if !r.OK {
		status = statusFailing
	}
	_ = s.st.recordStatus(context.WithoutCancel(ctx), t, c.ID, status, rep.Error, false)
	return rep, nil
}

// LibreNMSDeleteRouterDevice borra de LibreNMS el dispositivo que Horus creó
// para un router (solo por acción explícita de la persona).
func (s *Service) LibreNMSDeleteRouterDevice(ctx context.Context, channelID, routerID uuid.UUID) error {
	t, c, lc, cr, err := s.librenmsChannel(ctx, PermManage, channelID)
	if err != nil {
		return err
	}
	mu := s.channelLock(channelID)
	mu.Lock()
	defer mu.Unlock()
	_, objs, err := s.integrationOf(ctx, t, channelID)
	if err != nil {
		return err
	}
	idx := slices.IndexFunc(objs, func(o librenms.Object) bool {
		return o.Kind == librenms.KindRouterDevice && o.Key == routerID.String()
	})
	if idx < 0 {
		return apperr.NotFound(problem.CodeNotFound)
	}
	o := objs[idx]
	if !o.CreatedByHorus {
		return apperr.Conflict(CodeLibreNMSDeviceNotOwned, "Ese dispositivo ya existía en LibreNMS: Horus no lo borra.")
	}
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := s.librenmsClient(lc, cr).DeleteDevice(dctx, o.RemoteID); err != nil && librenms.CodeOf(err) != librenms.CodeNotFound {
		e, _ := librenms.AsError(err)
		detail := "LibreNMS rechazó el borrado."
		if e != nil && e.Message != "" {
			detail += " " + e.Message
		}
		return apperr.New(apperr.KindBadGateway, CodeChannelUnreachable, detail)
	}
	if err := s.st.deleteObject(ctx, t, channelID, librenms.KindRouterDevice, o.Key); err != nil {
		return err
	}
	s.record(ctx, t, "alerts.librenms.device.deleted", c, map[string]any{"router_id": routerID.String(), "remote_id": int64(o.RemoteID)})
	return nil
}

// ---------------------------------------------------------------- routers

// RouterView es LibreNmsRouter.
type RouterView struct {
	Router devapi.RouterInfo
	Config *RouterConfig
}

func (s *Service) onboarding() (devapi.Onboarding, error) {
	if s.devices.Onboarding != nil {
		if d, ok := s.devices.Onboarding(); ok {
			return d, nil
		}
	}
	return nil, apperr.New(apperr.KindUnavailable, problem.CodeServiceUnavailable, "El inventario (devices) no está disponible en este proceso.")
}

// LibreNMSRouters es GET /librenms/routers.
func (s *Service) LibreNMSRouters(ctx context.Context) ([]RouterView, error) {
	_, t, err := scope(ctx, PermRead)
	if err != nil {
		return nil, err
	}
	dev, err := s.onboarding()
	if err != nil {
		return nil, err
	}
	all, err := dev.ListRouters(ctx)
	if err != nil {
		return nil, fmt.Errorf("alerts: list routers: %w", err)
	}
	cfgs, err := s.st.listRouterConfigs(ctx, t)
	if err != nil {
		return nil, err
	}
	byID := map[uuid.UUID]*RouterConfig{}
	for i := range cfgs {
		byID[cfgs[i].RouterID] = &cfgs[i]
	}
	out := []RouterView{}
	for _, r := range all {
		if r.TenantID != t.UUID() {
			continue
		}
		out = append(out, RouterView{Router: r, Config: byID[r.ID]})
	}
	slices.SortFunc(out, func(a, b RouterView) int { return strings.Compare(a.Router.Name, b.Router.Name) })
	return out, nil
}

// RouterInput es LibreNmsRouterInput.
type RouterInput struct {
	MgmtIP  string `json:"mgmt_ip"`
	Mode    string `json:"mode"`
	Consent bool   `json:"reuse_horus_snmp_consent"`
}

func (s *Service) tenantRouter(ctx context.Context, t pgdb.TenantID, routerID uuid.UUID) (devapi.RouterInfo, error) {
	dev, err := s.onboarding()
	if err != nil {
		return devapi.RouterInfo{}, err
	}
	r, err := dev.GetRouter(ctx, t.UUID(), routerID)
	if errors.Is(err, devapi.ErrRouterNotFound) {
		return r, apperr.NotFound(codeRouterNotFound)
	}
	if err != nil {
		return r, fmt.Errorf("alerts: get router: %w", err)
	}
	return r, nil
}

// SetLibreNMSRouter es PUT /librenms/routers/{id}.
func (s *Service) SetLibreNMSRouter(ctx context.Context, routerID uuid.UUID, in RouterInput) (*RouterView, error) {
	p, t, err := scope(ctx, PermManage)
	if err != nil {
		return nil, err
	}
	var f fieldErrs
	ip, ipErr := netip.ParseAddr(strings.TrimSpace(in.MgmtIP))
	if ipErr != nil || ip.IsUnspecified() || ip.IsMulticast() || ip.Zone() != "" {
		f.add("mgmt_ip", "INVALID_FORMAT", "IPv4 o IPv6 de gestión alcanzable desde LibreNMS.")
	}
	if !slices.Contains([]string{librenms.ModePing, librenms.ModeSNMPv2c, librenms.ModeSNMPv3, librenms.ModeHorusSNMP3}, in.Mode) {
		f.add("mode", "INVALID_VALUE", "ping, snmp_v2c, snmp_v3 o horus_snmp_v3.")
	}
	if in.Mode == librenms.ModeHorusSNMP3 && !in.Consent {
		f.add("reuse_horus_snmp_consent", "REQUIRED", "Confirme que LibreNMS usará las credenciales SNMPv3 de Horus para este router.")
	}
	if err := f.err(); err != nil {
		return nil, err
	}
	r, err := s.tenantRouter(ctx, t, routerID)
	if err != nil {
		return nil, err
	}
	cfg, err := s.st.getRouterConfig(ctx, t, routerID)
	if errors.Is(err, errNotFound) {
		cfg, err = &RouterConfig{TenantID: t.UUID(), RouterID: routerID}, nil
	}
	if err != nil {
		return nil, err
	}
	prevMode, prevIP := cfg.Mode, cfg.MgmtIP
	if cfg.Mode != in.Mode {
		cfg.SecretCiphertext, cfg.DEKWrapped, cfg.KEKID = nil, nil, nil // credenciales de otro modo
	}
	cfg.MgmtIP, cfg.Mode = ip.String(), in.Mode
	consented := false
	if in.Mode == librenms.ModeHorusSNMP3 {
		if cfg.ConsentAt == nil {
			now := s.ts()
			by := p.Subject
			cfg.ConsentAt, cfg.ConsentBy, consented = &now, &by, true
		}
	} else {
		cfg.ConsentAt, cfg.ConsentBy = nil, nil
	}
	if err := s.st.upsertRouterConfig(ctx, t, cfg); err != nil {
		return nil, err
	}
	s.recordAs(ctx, t, "alerts.librenms.router.updated", "router", routerID.String(), map[string]any{"mode": in.Mode,
		"previous_mode": prevMode, "mgmt_ip_changed": prevIP != cfg.MgmtIP})
	if consented {
		s.recordAs(ctx, t, "alerts.librenms.router.horus_snmp_consented", "router", routerID.String(), map[string]any{"mode": in.Mode})
	}
	return &RouterView{Router: r, Config: cfg}, nil
}

// ClearLibreNMSRouter es DELETE /librenms/routers/{id}.
func (s *Service) ClearLibreNMSRouter(ctx context.Context, routerID uuid.UUID) error {
	_, t, err := scope(ctx, PermManage)
	if err != nil {
		return err
	}
	if _, err := s.tenantRouter(ctx, t, routerID); err != nil {
		return err
	}
	if err := s.st.deleteRouterConfig(ctx, t, routerID); err != nil {
		return err
	}
	s.recordAs(ctx, t, "alerts.librenms.router.cleared", "router", routerID.String(), nil)
	return nil
}

// RouterSecret son las credenciales SNMP propias para LibreNMS (LibreNmsSnmpCredentials).
type RouterSecret struct {
	Community      string `json:"community,omitempty"`
	V3AuthLevel    string `json:"v3_auth_level,omitempty"`
	V3Username     string `json:"v3_username,omitempty"`
	V3AuthProtocol string `json:"v3_auth_protocol,omitempty"`
	V3AuthPassword string `json:"v3_auth_password,omitempty"`
	V3PrivProtocol string `json:"v3_priv_protocol,omitempty"`
	V3PrivPassword string `json:"v3_priv_password,omitempty"`
}

func (r RouterSecret) v3() *librenms.SNMPv3 {
	v := &librenms.SNMPv3{AuthLevel: r.V3AuthLevel, AuthName: r.V3Username}
	if r.V3AuthLevel != "noAuthNoPriv" {
		v.AuthPass, v.AuthAlgo = r.V3AuthPassword, r.V3AuthProtocol
	}
	if r.V3AuthLevel == "authPriv" {
		v.CryptoPass, v.CryptoAlgo = r.V3PrivPassword, r.V3PrivProtocol
	}
	return v
}

func routerAAD(c *RouterConfig) []byte {
	return []byte("alerts_librenms_router:" + c.TenantID.String() + ":" + c.RouterID.String())
}

func (s *Service) routerSecret(c *RouterConfig) (RouterSecret, error) {
	var sec RouterSecret
	if !c.HasSecret() {
		return sec, nil
	}
	plain, err := s.sealer.Open(c.SecretCiphertext, c.DEKWrapped, routerAAD(c))
	if err != nil {
		return sec, fmt.Errorf("alerts: open router snmp: %w", err)
	}
	if err := json.Unmarshal(plain, &sec); err != nil {
		return sec, fmt.Errorf("alerts: router snmp: %w", err)
	}
	return sec, nil
}

var communityRe = regexp.MustCompile(`^[\x21-\x7e]{1,64}$`)

// PutLibreNMSRouterCredentials es PUT /librenms/routers/{id}/snmp-credentials (write-only).
func (s *Service) PutLibreNMSRouterCredentials(ctx context.Context, routerID uuid.UUID, in RouterSecret) error {
	_, t, err := scope(ctx, PermManage)
	if err != nil {
		return err
	}
	cfg, err := s.st.getRouterConfig(ctx, t, routerID)
	if errors.Is(err, errNotFound) {
		if _, rerr := s.tenantRouter(ctx, t, routerID); rerr != nil {
			return rerr
		}
		return apperr.Validation(apperr.Field("mode", "NOT_CONFIGURED", "Primero fije la IP de gestión y el modo SNMP del router."))
	}
	if err != nil {
		return err
	}
	var f fieldErrs
	v3 := in.V3AuthLevel != "" || in.V3Username != "" || in.V3AuthPassword != "" || in.V3PrivPassword != ""
	switch cfg.Mode {
	case librenms.ModeSNMPv2c:
		if v3 {
			f.add("v3_username", "NOT_APPLICABLE", "El modo snmp_v2c solo usa community.")
		}
		if !communityRe.MatchString(in.Community) {
			f.add("community", "INVALID_VALUE", "Community de 1 a 64 caracteres imprimibles sin espacios.")
		}
	case librenms.ModeSNMPv3:
		if in.Community != "" {
			f.add("community", "NOT_APPLICABLE", "El modo snmp_v3 no usa community.")
		}
		if !slices.Contains([]string{"noAuthNoPriv", "authNoPriv", "authPriv"}, in.V3AuthLevel) {
			f.add("v3_auth_level", "INVALID_VALUE", "noAuthNoPriv, authNoPriv o authPriv.")
		}
		if l := len(in.V3Username); l < 1 || l > 64 {
			f.add("v3_username", "INVALID_VALUE", "Usuario de 1 a 64 caracteres.")
		}
		if in.V3AuthLevel != "noAuthNoPriv" {
			if !slices.Contains([]string{"MD5", "SHA", "SHA-224", "SHA-256", "SHA-384", "SHA-512"}, in.V3AuthProtocol) {
				f.add("v3_auth_protocol", "INVALID_VALUE", "MD5, SHA, SHA-224, SHA-256, SHA-384 o SHA-512.")
			}
			if l := len(in.V3AuthPassword); l < 8 || l > 128 {
				f.add("v3_auth_password", "INVALID_VALUE", "Entre 8 y 128 caracteres.")
			}
		}
		if in.V3AuthLevel == "authPriv" {
			if in.V3PrivProtocol != "AES" && in.V3PrivProtocol != "DES" {
				f.add("v3_priv_protocol", "INVALID_VALUE", "AES o DES.")
			}
			if l := len(in.V3PrivPassword); l < 8 || l > 128 {
				f.add("v3_priv_password", "INVALID_VALUE", "Entre 8 y 128 caracteres.")
			}
		}
	default:
		f.add("mode", "NOT_APPLICABLE", "Solo los modos snmp_v2c y snmp_v3 tienen credenciales propias.")
	}
	if err := f.err(); err != nil {
		return err
	}
	plain, _ := json.Marshal(in)
	ct, dek, err := s.sealer.Seal(plain, routerAAD(cfg))
	if err != nil {
		return fmt.Errorf("alerts: seal: %w", err)
	}
	kek := s.sealer.KEKID()
	cfg.SecretCiphertext, cfg.DEKWrapped, cfg.KEKID = ct, dek, &kek
	if err := s.st.upsertRouterConfig(ctx, t, cfg); err != nil {
		return err
	}
	fields := []string{}
	for k, v := range map[string]string{"community": in.Community, "v3_username": in.V3Username, "v3_auth_password": in.V3AuthPassword,
		"v3_priv_password": in.V3PrivPassword} {
		if v != "" {
			fields = append(fields, k)
		}
	}
	slices.Sort(fields)
	s.recordAs(ctx, t, "alerts.librenms.router.credentials.updated", "router", routerID.String(), map[string]any{"fields": fields})
	return nil
}

// ---------------------------------------------------------------- entrega e infraestructura

// sendLibreNMS deja la notificación como línea de syslog del dispositivo
// «Horus Flow» (y en su registro de eventos, best-effort).
func (s *Service) sendLibreNMS(ctx context.Context, t pgdb.TenantID, c *Channel, cr Credentials, m Message, eventType, severity string) error {
	if cr.APIToken == "" && cr.Password == "" {
		return errors.New("librenms: missing credentials")
	}
	var lc LibreNMSConfig
	_ = json.Unmarshal(c.Config, &lc)
	integ, objs, err := s.integrationOf(ctx, t, c.ID)
	if err != nil {
		return err
	}
	h, _, ok := horusTarget(integ, objs)
	if !ok {
		return ErrNotProvisioned
	}
	if eventType == "" {
		eventType = "test"
	}
	_, err = s.librenmsClient(lc, cr).SendEvent(ctx, h, librenms.Event{Severity: severity, EventType: eventType, Subject: m.Subject,
		Text: m.Text, Link: m.Link, DeliveryID: m.DeliveryID}, s.now())
	if err != nil {
		return fmt.Errorf("librenms: %w", err)
	}
	return nil
}

// InfraTarget es un servicio de infraestructura a vigilar por ping en
// LibreNMS (gancho del inventario de servicios).
type InfraTarget struct {
	Name string
	IP   string
}

// SyncInfrastructure mantiene el grupo «Horus · Infraestructura» de cada
// canal librenms aprovisionado del tenant con exactamente estos servicios
// (los que salen se quitan del grupo, nunca se borran de LibreNMS). Lo
// llama el inventario de servicios de infraestructura (sin principal: es un
// proceso interno con el tenant explícito).
func (s *Service) SyncInfrastructure(ctx context.Context, tenant uuid.UUID, items []InfraTarget) ([]librenms.Step, error) {
	t := pgdb.TenantID(tenant)
	targets := make([]librenms.Target, 0, len(items))
	seen := map[string]bool{}
	for _, it := range items {
		name := strings.TrimSpace(it.Name)
		ip, err := netip.ParseAddr(strings.TrimSpace(it.IP))
		if name == "" || err != nil || seen[name] {
			return nil, fmt.Errorf("alerts: infra target %q: nombre único e IP válida", it.Name)
		}
		seen[name] = true
		targets = append(targets, librenms.Target{Key: name, Name: name, Host: ip.String()})
	}
	channels, err := s.st.listChannels(ctx, t, nil, uuid.Nil, 1000, true)
	if err != nil {
		return nil, err
	}
	var steps []librenms.Step
	var errs []error
	for i := range channels {
		c := &channels[i]
		if c.Kind != KindLibreNMS {
			continue
		}
		integ, objs, err := s.integrationOf(ctx, t, c.ID)
		if err != nil || integ.Status == integrationNotProvisioned {
			continue
		}
		cr, err := s.credentials(c)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		var lc LibreNMSConfig
		_ = json.Unmarshal(c.Config, &lc)
		mu := s.channelLock(c.ID)
		mu.Lock()
		sctx, cancel := context.WithTimeout(ctx, librenmsSyncTO)
		res := librenms.NewProvisioner(s.librenmsClient(lc, cr)).Sync(sctx, objs, librenms.Plan{InfraOnly: true, Infra: targets})
		cancel()
		if res.Err != nil {
			mu.Unlock()
			errs = append(errs, res.Err)
			continue
		}
		err = s.st.saveIntegration(context.WithoutCancel(ctx), t, integ, res.Objects)
		mu.Unlock()
		if err != nil {
			errs = append(errs, err)
		}
		steps = append(steps, res.Steps...)
	}
	return steps, errors.Join(errs...)
}

// InfraHook adapta SyncInfrastructure al contrato público alertsapi.LibreNMSInfrastructure.
type InfraHook struct{ Svc *Service }

// SyncInfrastructure implementa alertsapi.LibreNMSInfrastructure.
func (h InfraHook) SyncInfrastructure(ctx context.Context, tenant uuid.UUID, services []alertsapi.InfraService) ([]alertsapi.InfraSyncStep, error) {
	items := make([]InfraTarget, 0, len(services))
	for _, it := range services {
		items = append(items, InfraTarget{Name: it.Name, IP: it.IP})
	}
	steps, err := h.Svc.SyncInfrastructure(ctx, tenant, items)
	out := make([]alertsapi.InfraSyncStep, 0, len(steps))
	for _, st := range steps {
		var id int64
		if st.RemoteID != nil {
			id = *st.RemoteID
		}
		out = append(out, alertsapi.InfraSyncStep{Kind: st.Kind, Key: st.Key, Name: st.Name, Action: st.Action, RemoteID: id, Error: st.Error})
	}
	return out, err
}
