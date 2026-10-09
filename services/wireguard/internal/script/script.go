// Package script genera los scripts RouterOS (.rsc) de alta y de baja
// (I1-02; docs/vendors/mikrotik.md §7, opción B de §5.2): plantillas
// versionadas por rama de RouterOS (7.12 mínima y long-term), con todos los
// valores validados antes de insertarlos (ningún valor puede cerrar una
// cadena ni inyectar comandos).
package script

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"text/template"
)

//go:embed templates/*.tmpl
var files embed.FS

var tmpl = template.Must(template.ParseFS(files, "templates/*.tmpl"))

// Plantillas.
const (
	Template712      = "7.12"
	TemplateLongTerm = "7-longterm"
	// LongTermMinor: desde 7.18 se usa la plantilla long-term (a verificar
	// en CHR, I1-25).
	LongTermMinor = 18
	MinMinor      = 12
)

// ErrUnsupported indica RouterOS < 7.12 o no v7 (D15).
var ErrUnsupported = errors.New("script: RouterOS v7 >= 7.12 required")

var versionRe = regexp.MustCompile(`^7\.([0-9]+)(\.[0-9]+)?$`)

// TemplateFor elige la plantilla de una versión ("" = mínima soportada).
func TemplateFor(version string) (string, error) {
	if version == "" {
		return Template712, nil
	}
	m := versionRe.FindStringSubmatch(version)
	if m == nil {
		return "", ErrUnsupported
	}
	minor, _ := strconv.Atoi(m[1])
	switch {
	case minor < MinMinor:
		return "", ErrUnsupported
	case minor >= LongTermMinor:
		return TemplateLongTerm, nil
	}
	return Template712, nil
}

// Values son los valores del script de alta.
type Values struct {
	Template     string
	RouterID     string
	RouterName   string
	RouterWGIP   netip.Addr
	HubPublicKey string
	WGEndpoint   string
	WGPort       int
	ServicesCIDR netip.Prefix
	CollectorIP  netip.Addr
	APIUser      string
	APIPassword  string
	SNMPUser     string
	SNMPAuthPass string
	SNMPPrivPass string
	NTPServer    string
	EnrollURL    string
	Token        string
	CacheEntries string
	AccessMode   string
	CACertPEM    string // vacío con ACME (certificado de confianza pública)
}

var (
	hostRe   = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,252})$`)
	secretRe = regexp.MustCompile(`^[A-Za-z0-9]{8,64}$`)
	userRe   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)
	tokenRe  = regexp.MustCompile(`^[A-Za-z0-9_-]{43,64}$`)
	keyRe    = regexp.MustCompile(`^[A-Za-z0-9+/]{42}[AEIMQUYcgkosw480]=$`)
	urlRe    = regexp.MustCompile(`^https://[A-Za-z0-9.:\[\]-]+(/[A-Za-z0-9/._-]*)?$`)
	cacheRe  = regexp.MustCompile(`^[0-9]+k$`)
	nameBad  = regexp.MustCompile(`[^A-Za-z0-9 ._-]`)
)

// host valida un nombre DNS o una IP literal.
func host(s string) bool {
	if _, err := netip.ParseAddr(s); err == nil {
		return true
	}
	return hostRe.MatchString(s) && !strings.Contains(s, "..")
}

// rosString escapa s para una cadena RouterOS entre comillas.
func rosString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "?", `\?`, "\r", "", "\n", `\n`, "\t", `\t`)
	return r.Replace(s)
}

func (v *Values) validate() error {
	var errs []string
	check := func(ok bool, what string) {
		if !ok {
			errs = append(errs, what)
		}
	}
	check(v.Template == Template712 || v.Template == TemplateLongTerm, "template")
	check(v.RouterWGIP.Is4(), "router tunnel ip")
	check(keyRe.MatchString(v.HubPublicKey), "hub public key")
	check(host(v.WGEndpoint), "wireguard endpoint")
	check(v.WGPort > 0 && v.WGPort < 65536, "wireguard port")
	check(v.ServicesCIDR.IsValid() && v.ServicesCIDR.Addr().Is4(), "services cidr")
	check(v.CollectorIP.Is4() && v.ServicesCIDR.Contains(v.CollectorIP), "collector ip")
	check(userRe.MatchString(v.APIUser) && userRe.MatchString(v.SNMPUser), "users")
	check(secretRe.MatchString(v.APIPassword) && secretRe.MatchString(v.SNMPAuthPass) && secretRe.MatchString(v.SNMPPrivPass), "passwords")
	check(host(v.NTPServer), "ntp server")
	check(urlRe.MatchString(v.EnrollURL), "enroll url")
	check(tokenRe.MatchString(v.Token), "token")
	check(cacheRe.MatchString(v.CacheEntries), "cache entries")
	if v.CACertPEM != "" {
		check(strings.HasPrefix(strings.TrimSpace(v.CACertPEM), "-----BEGIN CERTIFICATE-----"), "ca certificate")
	}
	if len(errs) > 0 {
		return fmt.Errorf("script: invalid values: %s", strings.Join(errs, ", "))
	}
	return nil
}

// cleanName deja en el nombre del router solo caracteres seguros para un
// comentario del script.
func cleanName(s string) string {
	s = nameBad.ReplaceAllString(s, "_")
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

type view struct {
	Values
	RouterName string
	CACertPEM  string
}

// Onboarding genera el script de alta.
func Onboarding(v Values) (string, error) {
	if err := v.validate(); err != nil {
		return "", err
	}
	vw := view{Values: v, RouterName: cleanName(v.RouterName)}
	if v.CACertPEM != "" {
		vw.CACertPEM = rosString(strings.TrimSpace(v.CACertPEM) + "\n")
	}
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, "onboarding-"+v.Template+".rsc.tmpl", vw); err != nil {
		return "", fmt.Errorf("script: render: %w", err)
	}
	return b.String(), nil
}

// DeprovisionValues son los valores del script inverso.
type DeprovisionValues struct {
	RouterID    string
	RouterName  string
	RouterWGIP  netip.Addr
	CollectorIP netip.Addr
	SNMPUser    string
}

// Deprovisioning genera el script inverso.
func Deprovisioning(v DeprovisionValues) (string, error) {
	if !v.CollectorIP.Is4() || !userRe.MatchString(v.SNMPUser) {
		return "", errors.New("script: invalid deprovisioning values")
	}
	if !v.RouterWGIP.IsValid() {
		// Sin IP de túnel aún: el filtro por src-address no casa con nada.
		v.RouterWGIP = netip.IPv4Unspecified()
	}
	v.RouterName = cleanName(v.RouterName)
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, "deprovisioning.rsc.tmpl", v); err != nil {
		return "", fmt.Errorf("script: render: %w", err)
	}
	return b.String(), nil
}
