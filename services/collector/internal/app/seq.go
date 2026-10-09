package app

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
}

// maxJump separa una pérdida de un reinicio del exportador o un reordenado.
const maxJump = 1 << 24

// observe registra un datagrama y devuelve los registros perdidos antes de él.
func (s *seqTracker) observe(ipfix bool, seq uint32, records int) (lost uint64) {
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
		if diff < 0 && diff > -maxJump {
			// Datagrama atrasado (reordenado): no mueve la secuencia esperada.
			return 0
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
