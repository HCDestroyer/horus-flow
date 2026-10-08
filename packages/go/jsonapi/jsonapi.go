// Package jsonapi reúne las reglas transversales de los handlers REST del
// contrato (docs/api.md §1): lectura de JSON con límite de 1 MiB, escritura
// con `Cache-Control: no-store`, `ETag`/`If-Match` sobre `version` y la
// cabecera `Location`.
package jsonapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
)

// MaxBody es el tamaño máximo de un cuerpo (413 si se supera).
const MaxBody = 1 << 20

// Decode lee el cuerpo JSON de r en dst. strict rechaza campos desconocidos
// (`additionalProperties: false`). Escribe el error y devuelve false si
// falla: 415 si el tipo no es JSON, 413 si es demasiado grande, 400 si está
// mal formado.
func Decode(w http.ResponseWriter, r *http.Request, dst any, strict bool) bool {
	if ct := r.Header.Get("Content-Type"); ct != "" {
		mt, _, err := mime.ParseMediaType(ct)
		if err != nil || (mt != "application/json" && mt != "application/merge-patch+json") {
			problem.Std(w, r, http.StatusUnsupportedMediaType, problem.CodeUnsupportedMediaType)
			return false
		}
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBody))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			problem.Std(w, r, http.StatusRequestEntityTooLarge, problem.CodePayloadTooLarge)
			return false
		}
		problem.Write(w, r, http.StatusBadRequest, problem.CodeValidationFailed, "Cuerpo ilegible")
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(dst); err != nil {
		msg := "JSON mal formado"
		var ute *json.UnmarshalTypeError
		switch {
		case errors.As(err, &ute):
			problem.Write(w, r, http.StatusBadRequest, problem.CodeValidationFailed, "Tipo de dato inválido",
				problem.WithErrors(problem.FieldError{Field: ute.Field, Code: "INVALID_TYPE", Message: "tipo inválido"}))
			return false
		case strings.HasPrefix(err.Error(), "json: unknown field"):
			f := strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), `"`)
			problem.Write(w, r, http.StatusUnprocessableEntity, problem.CodeValidationFailed, problem.Title(problem.CodeValidationFailed),
				problem.WithErrors(problem.FieldError{Field: f, Code: "UNKNOWN_FIELD", Message: "campo no permitido"}))
			return false
		case errors.Is(err, io.EOF):
			msg = "Cuerpo vacío"
		}
		problem.Write(w, r, http.StatusBadRequest, problem.CodeValidationFailed, msg)
		return false
	}
	if dec.More() {
		problem.Write(w, r, http.StatusBadRequest, problem.CodeValidationFailed, "JSON mal formado")
		return false
	}
	return true
}

// Write escribe v como JSON con el estado dado.
func Write(w http.ResponseWriter, status int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	if h.Get("Cache-Control") == "" {
		h.Set("Cache-Control", "no-store")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // el cliente puede haber cerrado
}

// ETag formatea una versión (`"7"`).
func ETag(version int) string { return `"` + strconv.Itoa(version) + `"` }

// IfMatch lee la versión de `If-Match`. Sin cabecera → 428; mal formada →
// 412 (no puede coincidir).
func IfMatch(r *http.Request) (int, error) {
	v := strings.TrimSpace(r.Header.Get("If-Match"))
	if v == "" {
		return 0, apperr.PreconditionRequired("Falta la cabecera If-Match con el ETag del recurso.")
	}
	v = strings.TrimPrefix(v, "W/")
	n, err := strconv.Atoi(strings.Trim(v, `"`))
	if err != nil || n < 1 {
		return -1, nil
	}
	return n, nil
}

// SetLocation fija `Location` (absoluta con base si se configuró
// HORUS_PUBLIC_BASE_URL, nunca a partir de `Host`).
func SetLocation(w http.ResponseWriter, base, path string) {
	w.Header().Set("Location", strings.TrimRight(base, "/")+path)
}
