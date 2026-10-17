// Package snmpclient habla SNMPv3 con los routers por el túnel: un
// [Transport] abstracto (GET y walk) con implementación gosnmp y un lector de
// MIB-II / IF-MIB / HOST-RESOURCES-MIB / MIKROTIK-MIB que devuelve null para
// todo OID ausente (docs/vendors/mikrotik.md §3.2). Nunca registra
// contraseñas ni usuarios.
package snmpclient

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
)

// Var es una variable SNMP normalizada: Value es int64 (INTEGER), uint64
// (Counter32/64, Gauge32, TimeTicks), string (OCTET STRING) o nil (ausente).
type Var struct {
	OID   string
	Value any
}

// Transport es una sesión SNMP con un router.
type Transport interface {
	Get(ctx context.Context, oids []string) ([]Var, error)
	Walk(ctx context.Context, root string) ([]Var, error)
	Close() error
}

// Credential es la credencial SNMPv3 (authPriv; sin priv si PrivProtocol
// está vacío).
type Credential struct {
	Username     string
	AuthProtocol string // sha1 | sha256 | md5
	AuthPassword string
	PrivProtocol string // aes128 | aes256 | des | ""
	PrivPassword string
}

// Dialer abre sesiones (se sustituye por un doble en tests).
type Dialer interface {
	Dial(ctx context.Context, target netip.Addr, cred Credential) (Transport, error)
}

// ErrAuth: el router rechazó la credencial (usuario desconocido, digest o
// descifrado incorrectos) → señal snmp = auth_failed.
var ErrAuth = errors.New("snmp: authentication failed")

// IsAuth indica si err es un fallo de credenciales.
func IsAuth(err error) bool { return errors.Is(err, ErrAuth) }

// GoSNMP es el Dialer real (UDP/161, SNMPv3 USM).
type GoSNMP struct {
	Timeout time.Duration
	Retries int
	Port    uint16
}

// Dial implementa Dialer.
func (d GoSNMP) Dial(ctx context.Context, target netip.Addr, cred Credential) (Transport, error) {
	auth, err := authProtocol(cred.AuthProtocol)
	if err != nil {
		return nil, err
	}
	priv, err := privProtocol(cred.PrivProtocol)
	if err != nil {
		return nil, err
	}
	flags := gosnmp.AuthPriv
	if priv == gosnmp.NoPriv {
		flags = gosnmp.AuthNoPriv
	}
	port := d.Port
	if port == 0 {
		port = 161
	}
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	g := &gosnmp.GoSNMP{
		Target: target.Unmap().String(), Port: port, Transport: "udp", Version: gosnmp.Version3,
		Timeout: timeout, Retries: d.Retries, MaxOids: 60, MaxRepetitions: 25, ExponentialTimeout: false,
		SecurityModel: gosnmp.UserSecurityModel, MsgFlags: flags, Context: ctx,
		SecurityParameters: &gosnmp.UsmSecurityParameters{
			UserName: cred.Username, AuthenticationProtocol: auth, AuthenticationPassphrase: cred.AuthPassword,
			PrivacyProtocol: priv, PrivacyPassphrase: cred.PrivPassword,
		},
	}
	if err := g.Connect(); err != nil {
		return nil, fmt.Errorf("snmp: connect: %w", err)
	}
	return &session{g: g}, nil
}

type session struct{ g *gosnmp.GoSNMP }

func (s *session) Get(ctx context.Context, oids []string) ([]Var, error) {
	s.g.Context = ctx
	res, err := s.g.Get(oids)
	if err != nil {
		return nil, classify(err)
	}
	out := make([]Var, 0, len(res.Variables))
	for _, p := range res.Variables {
		out = append(out, convert(p))
	}
	return out, nil
}

func (s *session) Walk(ctx context.Context, root string) ([]Var, error) {
	s.g.Context = ctx
	res, err := s.g.BulkWalkAll(root)
	if err != nil {
		return nil, classify(err)
	}
	out := make([]Var, 0, len(res))
	for _, p := range res {
		out = append(out, convert(p))
	}
	return out, nil
}

func (s *session) Close() error {
	if s.g.Conn != nil {
		return s.g.Conn.Close()
	}
	return nil
}

// classify marca como ErrAuth los fallos de credencial. Validado contra RouterOS 7.12 y 7.23.8
// (CHR del laboratorio): con la contraseña de autenticación incorrecta RouterOS responde un
// Report que gosnmp rechaza con un error sin tipo ("incoming packet is not authentic,
// discarding"), no con ErrWrongDigest; también es un fallo de credencial.
func classify(err error) error {
	switch {
	case errors.Is(err, gosnmp.ErrUnknownUsername), errors.Is(err, gosnmp.ErrWrongDigest), errors.Is(err, gosnmp.ErrDecryption),
		strings.Contains(err.Error(), "packet is not authentic"):
		return fmt.Errorf("%w: %v", ErrAuth, err)
	}
	return err
}

func convert(p gosnmp.SnmpPDU) Var {
	v := Var{OID: p.Name}
	switch p.Type {
	case gosnmp.NoSuchObject, gosnmp.NoSuchInstance, gosnmp.EndOfMibView, gosnmp.Null:
		return v
	case gosnmp.OctetString:
		if b, ok := p.Value.([]byte); ok {
			v.Value = string(b)
		} else if s, ok := p.Value.(string); ok {
			v.Value = s
		}
	case gosnmp.Integer:
		v.Value = gosnmp.ToBigInt(p.Value).Int64()
	case gosnmp.Counter32, gosnmp.Counter64, gosnmp.Gauge32, gosnmp.TimeTicks, gosnmp.Uinteger32:
		v.Value = gosnmp.ToBigInt(p.Value).Uint64()
	default:
		if p.Value != nil {
			v.Value = fmt.Sprint(p.Value)
		}
	}
	return v
}

func authProtocol(p string) (gosnmp.SnmpV3AuthProtocol, error) {
	switch strings.ToLower(p) {
	case "sha1", "sha", "":
		return gosnmp.SHA, nil
	case "sha256":
		return gosnmp.SHA256, nil
	case "sha512":
		return gosnmp.SHA512, nil
	case "md5":
		return gosnmp.MD5, nil
	}
	return 0, fmt.Errorf("snmp: unsupported auth protocol %q", p)
}

func privProtocol(p string) (gosnmp.SnmpV3PrivProtocol, error) {
	switch strings.ToLower(p) {
	case "":
		return gosnmp.NoPriv, nil
	case "aes128", "aes":
		return gosnmp.AES, nil
	case "aes256":
		return gosnmp.AES256, nil
	case "des":
		return gosnmp.DES, nil
	}
	return 0, fmt.Errorf("snmp: unsupported priv protocol %q", p)
}
