// Command ipfix-anonymize anonimiza una captura pcap/pcapng de NetFlow
// v9/IPFIX para versionarla como fixture (I0-12) y comprueba que ninguna IP o
// MAC original sobrevive. Detalle del remapeo en internal/anonymize.
//
//	ipfix-anonymize -in cap.pcapng -out fixture.pcapng -key-file clave.bin -duration 20s
//	ipfix-anonymize -check -in cap.pcapng -anon fixture.pcapng
//
// Nunca imprime direcciones originales. La clave HMAC debe guardarse fuera del
// repositorio (o no guardarse: sin -key-file se genera una aleatoria y el
// mapeo no se puede reproducir ni invertir).
package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/anonymize"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/capture"
)

var errLeak = errors.New("la captura anonimizada contiene direcciones originales")

type config struct {
	in, out, anon string
	keyFile       string
	clientNet     string
	exporter      string
	natIPs        string
	from          time.Duration
	duration      time.Duration
	align         bool
	ifName        string
	check         bool
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "ipfix-anonymize:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	c := &config{}
	fs := flag.NewFlagSet("ipfix-anonymize", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&c.in, "in", "", "captura original (pcap o pcapng, opcionalmente .gz)")
	fs.StringVar(&c.out, "out", "", "pcapng anonimizado de salida")
	fs.StringVar(&c.anon, "anon", "", "con -check: captura anonimizada a comprobar")
	fs.StringVar(&c.keyFile, "key-file", "", "clave HMAC (fichero, ≥ 16 bytes; hex o binario). Sin ella se usa una aleatoria")
	fs.StringVar(&c.clientNet, "client-net", "10.20.0.0/16", "/16 destino de las IPs privadas (se conserva la estructura de /24)")
	fs.StringVar(&c.exporter, "exporter", "10.255.3.17", "IP que sustituye al exportador")
	fs.StringVar(&c.natIPs, "nat-ips", "192.0.2.10,192.0.2.11,192.0.2.12", "IPs que sustituyen a las IPs públicas del NAT (por frecuencia)")
	fs.DurationVar(&c.from, "from", 0, "descarta lo anterior a inicio + from")
	fs.DurationVar(&c.duration, "duration", 0, "duración del recorte (0 = todo)")
	fs.BoolVar(&c.align, "align-template", true, "empieza en el primer datagrama con plantillas")
	fs.StringVar(&c.ifName, "ifname", "ipfix-export", "nombre de interfaz del pcapng de salida")
	fs.BoolVar(&c.check, "check", false, "compara -in (original) con -anon y falla si queda alguna IP o MAC original")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("flags: %w", err)
	}
	if c.in == "" {
		fs.Usage()
		return errors.New("falta -in")
	}
	orig, err := readFrames(c.in)
	if err != nil {
		return err
	}
	if c.check {
		if c.anon == "" {
			return errors.New("-check requiere -anon")
		}
		anon, err := readFrames(c.anon)
		if err != nil {
			return err
		}
		return check(orig, anon, stdout)
	}
	if c.out == "" {
		return errors.New("falta -out")
	}
	cfg, err := c.anonConfig(stderr)
	if err != nil {
		return err
	}
	out, st, err := anonymize.Anonymize(orig, cfg)
	if err != nil {
		return err
	}
	if len(out) == 0 {
		return errors.New("el recorte no contiene datagramas")
	}
	if err := writeFrames(c.out, out, c.ifName); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout,
		"ipfix-anonymize: %d/%d tramas escritas (%d descartadas por no ser flujo, %d sin plantilla), %d registros, %s → %s (%.1f s)\n"+
			"  remapeadas: %d exportador(es) → %s, %d IP(s) de NAT → %s, %d IPs privadas en %d /24 → %s, %d IPs públicas → %d direcciones de documentación, %d MAC\n",
		st.FramesOut, st.FramesIn, st.DroppedNonFlow, st.DroppedNoTmpl, st.RecordsOut,
		st.Start.Format(time.RFC3339), st.End.Format(time.RFC3339), st.End.Sub(st.Start).Seconds(),
		len(st.Exporters), cfg.Exporter, st.NATIPs, c.natIPs, st.PrivateIPs, st.Private24, cfg.ClientNet,
		st.PublicIPs, st.PoolSize, st.MACs)
	if err != nil {
		return fmt.Errorf("salida: %w", err)
	}
	anon, err := readFrames(c.out)
	if err != nil {
		return err
	}
	return check(orig, anon, stdout)
}

