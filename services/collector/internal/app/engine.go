package app

import (
	"context"
	"errors"
	"hash/maphash"
	"log/slog"
	"net"
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
		e.workers = append(e.workers, NewWorker(inv, e.Batcher, e.States, m, o.PendingTTL))
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
		_ = c.SetReadBuffer(8 << 20)
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

// Submit entrega un datagrama al trabajador de su exportador sin bloquear.
func (e *Engine) Submit(d Datagram) {
	ip := d.Src.Addr().Unmap().As16()
	i := int(maphash.Bytes(e.seed, ip[:]) % uint64(len(e.queues))) //nolint:gosec // índice acotado
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
	for i, w := range e.workers {
		wg.Add(1)
		go func(q chan Datagram, w *Worker) {
			defer wg.Done()
			for d := range q {
				w.Handle(d)
			}
		}(e.queues[i], w)
	}
	pubCtx, stopPub := context.WithCancel(context.Background())
	pubDone := make(chan struct{})
	go func() {
		defer close(pubDone)
		e.Pub.Run(pubCtx, 5*time.Second)
	}()
	stCtx, stopStates := context.WithCancel(ctx)
	go e.States.Run(stCtx)

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
	stopStates()
	stopPub()
	<-pubDone
	return nil
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
