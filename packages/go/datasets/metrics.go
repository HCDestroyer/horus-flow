package datasets

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Age devuelve la antigüedad del dataset vigente: tiempo desde el último
// intento que dejó (o confirmó) una versión válida. ok es false si la fuente
// nunca tuvo una versión válida.
func (st State) Age(now time.Time) (time.Duration, bool) {
	if st.Current == nil || st.LastSuccess.IsZero() {
		return 0, false
	}
	return now.Sub(st.LastSuccess), true
}

// WritePrometheus escribe las métricas de las fuentes en formato de
// exposición de Prometheus (para un collector de textfile o para el registro
// de observabilidad que llega con I0-04/I0-18):
//
//	horus_dataset_age_seconds{kind,source}             antigüedad del dataset vigente
//	horus_dataset_entries{kind,source}                 entradas del dataset vigente
//	horus_dataset_consecutive_failures{kind,source}    intentos fallidos seguidos
//	horus_dataset_last_success_timestamp_seconds{kind,source}
//
// Una fuente sin versión válida no emite antigüedad (ausencia = alerta).
func WritePrometheus(w io.Writer, kind string, states []State, now time.Time) error {
	var b strings.Builder
	type metric struct{ name, help string }
	ms := []metric{
		{"horus_dataset_age_seconds", "Antigüedad del dataset externo vigente."},
		{"horus_dataset_entries", "Entradas válidas del dataset externo vigente."},
		{"horus_dataset_consecutive_failures", "Intentos de descarga fallidos consecutivos."},
		{"horus_dataset_last_success_timestamp_seconds", "Último intento que dejó un dataset válido vigente."},
	}
	for _, m := range ms {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s gauge\n", m.name, m.help, m.name)
		for _, st := range states {
			labels := fmt.Sprintf(`{kind=%s,source=%s}`, strconv.Quote(kind), strconv.Quote(st.SourceID))
			switch m.name {
			case "horus_dataset_age_seconds":
				if age, ok := st.Age(now); ok {
					fmt.Fprintf(&b, "%s%s %.0f\n", m.name, labels, age.Seconds())
				}
			case "horus_dataset_entries":
				if st.Current != nil {
					fmt.Fprintf(&b, "%s%s %d\n", m.name, labels, st.Current.Entries)
				}
			case "horus_dataset_consecutive_failures":
				fmt.Fprintf(&b, "%s%s %d\n", m.name, labels, st.ConsecutiveFailures)
			case "horus_dataset_last_success_timestamp_seconds":
				if !st.LastSuccess.IsZero() {
					fmt.Fprintf(&b, "%s%s %d\n", m.name, labels, st.LastSuccess.Unix())
				}
			}
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}
