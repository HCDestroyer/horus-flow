#!/bin/sh
# horus-ipsec — arranca charon en primer plano (tini es el PID 1). La configuración llega por vici.
set -eu
install -d -m 0770 -o root -g horusvici /run/horus-ipsec
rm -f /run/horus-ipsec/charon.vici /var/run/charon.pid
exec /usr/lib/strongswan/charon
