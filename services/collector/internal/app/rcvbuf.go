package app

import (
	"net"
	"syscall"
)

// DefaultUDPReadBuffer es el búfer de recepción que pide cada socket (32 MiB):
// ≈ 1–2 s de datagramas IPFIX de un ISP de 10 000 clientes en pico con la CPU
// ocupada. El kernel lo limita a net.core.rmem_max salvo SO_RCVBUFFORCE
// (CAP_NET_ADMIN); el instalador debe subir rmem_max a este valor
// (docs/architecture.md §10.14).
const DefaultUDPReadBuffer = 32 << 20

// setReadBuffer pide want bytes de búfer de recepción y devuelve lo que el
// kernel concedió (Linux informa el doble de lo reservado: se divide entre 2).
func setReadBuffer(c *net.UDPConn, want int) int {
	raw, err := c.SyscallConn()
	if err != nil {
		_ = c.SetReadBuffer(want)
		return 0
	}
	got := 0
	_ = raw.Control(func(fd uintptr) {
		// Con CAP_NET_ADMIN, SO_RCVBUFFORCE ignora rmem_max; si no, el normal.
		if soRcvbufForce != 0 && syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, soRcvbufForce, want) == nil {
			got = want
		} else {
			_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, want)
		}
		if v, err := syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF); err == nil {
			got = v / rcvbufFactor
		}
	})
	return got
}
