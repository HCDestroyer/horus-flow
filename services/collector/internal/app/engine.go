package app

import (
	"context"
	"errors"
	"hash/maphash"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
)

// EngineOptions configura el motor del collector.
type EngineOptions struct {
	Workers         int
	QueueDatagrams  int
	BatchMaxRecords int
	BatchMaxAge     time.Duration
	BufferBytes     int64
	PendingTTL      time.Duration
	CollectorID     string
	State           StateOptions
	// UDPReadBuffer es el búfer de recepción de cada socket (0 = DefaultUDPReadBuffer).
	UDPReadBuffer int
	// DecodeWorkers son los hilos que decodifican en paralelo los datagramas
	// de un mismo exportador (parallel.go); 1 (o 0) = un hilo por carril.
	DecodeWorkers int
}

// Engine recibe UDP, reparte por exportador entre trabajadores y publica.
type Engine struct {
	opts    EngineOptions
	m       *Metrics
	log     *slog.Logger
	inv     *flowinv.Store
	Pub     *Publisher
	Batcher *Batcher
	States  *States
	workers []*Worker
	queues  []chan Datagram
	seed    maphash.Seed
	persist *persister

	mu    sync.Mutex
	conns []*net.UDPConn
}

// NewEngine construye el motor.
func NewEngine(o EngineOptions, inv *flowinv.Store, sink Sink, kv KV, m *Metrics, log *slog.Logger) *Engine {
	if o.Workers < 1 {
		o.Workers = 1
	}
	if o.QueueDatagrams < 1 {
		o.QueueDatagrams = 1024
	}
	e := &Engine{opts: o, m: m, log: log, inv: inv, seed: maphash.MakeSeed()}
	e.Pub = NewPublisher(sink, o.BufferBytes, o.CollectorID, m, log)
	e.Batcher = NewBatcher(e.Pub, o.BatchMaxRecords, o.BatchMaxAge, o.CollectorID)
	e.States = NewStates(o.State, inv, kv, sink, log)
	for range o.Workers {
		w := NewWorker(inv, e.Batcher, e.States, m, o.PendingTTL)
		w.downtime = e.Pub.ReportDowntime
		e.workers = append(e.workers, w)
		e.queues = append(e.queues, make(chan Datagram, o.QueueDatagrams))
	}
	return e
}

// Listen abre los sockets UDP (en Start, para fallar antes de estar listo).
func (e *Engine) Listen(addrs []string) error {
	for _, a := range addrs {
		ua, err := net.ResolveUDPAddr("udp", a)
		if err != nil {
			return err
		}
		c, err := net.ListenUDP("udp", ua)
		if err != nil {
			e.Close()
			return err
		}
		want := e.opts.UDPReadBuffer
		if want <= 0 {
			want = DefaultUDPReadBuffer
		}
		if got := setReadBuffer(c, want); got > 0 && got < want {
			e.log.Warn("UDP receive buffer limited by the kernel: raise net.core.rmem_max (datagrams are dropped in bursts otherwise)",
				"addr", a, "requested_bytes", want, "granted_bytes", got)
		} else if got > 0 {
			e.log.Info("UDP receive buffer", "addr", a, "bytes", got)
		}
		e.mu.Lock()
		e.conns = append(e.conns, c)
		e.mu.Unlock()
	}
	return nil
}

// Addrs devuelve las direcciones locales (tests con puerto 0).
func (e *Engine) Addrs() []net.Addr {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]net.Addr, 0, len(e.conns))
	for _, c := range e.conns {
		out = append(out, c.LocalAddr())
	}
	return out
}

// Close cierra los sockets.
func (e *Engine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, c := range e.conns {
		_ = c.Close()
	}
	e.conns = nil
}

func (e *Engine) lane(a netip.Addr) int {
	ip := a.Unmap().As16()
	return int(maphash.Bytes(e.seed, ip[:]) % uint64(len(e.queues))) //nolint:gosec // índice acotado
}

func (e *Engine) workerFor(a netip.Addr) *Worker { return e.workers[e.lane(a)] }

// Submit entrega un datagrama al trabajador de su exportador sin bloquear.
func (e *Engine) Submit(d Datagram) {
	i := e.lane(d.Src.Addr())
	select {
	case e.queues[i] <- d:
	default:
		e.m.Dropped.WithLabelValues(DropQueueFull).Inc()
	}
}

// Run procesa hasta que ctx se cancela; luego vacía lotes y publica lo que
// pueda durante 5 s.
func (e *Engine) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	if e.opts.DecodeWorkers > 1 {
		wait := e.runParallel()
		wg.Add(1)
		go func() { defer wg.Done(); wait() }()
	} else {
		for i, w := range e.workers {
			wg.Add(1)
			go func(q chan Datagram, w *Worker) {
				defer wg.Done()
				for d := range q {
					w.Handle(d)
				}
			}(e.queues[i], w)
		}
	}
	pubCtx, stopPub := context.WithCancel(context.Background())
	pubDone := make(chan struct{})
	go func() {
		defer close(pubDone)
		e.Pub.Run(pubCtx, 5*time.Second)
	}()
	stCtx, stopStates := context.WithCancel(ctx)
	go e.States.Run(stCtx)
	persistDone := make(chan struct{})
	persistCtx, stopPersist := context.WithCancel(context.Background())
	if e.persist != nil {
		go func() { defer close(persistDone); e.persist.run(persistCtx) }()
	} else {
		close(persistDone)
	}

	var rg sync.WaitGroup
	e.mu.Lock()
	conns := append([]*net.UDPConn(nil), e.conns...)
	e.mu.Unlock()
	for _, c := range conns {
		rg.Add(1)
		go func(c *net.UDPConn) {
			defer rg.Done()
			e.read(c)
		}(c)
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case now := <-tick.C:
			e.Batcher.Flush(now, false)
		}
	}
	e.Close()
	rg.Wait()
	for _, q := range e.queues {
		close(q)
	}
	wg.Wait()
	e.Batcher.Flush(time.Now(), true)
	e.persistAll()
	stopPersist()
	<-persistDone
	stopStates()
	stopPub()
	<-pubDone
	return nil
}

// persistAll guarda la última secuencia de cada dominio (al parar, con los
// trabajadores ya terminados).
func (e *Engine) persistAll() {
	if e.persist == nil {
		return
	}
	for _, w := range e.workers {
		for k, tr := range w.seq {
			if w.persist != nil {
				w.persist.seqAt[k] = time.Time{}
				w.persist.seq(k, tr, time.Now())
			}
		}
	}
}

func (e *Engine) read(c *net.UDPConn) {
	buf := make([]byte, 65535)
	for {
		n, src, err := c.ReadFromUDPAddrPort(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		p := make([]byte, n)
		copy(p, buf[:n])
		e.Submit(Datagram{Src: src, Payload: p, At: time.Now()})
	}
}
