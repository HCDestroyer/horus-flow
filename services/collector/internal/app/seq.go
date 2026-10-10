package app

import "time"

// seqTracker mide los saltos de secuencia de un dominio de observación
// (I1-03 criterio 4). IPFIX numera registros de datos (RFC 7011 §3.1): el
// siguiente datagrama debe traer seq = anterior + registros del anterior.
// NetFlow v9 numera datagramas (RFC 3954 §5.1): se cuentan datagramas
// perdidos y se estiman los registros con la media por datagrama.
type seqTracker struct {
	init     bool
	next     uint32
	avgRecs  float64
	Gaps     uint64
	Lost     uint64
	Received uint64
	// restored: secuencia guardada por un proceso anterior (persist.go). El
	// primer salto tras el arranque es lo enviado con el collector caído.
	restored   bool
	restoredAt time.Time
	Downtime   uint64
	// Resets cuenta los reinicios de secuencia del exportador.
	Resets uint64
}

// reorderDatagrams es cuántos datagramas puede llegar atrasado uno
// reordenado antes de considerar que la secuencia se reinició.
const reorderDatagrams = 256

func (s *seqTracker) reorderWindow(ipfix bool) int64 {
	if !ipfix {
		return reorderDatagrams
	}
	return int64(reorderDatagrams * max(s.avgRecs, 1))
}

// maxJump separa una pérdida de un reinicio del exportador o un reordenado.
const maxJump = 1 << 24

// observe registra un datagrama y devuelve los registros perdidos antes de él
// y, en el primero tras restaurar la secuencia, los que se enviaron mientras
// el collector estaba caído (downtime, que no cuentan como pérdida).
func (s *seqTracker) observe(ipfix bool, seq uint32, records int) (lost, downtime uint64) {
	restored := s.restored
	s.restored = false
	lost = s.observeSeq(ipfix, seq, records)
	if restored && lost > 0 {
		s.Gaps--
		s.Lost -= lost
		s.Downtime += lost
		return 0, lost
	}
	return lost, 0
}

func (s *seqTracker) observeSeq(ipfix bool, seq uint32, records int) (lost uint64) {
	s.Received += uint64(records) //nolint:gosec // records >= 0
	if records > 0 {
		if s.avgRecs == 0 {
			s.avgRecs = float64(records)
		} else {
			s.avgRecs = 0.95*s.avgRecs + 0.05*float64(records)
		}
	}
	if s.init {
		diff := int64(int32(seq - s.next)) //nolint:gosec // diferencia módulo 2^32
		if diff > 0 && diff < maxJump {
			s.Gaps++
			if ipfix {
				lost = uint64(diff)
			} else {
				lost = uint64(float64(diff)*s.avgRecs + 0.5)
			}
			s.Lost += lost
		}
		if diff < 0 && diff > -maxJump && -diff <= s.reorderWindow(ipfix) {
			// Datagrama atrasado (reordenado): no mueve la secuencia esperada.
			return 0
		}
		if diff < 0 {
			// Salto atrás mayor que cualquier reordenado: el exportador reinició
			// su secuencia (reinicio del router o del proceso exportador). Sin
			// esto, todos los datagramas siguientes parecían atrasados hasta
			// alcanzar la secuencia anterior y los huecos no se medían.
			s.Resets++
		}
	}
	s.init = true
	if ipfix {
		s.next = seq + uint32(records) //nolint:gosec // módulo 2^32
	} else {
		s.next = seq + 1
	}
	return lost
}
