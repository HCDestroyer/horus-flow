// Package vici habla con el demonio IKEv2 de strongSwan (charon) por su
// socket vici (https://docs.strongswan.org/docs/latest/plugins/vici.html):
// carga una conexión route-based por túnel (IKEv2, PSK, if_id de la interfaz
// XFRM, DPD, rekey y NAT-T automático) y su PSK solo en la memoria del
// demonio (load-shared), lista conexiones y SA. El demonio vive en su propio
// contenedor (horus-ipsec) sin certificados gRPC ni base de datos; el agente
// no expone nada a Internet.
package vici

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	govici "github.com/strongswan/govici/vici"

	"github.com/hcdestroyer/horus-flow/services/tunnelagent/internal/app"
	"github.com/hcdestroyer/horus-flow/services/tunnelagent/internal/domain"
)

// Propuestas (docs/tunnels.md §4.2), en orden de preferencia del hub:
// AES-256-GCM con PRF SHA-384 y ECP-384 (RFC 6379, Suite B); AES-256-CBC +
// SHA-256 con ECP-384 (RouterOS: su perfil IKE no ofrece GCM); y de respaldo
// AES-256-CBC + SHA-256 con MODP-2048 (grupo 14) para VRP antiguos sin
// curvas elípticas. Nada de SHA-1, MD5, 3DES ni grupos DH < 2048. El grupo
// en ESP es el de PFS en cada rekey de la CHILD SA.
var (
	IKEProposals = []string{"aes256gcm16-prfsha384-ecp384", "aes256-sha256-ecp384", "aes256-sha256-modp2048"}
	ESPProposals = []string{"aes256gcm16-ecp384", "aes256-sha256-ecp384", "aes256-sha256-modp2048"}
)

// Tiempos de la conexión: DPD cada 30 s (se cierra la SA del lado del hub
// si el router no responde: él la vuelve a iniciar), rekey de IKE cada 4 h
// y de la CHILD SA cada hora (con margen y aleatorización por defecto).
const (
	DPDDelay      = "30s"
	IKERekeyTime  = "4h"
	ESPRekeyTime  = "1h"
	DefaultSocket = "/run/horus-ipsec/charon.vici"
)

// Caller es la parte de govici que se usa (sustituible en tests).
type Caller interface {
	Call(ctx context.Context, cmd string, in *govici.Message) (*govici.Message, error)
	StreamedSAs(ctx context.Context) ([]*govici.Message, error)
	Close() error
}

type session struct{ s *govici.Session }

func (s session) Call(ctx context.Context, cmd string, in *govici.Message) (*govici.Message, error) {
	return s.s.Call(ctx, cmd, in) //nolint:wrapcheck // error de vici tal cual
}