func (c *config) anonConfig(stderr io.Writer) (anonymize.Config, error) {
	cfg := anonymize.Config{From: c.from, Duration: c.duration, AlignTemplate: c.align, IfName: c.ifName}
	var err error
	if cfg.ClientNet, err = netip.ParsePrefix(c.clientNet); err != nil {
		return cfg, fmt.Errorf("-client-net: %w", err)
	}
	if cfg.Exporter, err = netip.ParseAddr(c.exporter); err != nil {
		return cfg, fmt.Errorf("-exporter: %w", err)
	}
	for _, s := range strings.Split(c.natIPs, ",") {
		a, err := netip.ParseAddr(strings.TrimSpace(s))
		if err != nil {
			return cfg, fmt.Errorf("-nat-ips: %w", err)
		}
		cfg.NATIPs = append(cfg.NATIPs, a)
	}
	if c.keyFile == "" {
		cfg.Key = make([]byte, 32)
		if _, err := rand.Read(cfg.Key); err != nil {
			return cfg, fmt.Errorf("clave: %w", err)
		}
		fmt.Fprintln(stderr, "ipfix-anonymize: sin -key-file: clave aleatoria (el mapeo no será reproducible)") //nolint:errcheck
		return cfg, nil
	}
	raw, err := os.ReadFile(c.keyFile) //nolint:gosec // ruta indicada por quien ejecuta la herramienta
	if err != nil {
		return cfg, fmt.Errorf("-key-file: %w", err)
	}
	if h, err := hex.DecodeString(strings.TrimSpace(string(raw))); err == nil {
		raw = h
	}
	cfg.Key = raw
	return cfg, nil
}

func readFrames(path string) ([]capture.Frame, error) {
	fh, err := os.Open(path) //nolint:gosec // ruta indicada por quien ejecuta la herramienta
	if err != nil {
		return nil, fmt.Errorf("captura: %w", err)
	}
	defer func() { _ = fh.Close() }()
	var out []capture.Frame
	err = capture.ReadFrames(fh, func(f capture.Frame) error {
		f.Data = append([]byte(nil), f.Data...)
		out = append(out, f)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return out, nil
}

func writeFrames(path string, frames []capture.Frame, ifName string) error {
	fh, err := os.Create(path) //nolint:gosec // ruta indicada por quien ejecuta la herramienta
	if err != nil {
		return fmt.Errorf("salida: %w", err)
	}
	w, err := capture.NewPcapngWriter(fh, frames[0].LinkType, ifName)
	if err == nil {
		for _, f := range frames {
			if err = w.WriteFrame(f.Time, f.Data); err != nil {
				break
			}
		}
	}
	if err == nil {
		err = w.Flush()
	}
	if cerr := fh.Close(); err == nil && cerr != nil {
		err = fmt.Errorf("salida: %w", cerr)
	}
	return err
}

func check(orig, anon []capture.Frame, w io.Writer) error {
	r, err := anonymize.Check(orig, anon)
	if err != nil {
		return err
	}
	kept := make([]string, len(r.Kept))
	for i, a := range r.Kept {
		kept[i] = a.String()
	}
	res := "OK"
	if !r.OK() {
		res = "FALLO"
	}
	_, err = fmt.Fprintf(w,
		"comprobación %s: original %d IPs y %d MAC identificativas; anonimizada %d IPs y %d MAC\n"+
			"  IPs originales presentes como dirección: %d; MAC originales presentes: %d\n"+
			"  búsqueda de bytes en las tramas: %d MAC (6 B), %d IPv6 (16 B), %d IPv4 (4 B, incluye coincidencias casuales en contadores)\n"+
			"  conservadas por no identificar a nadie: %s\n",
		res, r.OriginalIPs, r.OriginalMACs, r.AnonymizedIPs, r.AnonymizedMACs,
		len(r.LeakedIPs), len(r.LeakedMACs), r.RawMACHits, r.RawIPv6Hits, r.RawIPv4Hits, strings.Join(kept, ", "))
	if err != nil {
		return fmt.Errorf("salida: %w", err)
	}
	if !r.OK() {
		return errLeak
	}
	return nil
}
