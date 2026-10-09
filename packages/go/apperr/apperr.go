// Package apperr define errores de aplicación con código estable del
// contrato (ErrorCode de common.yaml) y su traducción a RFC 9457. Los casos
// de uso devuelven *Error; el adaptador HTTP los escribe con [WriteHTTP] y
// trata cualquier otro error como 500 sin detalles (docs/conventions.md §2.4).
package apperr

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/hcdestroyer/horus-flow/packages/go/problem"
)

// Kind clasifica el error (determina el estado HTTP).
type Kind int

// Clases de error.
const (
	KindInvalid Kind = iota + 1
	KindBadRequest
	KindUnauthorized
	KindForbidden
	KindNotFound
	KindConflict
	KindPreconditionFailed
	KindPreconditionRequired
	KindTooManyRequests
	KindUnavailable
	// KindBadGateway: un sistema externo (p. ej. el router) no responde (502).
	KindBadGateway
)

var kindStatus = map[Kind]int{
	KindInvalid:              http.StatusUnprocessableEntity,
	KindBadRequest:           http.StatusBadRequest,
	KindUnauthorized:         http.StatusUnauthorized,
	KindForbidden:            http.StatusForbidden,
	KindNotFound:             http.StatusNotFound,
	KindConflict:             http.StatusConflict,
	KindPreconditionFailed:   http.StatusPreconditionFailed,
	KindPreconditionRequired: http.StatusPreconditionRequired,
	KindTooManyRequests:      http.StatusTooManyRequests,
	KindUnavailable:          http.StatusServiceUnavailable,
	KindBadGateway:           http.StatusBadGateway,
}

// Error es un error de aplicación con código del contrato.
type Error struct {
	Kind    Kind
	Code    string
	Title   string
	Detail  string
	Fields  []problem.FieldError
	Current any // representación actual (412)
	// RetryAfter en segundos (429).
	RetryAfter int
}

func (e *Error) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Detail)
	}
	return e.Code
}

// Status devuelve el estado HTTP.
func (e *Error) Status() int {
	if s, ok := kindStatus[e.Kind]; ok {
		return s
	}
	return http.StatusInternalServerError
}

// New crea un error.
func New(kind Kind, code, detail string) *Error {
	return &Error{Kind: kind, Code: code, Detail: detail}
}

// NotFound es un 404 con code.
func NotFound(code string) *Error { return &Error{Kind: KindNotFound, Code: code} }

// Forbidden es un 403 con code.
func Forbidden(code, detail string) *Error {
	return &Error{Kind: KindForbidden, Code: code, Detail: detail}
}

// Conflict es un 409 con code.
func Conflict(code, detail string) *Error {
	return &Error{Kind: KindConflict, Code: code, Detail: detail}
}

// Validation es un 422 VALIDATION_FAILED con errores de campo.
func Validation(fields ...problem.FieldError) *Error {
	return &Error{Kind: KindInvalid, Code: problem.CodeValidationFailed, Fields: fields,
		Detail: fmt.Sprintf("%d campo(s) no pasaron la validación", len(fields))}
}

// Field construye un error de campo.
func Field(field, code, msg string) problem.FieldError {
	return problem.FieldError{Field: field, Code: code, Message: msg}
}

// PreconditionRequired es el 428 por falta de If-Match / Idempotency-Key.
func PreconditionRequired(detail string) *Error {
	return &Error{Kind: KindPreconditionRequired, Code: problem.CodePreconditionRequired, Detail: detail}
}

// PreconditionFailed es el 412 con la representación actual.
func PreconditionFailed(current any) *Error {
	return &Error{Kind: KindPreconditionFailed, Code: problem.CodePreconditionFailed, Current: current}
}

// As extrae un *Error.
func As(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

// WriteHTTP escribe err como problem+json. Un error que no es *Error se
// registra una vez y se responde 500 INTERNAL sin detalles.
func WriteHTTP(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	e, ok := As(err)
	if !ok {
		if logger != nil {
			logger.ErrorContext(r.Context(), "request failed", slog.String("method", r.Method),
				slog.String("route", r.Pattern), slog.Any("error", err))
		}
		problem.Std(w, r, http.StatusInternalServerError, problem.CodeInternal)
		return
	}
	title := e.Title
	if title == "" {
		title = problem.Title(e.Code)
	}
	opts := []problem.Option{}
	if e.Detail != "" {
		opts = append(opts, problem.WithDetail(e.Detail))
	}
	if len(e.Fields) > 0 {
		opts = append(opts, problem.WithErrors(e.Fields...))
	}
	if e.Current != nil {
		opts = append(opts, problem.WithCurrent(e.Current))
	}
	if e.RetryAfter > 0 {
		w.Header().Set("Retry-After", fmt.Sprint(e.RetryAfter))
	}
	problem.Write(w, r, e.Status(), e.Code, title, opts...)
}
