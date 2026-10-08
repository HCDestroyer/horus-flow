package observability

import "log/slog"

// Redacted es el texto que sustituye a un secreto en logs y JSON.
const Redacted = "[REDACTED]"

// Secret guarda un valor sensible (contraseña, token, clave). Se puede usar
// como campo de una configuración con etiquetas env. Al formatearlo, loguearlo
// o serializarlo a JSON siempre produce [Redacted]; el valor real solo se
// obtiene con Reveal.
type Secret string

// Reveal devuelve el valor real. Úsese solo en el punto donde se consume.
func (s Secret) Reveal() string { return string(s) }

// IsZero indica si el secreto está vacío.
func (s Secret) IsZero() bool { return s == "" }

// String implementa fmt.Stringer.
func (Secret) String() string { return Redacted }

// GoString implementa fmt.GoStringer (%#v).
func (Secret) GoString() string { return Redacted }

// LogValue implementa slog.LogValuer.
func (Secret) LogValue() slog.Value { return slog.StringValue(Redacted) }

// MarshalJSON implementa json.Marshaler.
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + Redacted + `"`), nil }

// MarshalText implementa encoding.TextMarshaler.
func (Secret) MarshalText() ([]byte, error) { return []byte(Redacted), nil }
