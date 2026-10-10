//go:build linux

package app

import "syscall"

const (
	soRcvbufForce = syscall.SO_RCVBUFFORCE
	rcvbufFactor  = 2 // Linux devuelve el doble (incluye la sobrecarga de contabilidad)
)
