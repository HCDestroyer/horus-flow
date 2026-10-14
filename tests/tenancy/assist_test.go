//go:build integration

package tenancy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/assist"
)

// Un servidor compatible con OpenAI que responde siempre con un análisis
// completo (sin cifras ni comandos: pasa la verificación de anclaje).
func fakeLLM(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	answer := "## Qué está pasando\nEl cliente muestra señales compatibles con escaneo saliente [F1].\n" +
		"## Posibles causas\n- Equipo comprometido (probable).\n- Herramienta de diagnóstico del propio cliente (menos probable).\n" +
		"## Qué hacer\n- Revisar el caso con el cliente.\n## Para deshacer\nNo hay cambios que deshacer.\n## Fuentes\n- get_finding [F1]"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		var req struct {
			Messages []struct {
				Role    string  `json:"role"`
				Content *string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		// El primer mensaje de usuario debe traer los datos precargados del hallazgo.
		if len(req.Messages) < 2 || req.Messages[1].Content == nil || !strings.Contains(*req.Messages[1].Content, "get_finding") {
			t.Errorf("el prompt no trae los datos primarios: %+v", req.Messages)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "fake-1",
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": answer}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 1200, "completion_tokens": 180},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// POST /assist/analyses de principio a fin dentro del proceso: el asistente
// lee el hallazgo por la API como el usuario (loopback), precarga los datos,
// llama al proveedor, enmascara, verifica el anclaje, audita y descuenta
// del límite diario. Otro ISP no puede analizar ese hallazgo (404).
func TestAssistAnalyzeEndToEnd(t *testing.T) {
	t.Parallel()
	llm, calls := fakeLLM(t)
	a := startAppWith(t, []roleFactory{{assist.Role, assist.Register}},
		"HORUS_ASSIST_DEFAULT_ENABLED=true", "HORUS_ASSIST_PROVIDER=openai_compatible", "HORUS_ASSIST_BASE_URL="+llm.URL,
		"HORUS_ASSIST_MODEL=fake-1", "HORUS_ASSIST_ALLOW_LOOPBACK=true", "HORUS_ASSIST_DAILY_LIMIT=3")
	pt := a.superadmin().platformToken()
	A, B := a.newISP(pt, "isp-ia"), a.newISP(pt, "isp-ib")
	ids := a.fixtureISP(A, "10.10.0.0/24")
	fid := ids["finding_id"]

	st := a.must(a.do(req{Method: "GET", Path: "/api/v1/assist/status", Token: A.token}), 200)
	if st.Body["enabled"] != true || st.str("provider") != "openai_compatible" || st.Body["remaining_today"] != float64(3) {
		t.Fatalf("status = %s", st.Raw)
	}
	body := map[string]any{"feature": "finding", "context_id": fid}
	r := a.do(req{Method: "POST", Path: "/api/v1/assist/analyses", Token: A.token, Body: body})
	if r.Status != 200 {
		t.Fatalf("analizar = %d %s", r.Status, r.Raw)
	}
	md := r.str("markdown")
	if !strings.Contains(md, "## Qué está pasando") || r.str("provider") != "openai_compatible" || r.str("model") != "fake-1" || calls.Load() != 1 {
		t.Fatalf("análisis = %s (llamadas %d)", r.Raw, calls.Load())
	}
	if g, _ := r.Body["grounding"].(map[string]any); g == nil || g["status"] != "verified" {
		t.Fatalf("anclaje = %v", r.Body["grounding"])
	}
	if tc, _ := r.Body["tool_calls"].([]any); len(tc) == 0 {
		t.Fatalf("sin herramientas precargadas: %s", r.Raw)
	}
	st = a.must(a.do(req{Method: "GET", Path: "/api/v1/assist/status", Token: A.token}), 200)
	if st.Body["remaining_today"] != float64(2) {
		t.Fatalf("límite tras un análisis = %s", st.Raw)
	}
	// El otro ISP no ve el hallazgo: 404 y el proveedor no se llama.
	if r := a.do(req{Method: "POST", Path: "/api/v1/assist/analyses", Token: B.token, Body: body}); r.Status != 404 || r.code() != "ASSIST_CONTEXT_NOT_FOUND" || calls.Load() != 1 {
		t.Fatalf("otro ISP = %d %s (llamadas %d)", r.Status, r.Raw, calls.Load())
	}
	// Historial: A vuelve a ver el análisis sin gastar consulta; B no ve el hallazgo.
	hist := a.must(a.do(req{Method: "GET", Path: "/api/v1/assist/analyses?feature=finding&context_id=" + fid, Token: A.token}), 200)
	if items, _ := hist.Body["data"].([]any); len(items) != 1 || items[0].(map[string]any)["markdown"] != md {
		t.Fatalf("historial = %s", hist.Raw)
	}
	if r := a.do(req{Method: "GET", Path: "/api/v1/assist/analyses?feature=finding&context_id=" + fid, Token: B.token}); r.Status != 404 {
		t.Fatalf("historial de otro ISP = %d %s", r.Status, r.Raw)
	}
	if st := a.must(a.do(req{Method: "GET", Path: "/api/v1/assist/status", Token: A.token}), 200); st.Body["remaining_today"] != float64(2) {
		t.Fatalf("ver el historial consumió cupo: %s", st.Raw)
	}
	// Hallazgo inexistente: 404 también.
	if r := a.do(req{Method: "POST", Path: "/api/v1/assist/analyses", Token: A.token, Body: map[string]any{"feature": "finding", "context_id": uuid.NewString()}}); r.Status != 404 {
		t.Fatalf("inexistente = %d %s", r.Status, r.Raw)
	}
	// Auditoría del ISP con el análisis.
	audit := a.must(a.do(req{Method: "GET", Path: "/api/v1/assist/audit", Token: A.token}), 200)
	if !strings.Contains(string(audit.Raw), `"outcome":"success"`) {
		t.Fatalf("auditoría = %s", audit.Raw)
	}
}
