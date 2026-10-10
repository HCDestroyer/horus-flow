package app

import "sync"

// Decodificación multihilo de un mismo exportador
// (HORUS_COLLECTOR_DECODE_WORKERS > 1).
//
// Con un solo hilo por exportador (el reparto por IP de Engine.Submit) un
// router grande queda limitado a lo que da un núcleo. Aquí cada carril
// (un subconjunto de exportadores) tiene dos goroutines en serie y comparte
// un pool de decodificación:
//
//	recepción → cola del carril → prepare (serie) ─┬─→ ordered (FIFO) → post (serie) → lotes
//	                                              └─→ pool (N hilos): decode + filtros
//
//   - prepare hace en serie todo lo que toca estado: exportador, cabecera,
//     plantillas (una plantilla nueva se aplica antes de los datos que la
//     siguen), retenidos sin plantilla y opciones de muestreo
//     (decode.Decoder.Prepare). Lo que queda son conjuntos de datos de
//     longitud fija con su plantilla inmutable.
//   - el pool decodifica esos conjuntos y filtra (túnel, excluded) en
//     cualquier orden;
//   - post recorre los datagramas en el orden de llegada (la cola ordered
//     es FIFO y espera a que cada uno termine): secuencia y huecos por
//     dominio de observación, estado del exportador y lotes. Por eso los
//     lotes y sus batch_id salen en el mismo orden y con los mismos
//     registros que con un solo hilo.
//
// La equivalencia con un hilo (mismo multiconjunto y mismo orden de
// registros, mismos huecos) la prueban TestDecodeWorkersEquivalence y el test
// dorado con HORUS_COLLECTOR_DECODE_WORKERS=8.

// laneDepth acota los datagramas en curso por carril entre prepare y post.
const laneDepth = 1024

// runParallel arranca carriles y pool; devuelve una función que espera a que
// terminen tras cerrar las colas de los carriles.
func (e *Engine) runParallel() (wait func()) {
	pool := make(chan *dgram, e.opts.DecodeWorkers*64)
	var poolWG, prepWG, postWG sync.WaitGroup
	for range e.opts.DecodeWorkers {
		poolWG.Add(1)
		go func() {
			defer poolWG.Done()
			for j := range pool {
				j.decode(e.m)
				close(j.done)
			}
		}()
	}
	for i, w := range e.workers {
		ordered := make(chan *dgram, laneDepth)
		prepWG.Add(1)
		go func(q chan Datagram, w *Worker) {
			defer prepWG.Done()
			defer close(ordered)
			for d := range q {
				j := w.prepare(d)
				if j == nil {
					continue
				}
				j.done = make(chan struct{})
				ordered <- j
				pool <- j
			}
		}(e.queues[i], w)
		postWG.Add(1)
		go func(w *Worker) {
			defer postWG.Done()
			for j := range ordered {
				<-j.done
				w.post(j)
			}
		}(w)
	}
	return func() {
		prepWG.Wait()
		close(pool)
		poolWG.Wait()
		postWG.Wait()
	}
}