func (s session) StreamedSAs(ctx context.Context) ([]*govici.Message, error) {
	var out []*govici.Message
	for m, err := range s.s.CallStreaming(ctx, "list-sas", "list-sa", govici.NewMessage()) {
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func (s session) Close() error { return s.s.Close() } //nolint:wrapcheck // cierre

// Daemon es el adaptador. La sesión se abre en el primer uso y se reabre
// si el demonio se reinicia.
type Daemon struct {
	mu     sync.Mutex
	socket string
	dial   func(socket string) (Caller, error)
	c      Caller
	now    func() time.Time
}

var _ app.IKE = (*Daemon)(nil)

// New crea el adaptador sobre el socket vici.
func New(socket string) *Daemon {
	if socket == "" {
		socket = DefaultSocket
	}
	return &Daemon{socket: socket, now: time.Now, dial: func(path string) (Caller, error) {
		s, err := govici.NewSession(govici.WithSocketPath(path))
		if err != nil {
			return nil, err //nolint:wrapcheck // se envuelve en conn()
		}
		return session{s: s}, nil
	}}
}

// NewWithCaller crea el adaptador con un Caller ya abierto (tests).
func NewWithCaller(c Caller, now func() time.Time) *Daemon {
	return &Daemon{c: c, now: now, dial: func(string) (Caller, error) { return nil, errors.New("vici: no dialer") }}
}

func (d *Daemon) conn() (Caller, error) {
	if d.c != nil {
		return d.c, nil
	}
	c, err := d.dial(d.socket)
	if err != nil {
		return nil, fmt.Errorf("vici %s: %w", d.socket, err)
	}
	d.c = c
	return c, nil
}

// call ejecuta cmd; ante un error de transporte cierra la sesión para
// reabrirla en la siguiente llamada (el demonio pudo reiniciarse).
func (d *Daemon) call(ctx context.Context, cmd string, in *govici.Message) (*govici.Message, error) {
	c, err := d.conn()
	if err != nil {
		return nil, err
	}
	out, err := c.Call(ctx, cmd, in)
	if err != nil && out == nil {
		_ = c.Close()
		d.c = nil
	}
	if err != nil {
		return out, fmt.Errorf("vici %s: %w", cmd, err)
	}
	return out, nil
}

// ConnMessage construye el mensaje load-conn de un túnel (exportado para
// las pruebas y la documentación).
func ConnMessage(t domain.Tunnel) (*govici.Message, error) {
	name := t.ConnName()
	remote := "%any"
	if t.Remote.IsValid() {
		remote = t.Remote.String()
	}
	ifid := strconv.FormatUint(uint64(t.Index), 10)
	child := map[string]any{
		// Route-based: selectores 0/0 (lo que esperan VRP «tunnel-protocol
		// ipsec» y RouterOS con políticas 0/0); lo que entra por la interfaz
		// lo restringe el cortafuegos (horus-tunnel-src: solo la /32 del
		// router por su hxfrm) y la ruta /32.
		"local_ts":      []string{"0.0.0.0/0"},
		"remote_ts":     []string{"0.0.0.0/0"},
		"esp_proposals": ESPProposals,
		"if_id_in":      ifid,
		"if_id_out":     ifid,
		"mode":          "tunnel",
		"rekey_time":    ESPRekeyTime,
		"dpd_action":    "clear",
		"start_action":  "none",
		"close_action":  "none",
	}
	conn := map[string]any{
		"version":      "2",
		"local_addrs":  []string{"%any"},
		"remote_addrs": []string{remote},
		"proposals":    IKEProposals,
		"dpd_delay":    DPDDelay,
		"rekey_time":   IKERekeyTime,
		// NAT-T automático (RFC 3948): UDP 4500 si hay NAT en el camino.
		"encap":  "no",
		"mobike": "no",
		// Un router que reinicia y vuelve a iniciar reemplaza su IKE SA vieja.
		"unique":   "replace",
		"if_id_in": ifid, "if_id_out": ifid,
		"local":    map[string]any{"auth": "psk", "id": t.LocalID},
		"remote":   map[string]any{"auth": "psk", "id": t.RemoteID},
		"children": map[string]any{name: child},
	}
	m := govici.NewMessage()
	if err := m.Set(name, conn); err != nil {
		return nil, fmt.Errorf("vici: conn %s: %w", name, err)
	}
	return m, nil
}

// SharedMessage construye el mensaje load-shared (la PSK solo viaja aquí).
func SharedMessage(t domain.Tunnel) (*govici.Message, error) {
	m := govici.NewMessage()
	for k, v := range map[string]any{"id": t.ConnName(), "type": "IKE", "data": t.PSK, "owners": []string{t.LocalID, t.RemoteID}} {
		if err := m.Set(k, v); err != nil {
			return nil, fmt.Errorf("vici: shared: %w", err)
		}
	}
	return m, nil
}

// Load implementa app.IKE.
func (d *Daemon) Load(ctx context.Context, t domain.Tunnel) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	sh, err := SharedMessage(t)
	if err != nil {
		return err
	}
	if _, err := d.call(ctx, "load-shared", sh); err != nil {
		return err // sin la PSK en el mensaje de error
	}
	cm, err := ConnMessage(t)
	if err != nil {
		return err
	}
	_, err = d.call(ctx, "load-conn", cm)
	return err
}

func named(k, v string) (*govici.Message, error) {
	m := govici.NewMessage()
	if err := m.Set(k, v); err != nil {
		return nil, fmt.Errorf("vici: %w", err)
	}
	return m, nil
}

// Unload implementa app.IKE: descarga la conexión (las SA nuevas ya no se
// aceptan), cierra la IKE SA vigente y borra la PSK.
func (d *Daemon) Unload(ctx context.Context, index uint32) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	name := domain.ConnName(index)
	var errs []error
	if m, err := named("name", name); err == nil {
		if _, err := d.call(ctx, "unload-conn", m); err != nil && !notFound(err) {
			errs = append(errs, err)
		}
	}
	if m, err := named("ike", name); err == nil {
		_ = m.Set("force", "yes")
		_ = m.Set("timeout", "-1")
		if _, err := d.call(ctx, "terminate", m); err != nil && !notFound(err) && !noMatch(err) {
			errs = append(errs, err)
		}
	}
	if m, err := named("id", name); err == nil {
		if _, err := d.call(ctx, "unload-shared", m); err != nil && !notFound(err) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func notFound(err error) bool { return err != nil && strings.Contains(err.Error(), "not found") }
func noMatch(err error) bool  { return err != nil && strings.Contains(err.Error(), "no matching") }

// Conns implementa app.IKE.
func (d *Daemon) Conns(ctx context.Context) ([]uint32, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out, err := d.call(ctx, "get-conns", govici.NewMessage())
	if err != nil {
		return nil, err
	}
	var idxs []uint32
	if names, ok := out.Get("conns").([]string); ok {
		for _, n := range names {
			if idx, ok := domain.ParseConnName(n); ok {
				idxs = append(idxs, idx)
			}
		}
	}
	return idxs, nil
}

// SAs implementa app.IKE (list-sas: tiempos relativos en segundos).
func (d *Daemon) SAs(ctx context.Context) (map[uint32]app.SA, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, err := d.conn()
	if err != nil {
		return nil, err
	}
	msgs, err := c.StreamedSAs(ctx)
	if err != nil {
		_ = c.Close()
		d.c = nil
		return nil, fmt.Errorf("vici list-sas: %w", err)
	}
	return ParseSAs(msgs, d.now()), nil
}

func secs(m *govici.Message, k string) (time.Duration, bool) {
	s, ok := m.Get(k).(string)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return time.Duration(n) * time.Second, true
}

func u64(m *govici.Message, k string) uint64 {
	s, _ := m.Get(k).(string)
	n, _ := strconv.ParseUint(s, 10, 64)
	return n
}

// ParseSAs interpreta los eventos list-sa (exportado para las pruebas).
func ParseSAs(msgs []*govici.Message, now time.Time) map[uint32]app.SA {
	out := map[uint32]app.SA{}
	for _, m := range msgs {
		for _, name := range m.Keys() {
			idx, ok := domain.ParseConnName(name)
			if !ok {
				continue
			}
			ike, ok := m.Get(name).(*govici.Message)
			if !ok {
				continue
			}
			sa := app.SA{State: "connecting"}
			state, _ := ike.Get("state").(string)
			switch state {
			case "ESTABLISHED":
				sa.State = "established"
			case "DELETING", "DESTROYING", "PASSIVE":
				sa.State = "down"
			}
			if d, ok := secs(ike, "established"); ok {
				sa.EstablishedAt = now.Add(-d)
				sa.LastRekeyAt = sa.EstablishedAt
			}
			if host, _ := ike.Get("remote-host").(string); host != "" {
				sa.Endpoint = host
				if port, _ := ike.Get("remote-port").(string); port != "" {
					sa.Endpoint = host + ":" + port
				}
			}
			if children, ok := ike.Get("child-sas").(*govici.Message); ok {
				for _, ck := range children.Keys() {
					ch, ok := children.Get(ck).(*govici.Message)
					if !ok {
						continue
					}
					if st, _ := ch.Get("state").(string); st == "INSTALLED" {
						sa.ChildInstalled = true
					}
					sa.RxBytes += u64(ch, "bytes-in")
					sa.TxBytes += u64(ch, "bytes-out")
					if d, ok := secs(ch, "install-time"); ok {
						if at := now.Add(-d); at.After(sa.LastRekeyAt) {
							sa.LastRekeyAt = at
						}
					}
				}
			}
			// Con varias IKE SA del mismo túnel (rekey en curso) gana la establecida.
			if prev, dup := out[idx]; dup && prev.State == "established" && sa.State != "established" {
				continue
			}
			out[idx] = sa
		}
	}
	return out
}
