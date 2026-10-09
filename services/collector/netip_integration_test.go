//go:build integration

package collector_test

import "net/netip"

type (
	netipAddr   = netip.Addr
	netipPrefix = netip.Prefix
)

var (
	parseAddr   = netip.ParseAddr
	parsePrefix = netip.ParsePrefix
)
