// Command flowsim genera flujos IPFIX/NetFlow v9 como los exportaría un
// MikroTik RouterOS 7 a partir de un escenario (tools/flowsim/scenarios) y los
// envía por UDP a un colector o los escribe en un fichero (hfsim, pcap o
// pcapng). En IPFIX usa por defecto las plantillas reales de RouterOS 7
// (258/259) y el NAT tal como lo exporta un router real
// (docs/traffic-model.md §4.4).
// Al terminar escribe el expected.json del escenario.
//
// Ejemplos:
//
//	flowsim -scenario normal -seed 1 -rate 2000                 # UDP a 127.0.0.1:4739, 5 min en tiempo real
//	flowsim -scenario scan -proto v9 -target 10.0.0.5:2055
//	flowsim -scenario c2 -fixture -out c2.hfsim.gz -expected c2.expected.json
//	flowsim -fixtures-dir tools/flowsim/fixtures/sim            # regenera los fixtures de CI
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/capture"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/expect"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/flow"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/selftest"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/sim"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/scenarios"
)

// version se inyecta con -ldflags "-X main.version=...".
var version = "dev"

type config struct {
	scenario    string
	list        bool
	seed        uint64
	proto       string
	rate        float64
	duration    time.Duration
	nat         string
	ipv6        string
	start       string
	fixture     bool
	profile     string
	natFields   bool
	natIPs      string
	target      string
	src         string
	speed       float64
	out         string
	format      string
	expected    string
	flat        bool
	allowUnmet  bool
	fixturesDir string
	maxDatagram int
	showVersion bool
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "flowsim:", err)
		os.Exit(1)
	}
}

func parseFlags(args []string, stderr io.Writer) (*config, error) {
	c := &config{}
	fs := flag.NewFlagSet("flowsim", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&c.scenario, "scenario", "normal", "escenario embebido ("+strings.Join(scenarios.Names(), ", ")+") o ruta a un YAML")
	fs.BoolVar(&c.list, "list", false, "lista los escenarios y sale")
	fs.Uint64Var(&c.seed, "seed", 1, "semilla (misma semilla = misma salida)")
	fs.StringVar(&c.proto, "proto", "ipfix", "protocolo: ipfix o v9")
	fs.Float64Var(&c.rate, "rate", 0, "registros/s del tráfico de fondo (0 = el del escenario)")
	fs.DurationVar(&c.duration, "duration", 0, "duración simulada (0 = la del escenario)")
	fs.StringVar(&c.nat, "nat", "", "true/false: NAT en el router principal (vacío = escenario)")
	fs.StringVar(&c.ipv6, "ipv6", "", "true/false: clientes IPv6 (vacío = escenario)")
	fs.StringVar(&c.start, "start", "", "instante simulado de inicio RFC 3339 (por defecto: ahora en UDP, el del escenario en fichero)")
	fs.BoolVar(&c.fixture, "fixture", false, "usa la duración y tasa reducidas del bloque fixture del escenario")
	fs.StringVar(&c.profile, "profile", "routeros7", "plantillas IPFIX: routeros7 (las reales 258/259, con campos NAT y NAT real) o legacy (las de I0-10, 256/257)")
	fs.BoolVar(&c.natFields, "nat-fields", false, "IPFIX con -profile legacy: añade los campos post-NAT (IE 225-228) y emula el NAT real")
	fs.StringVar(&c.natIPs, "nat-ips", "", "IPs públicas del NAT separadas por comas (sustituye nat_ips del escenario en todos los exportadores)")
	fs.StringVar(&c.target, "target", "", "colector UDP host:puerto (por defecto 127.0.0.1:4739 o :2055 en v9)")
	fs.StringVar(&c.src, "src", "auto", "origen UDP: auto (127.0.0.10+i si el destino es loopback; si no, exporter_ip), scenario, any o lista de IPs separadas por comas")
	fs.Float64Var(&c.speed, "speed", 1, "UDP: velocidad respecto al tiempo real (0 = sin pausas)")
	fs.StringVar(&c.out, "out", "", "escribe a fichero en vez de enviar (.hfsim, .pcap, .pcapng; .gz comprime)")
	fs.StringVar(&c.format, "format", "", "formato del fichero: hfsim, pcap o pcapng (por defecto, por extensión)")
	fs.StringVar(&c.expected, "expected", "", "ruta del expected.json (por defecto <out>.expected.json o expected.json)")
	fs.BoolVar(&c.allowUnmet, "allow-unmet", false, "no falla si las señales no cumplen lo declarado en el escenario")
	fs.BoolVar(&c.flat, "flat", false, "ignora el perfil diario del escenario (daily): tasa fija = rate")
	fs.StringVar(&c.fixturesDir, "fixtures-dir", "", "regenera los fixtures de todos los escenarios en este directorio y sale")
	fs.IntVar(&c.maxDatagram, "max-datagram", 0, "tamaño máximo de datagrama en bytes (0 = escenario, 1392 por defecto)")
	fs.BoolVar(&c.showVersion, "version", false, "muestra la versión")
	if err := fs.Parse(args); err != nil {
		return nil, fmt.Errorf("flags: %w", err)
	}
	return c, nil
}

