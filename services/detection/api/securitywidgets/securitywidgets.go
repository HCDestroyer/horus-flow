// Package securitywidgets es el contrato en proceso con el que el catálogo
// de dashboards (analytics/dashboards, I1-15) pide a SEC los datos de los
// widgets de seguridad de C9 (findings_summary, botnet_signals,
// findings_feed, findings_trend, security_by_node, watched_ports). Usa los
// mismos tipos que el proveedor de tráfico
// (services/analytics/api/trafficwidgets): mismo sobre WidgetData y mismas
// formas que lee el frontend (apps/frontend/app/widgets/shapes.ts).
//
// Quien llama comprueba el permiso del tipo y la asignación del kiosco; SEC
// resuelve con el tenant y los nodos visibles del espectador y, si
// ShowPersonalData es false (kiosco sin show_personal_data o usuario sin
// customers.read), enmascara la IP del cliente y omite el alias.
package securitywidgets

import tw "github.com/hcdestroyer/horus-flow/services/analytics/api/trafficwidgets"

// ServiceWidgetData es el nombre en module.Services de la implementación (tw.Provider).
const ServiceWidgetData = "detection.SecurityWidgetData"

// Types son los tipos de widget que resuelve detection.
var Types = []string{"findings_summary", "botnet_signals", "findings_feed", "findings_trend", "security_by_node", "watched_ports"}

// Provider es el valor registrado en module.Services.
type Provider = tw.Provider
