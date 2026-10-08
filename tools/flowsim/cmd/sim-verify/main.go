// Command sim-verify decodifica flujos NetFlow v9/IPFIX (con goflow2, una
// librería independiente del codificador del simulador) y los compara con el
// expected.json del escenario: secuencias, refresco de plantillas, totales,
// atribución de clientes, NAT, IPv6 y señales/hallazgos esperados.
//
// Modos:
//
//	sim-verify -selftest [-fixtures DIR]                  # prueba sin router para CI
//	sim-verify -in captura.hfsim.gz -expected e.json      # verifica un fichero (hfsim o pcap)
//	sim-verify -listen 127.0.0.1:4739 -expected e.json    # escucha UDP hasta que el emisor calla
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/capture"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/expect"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/selftest"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/verify"
)

var errFailed = errors.New("la verificación ha fallado")

type config struct {
	selftest  bool
	fixtures  string
	in        string
	listen    string
	expected  string
	idle      time.Duration
	timeout   time.Duration
	tolerance float64
	jsonOut   bool
	verbose   bool
}

func main() {
	err := run(os.Args[1:], os.Stdout, os.Stderr)
	if err != nil {
		if !errors.Is(err, errFailed) {
			fmt.Fprintln(os.Stderr, "sim-verify:", err)
		}
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	c := &config{}
	fs := flag.NewFlagSet("sim-verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&c.selftest, "selftest", false, "genera y verifica todos los escenarios en memoria (sin red)")
	fs.StringVar(&c.fixtures, "fixtures", "", "con -selftest: verifica también los fixtures de este directorio")
	fs.StringVar(&c.in, "in", "", "fichero a verificar (hfsim o pcap, opcionalmente .gz)")
	fs.StringVar(&c.listen, "listen", "", "escucha UDP en host:puerto")
	fs.StringVar(&c.expected, "expected", "", "expected.json del escenario")
	fs.DurationVar(&c.idle, "idle", 20*time.Second, "con -listen: termina tras este tiempo sin datagramas (> inactive timeout de 15 s)")
	fs.DurationVar(&c.timeout, "timeout", 0, "con -listen: tiempo máximo total (0 = sin límite)")
	fs.Float64Var(&c.tolerance, "tolerance", 0, "fracción de datagramas perdidos admitida (UDP)")
	fs.BoolVar(&c.jsonOut, "json", false, "imprime el informe en JSON")
	fs.BoolVar(&c.verbose, "v", false, "con -selftest: imprime cada comprobación")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("flags: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch {
	case c.selftest:
		return runSelftest(ctx, c, stdout)
	case c.in != "":
		if c.expected == "" {
			return errors.New("-in requiere -expected")
		}
		exp, err := expect.Load(c.expected)
		if err != nil {
			return err
		}
		v, err := verify.New(exp, verify.Options{Tolerance: c.tolerance})
		if err != nil {
			return err
		}
		if err := capture.ReadFile(c.in, func(d capture.Datagram) error { v.Feed(d); return nil }); err != nil {
			return err
		}
		return report(v.Report(), c.jsonOut, stdout)
	case c.listen != "":
		if c.expected == "" {
			return errors.New("-listen requiere -expected")
		}
		return runListen(ctx, c, stdout, stderr)
	}
	fs.Usage()
	return errors.New("indica -selftest, -in o -listen")
}

func report(rep *verify.Report, jsonOut bool, w io.Writer) error {
	if jsonOut {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return fmt.Errorf("informe: %w", err)
		}
	} else if err := rep.Write(w); err != nil {
		return err
	}
	if !rep.OK {
		return errFailed
	}
	return nil
}

func runSelftest(ctx context.Context, c *config, w io.Writer) error {
	start := time.Now()
	ok, err := selftest.Run(ctx, w, selftest.DefaultCases(), c.verbose)
	if err != nil {
		return err
	}
	if c.fixtures != "" {
		fok, err := selftest.CheckFixtures(ctx, c.fixtures, w, c.verbose)
		if err != nil {
			return err
		}
		ok = ok && fok
	}
	res := "OK"
	if !ok {
		res = "FALLO"
	}
	if _, err := fmt.Fprintf(w, "sim-verify -selftest: %s en %s\n", res, time.Since(start).Round(time.Millisecond)); err != nil {
		return fmt.Errorf("selftest: %w", err)
	}
	if !ok {
		return errFailed
	}
	return nil
}

func runListen(ctx context.Context, c *config, stdout, stderr io.Writer) error {
	addr, err := net.ResolveUDPAddr("udp", c.listen)
	if err != nil {
		return fmt.Errorf("-listen: %w", err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("-listen: %w", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadBuffer(16 << 20)
	fmt.Fprintf(stderr, "sim-verify: escuchando en %s (fin tras %s sin datagramas)\n", conn.LocalAddr(), c.idle) //nolint:errcheck

	var got []capture.Datagram
	began := time.Now()
	buf := make([]byte, 65535)
	for ctx.Err() == nil {
		deadline := time.Now().Add(c.idle)
		if len(got) == 0 {
			deadline = time.Now().Add(time.Second) // espera al primer datagrama
		}
		if c.timeout > 0 && time.Since(began) > c.timeout {
			break
		}
		_ = conn.SetReadDeadline(deadline)
		n, src, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				if len(got) == 0 {
					continue
				}
				break
			}
			return fmt.Errorf("UDP: %w", err)
		}
		got = append(got, capture.Datagram{Time: time.Now().UTC(), Src: src, Payload: append([]byte(nil), buf[:n]...)})
	}
	fmt.Fprintf(stderr, "sim-verify: %d datagramas recibidos\n", len(got)) //nolint:errcheck

	// El emisor escribe expected.json al terminar: se espera a que exista.
	var exp *expect.Expected
	for i := 0; ; i++ {
		exp, err = expect.Load(c.expected)
		if err == nil || i >= 50 || !strings.Contains(err.Error(), "no such file") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		return err
	}
	v, err := verify.New(exp, verify.Options{Tolerance: c.tolerance})
	if err != nil {
		return err
	}
	for _, d := range got {
		v.Feed(d)
	}
	return report(v.Report(), c.jsonOut, stdout)
}