func optBool(s, name string) (*bool, error) {
	if s == "" {
		return nil, nil
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		return nil, fmt.Errorf("-%s: %w", name, err)
	}
	return &b, nil
}

func run(args []string, stdout, stderr io.Writer) error {
	c, err := parseFlags(args, stderr)
	if err != nil {
		return err
	}
	if c.showVersion {
		_, err := fmt.Fprintln(stdout, "flowsim", version)
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if c.list {
		for _, n := range scenarios.Names() {
			sc, err := sim.Load(n)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(stdout, "%-14s %s\n", n, strings.Join(strings.Fields(sc.Description), " ")); err != nil {
				return err
			}
		}
		return nil
	}
	if c.fixturesDir != "" {
		return selftest.WriteFixtures(ctx, c.fixturesDir, stdout)
	}

	proto, err := flow.ParseProtocol(c.proto)
	if err != nil {
		return err
	}
	profile, err := flow.ParseProfile(c.profile)
	if err != nil {
		return err
	}
	sc, err := sim.Load(c.scenario)
	if err != nil {
		return err
	}
	if c.natIPs != "" {
		var ips []netip.Addr
		for _, s := range strings.Split(c.natIPs, ",") {
			a, err := netip.ParseAddr(strings.TrimSpace(s))
			if err != nil || !a.Is4() || flow.IsClientPrivate(a) {
				return fmt.Errorf("-nat-ips: %q no es una IPv4 pública", s)
			}
			ips = append(ips, a)
		}
		for i := range sc.Exporters {
			sc.Exporters[i].NATIPs = ips
		}
	}
	opt := sim.Options{
		Seed: c.seed, Protocol: proto, Rate: c.rate, Duration: c.duration, Fixture: c.fixture,
		Profile: profile, NATFields: c.natFields, AllowUnmet: c.allowUnmet, MaxDatagram: c.maxDatagram, Flat: c.flat,
	}
	if opt.NAT, err = optBool(c.nat, "nat"); err != nil {
		return err
	}
	if opt.IPv6, err = optBool(c.ipv6, "ipv6"); err != nil {
		return err
	}
	if c.start != "" {
		if opt.Start, err = time.Parse(time.RFC3339, c.start); err != nil {
			return fmt.Errorf("-start: %w", err)
		}
	}

	var exp *expect.Expected
	began := time.Now()
	if c.out != "" {
		exp, err = toFile(ctx, c, sc, opt)
	} else {
		exp, err = toUDP(ctx, c, sc, opt, stderr)
	}
	if err != nil {
		return err
	}
	path := c.expected
	if path == "" {
		path = "expected.json"
		if c.out != "" {
			path = strings.TrimSuffix(strings.TrimSuffix(c.out, ".gz"), ".hfsim")
			path = strings.TrimSuffix(strings.TrimSuffix(path, ".pcapng"), ".pcap") + ".expected.json"
		}
	}
	if err := exp.Save(path); err != nil {
		return err
	}
	var recs, dgrams uint64
	for _, e := range exp.Exporters {
		recs += e.Totals.DataRecords
		dgrams += e.Totals.Datagrams
	}
	_, err = fmt.Fprintf(stderr, "flowsim: %s/%s semilla %d: %d registros en %d datagramas (%d exportadores, %.0f s simulados, %s reales); %d señales, %d hallazgos esperados → %s\n",
		exp.Scenario, exp.Protocol, exp.Seed, recs, dgrams, len(exp.Exporters), exp.DurationSeconds,
		time.Since(began).Round(time.Millisecond), len(exp.Signals), len(exp.Findings), path)
	return err
}

func toFile(ctx context.Context, c *config, sc *sim.Scenario, opt sim.Options) (*expect.Expected, error) {
	f := capture.FormatFromPath(c.out)
	if c.format != "" {
		var err error
		if f, err = capture.ParseFormat(c.format); err != nil {
			return nil, err
		}
	}
	w, closeFn, err := capture.Create(c.out, f)
	if err != nil {
		return nil, err
	}
	exp, err := sim.Run(ctx, sc, opt, func(d capture.Datagram, _ int) error { return w.Write(d) })
	if cerr := closeFn(); err == nil {
		err = cerr
	}
	return exp, err
}

func toUDP(ctx context.Context, c *config, sc *sim.Scenario, opt sim.Options, stderr io.Writer) (*expect.Expected, error) {
	target := c.target
	if target == "" {
		target = fmt.Sprintf("127.0.0.1:%d", opt.Protocol.DefaultPort())
	}
	dst, err := netip.ParseAddrPort(target)
	if err != nil {
		addr, rerr := net.ResolveUDPAddr("udp", target)
		if rerr != nil {
			return nil, fmt.Errorf("-target: %w", rerr)
		}
		dst = addr.AddrPort()
	}
	if opt.Start.IsZero() {
		opt.Start = time.Now().UTC().Truncate(time.Second)
	}
	opt.CollectorPort = dst.Port()

	conns := make([]*net.UDPConn, len(sc.Exporters))
	defer func() {
		for _, cn := range conns {
			if cn != nil {
				_ = cn.Close()
			}
		}
	}()
	explicit := []string{}
	if c.src != "auto" && c.src != "scenario" && c.src != "any" {
		explicit = strings.Split(c.src, ",")
	}
	for i, ex := range sc.Exporters {
		local, err := localAddr(c.src, explicit, i, ex.ExporterIP, dst.Addr())
		if err != nil {
			return nil, err
		}
		// Socket sin conectar: como un router, ignora los ICMP "port unreachable".
		cn, err := net.ListenUDP(udpNet(dst.Addr()), net.UDPAddrFromAddrPort(local))
		if err != nil && c.src == "auto" {
			fmt.Fprintf(stderr, "flowsim: no se puede enviar desde %s (%v); uso una dirección automática\n", local.Addr(), err) //nolint:errcheck
			cn, err = net.ListenUDP(udpNet(dst.Addr()), nil)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: UDP desde %s: %w", ex.Name, local, err)
		}
		_ = cn.SetWriteBuffer(4 << 20)
		conns[i] = cn
	}

	wallStart := time.Now()
	simStart := opt.Start
	exp, err := sim.Run(ctx, sc, opt, func(d capture.Datagram, idx int) error {
		if c.speed > 0 {
			due := wallStart.Add(time.Duration(float64(d.Time.Sub(simStart)) / c.speed))
			if wait := time.Until(due); wait > 0 {
				select {
				case <-time.After(wait):
				case <-ctx.Done():
					return fmt.Errorf("interrumpido: %w", ctx.Err())
				}
			}
		}
		if _, err := conns[idx].WriteToUDPAddrPort(d.Payload, dst); err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return nil
			}
			return fmt.Errorf("UDP: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for i := range exp.Exporters {
		exp.Exporters[i].SentFrom = conns[i].LocalAddr().(*net.UDPAddr).AddrPort().String()
	}
	return exp, nil
}

func udpNet(dst netip.Addr) string {
	if dst.Is4() || dst.Is4In6() {
		return "udp4"
	}
	return "udp6"
}

// localAddr elige la dirección de origen UDP del exportador i.
func localAddr(mode string, explicit []string, i int, exporterIP, dst netip.Addr) (netip.AddrPort, error) {
	switch {
	case len(explicit) > 0:
		if i >= len(explicit) {
			return netip.AddrPort{}, fmt.Errorf("-src: falta la IP del exportador %d", i+1)
		}
		a, err := netip.ParseAddr(strings.TrimSpace(explicit[i]))
		if err != nil {
			return netip.AddrPort{}, fmt.Errorf("-src: %w", err)
		}
		return netip.AddrPortFrom(a, 0), nil
	case mode == "any":
		return netip.AddrPort{}, nil
	case mode == "scenario":
		return netip.AddrPortFrom(exporterIP, 0), nil
	}
	if dst.IsLoopback() {
		if dst.Is4() {
			return netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, byte(10 + i)}), 0), nil
		}
		return netip.AddrPortFrom(netip.IPv6Loopback(), 0), nil
	}
	return netip.AddrPortFrom(exporterIP, 0), nil
}
