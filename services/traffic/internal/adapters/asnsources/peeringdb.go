package asnsources

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/services/traffic/api/asn"
)

type pdbNet struct {
	ASN       int64    `json:"asn"`
	Name      string   `json:"name"`
	InfoType  string   `json:"info_type"`
	InfoTypes []string `json:"info_types"`
}

type pdbResponse struct {
	Data *[]json.RawMessage `json:"data"`
}

// networkTypes normaliza info_type de PeeringDB a asn.network_type.
var networkTypes = map[string]string{
	"nsp":                  "nsp",
	"content":              "content",
	"cable/dsl/isp":        "access",
	"enterprise":           "enterprise",
	"educational/research": "education",
	"non-profit":           "non_profit",
	"route server":         "route_server",
	"network services":     "network_services",
	"route collector":      "route_collector",
	"government":           "government",
}

// parsePeeringDB interpreta la respuesta de /api/net de PeeringDB
// ({"data":[{"asn":…,"name":…,"info_type":…}], …}) y devuelve, por ASN, el
// nombre de la red y su tipo normalizado.
func parsePeeringDB(r io.Reader, src datasets.Source) (*Result, error) {
	var resp pdbResponse
	if err := json.NewDecoder(r).Decode(&resp); err != nil {
		return nil, fmt.Errorf("%w: JSON: %v", ErrCorrupt, err)
	}
	if resp.Data == nil {
		return nil, fmt.Errorf("%w: falta el campo data", ErrCorrupt)
	}
	res := &Result{ASInfo: map[uint32]asn.ASInfo{}}
	for _, raw := range *resp.Data {
		res.Lines++
		var n pdbNet
		if err := json.Unmarshal(raw, &n); err != nil || n.ASN <= 0 || n.ASN > 0xFFFFFFFF || strings.TrimSpace(n.Name) == "" {
			res.Invalid++
			continue
		}
		typ := n.InfoType
		if typ == "" && len(n.InfoTypes) > 0 {
			typ = n.InfoTypes[0]
		}
		res.ASInfo[uint32(n.ASN)] = asn.ASInfo{
			Name:        strings.TrimSpace(n.Name),
			NetworkType: networkTypes[strings.ToLower(strings.TrimSpace(typ))],
			Source:      src.ID,
		}
	}
	return res, nil
}
