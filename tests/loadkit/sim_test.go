package loadkit

import (
	"testing"
	"time"
)

func TestLatenciesByPath(t *testing.T) {
	var l Latencies
	for i := range 100 {
		l.AddPath("/a", time.Duration(i+1)*time.Millisecond, 200)
		l.AddPath("/b", 5*time.Millisecond, 200)
	}
	l.AddPath("/b", time.Second, 503)
	st := l.Stats()
	if st.N != 200 || st.Errors != 1 || st.ByStatus["503"] != 1 {
		t.Fatalf("N=%d errores=%d %v", st.N, st.Errors, st.ByStatus)
	}
	if st.ByPath["/a"] != 96*time.Millisecond || st.ByPath["/b"] != 5*time.Millisecond {
		t.Fatalf("p95 por consulta = %v", st.ByPath)
	}
}
