package main

import (
	"strings"
	"testing"
)

func TestEffectiveConfigHasNoSecrets(t *testing.T) {
	env := []string{
		"HORUS_ENV=prod",
		"HORUS_POSTGRES_PASSWORD=s3cr3t-pg",
		"HORUS_POSTGRES_PASSWORD_FILE=/run/secrets/postgres_password",
		"HORUS_NATS_URL=nats://horus:n4ts-pass@nats:4222",
		"HORUS_TELEGRAM_BOT_TOKEN=123:abcdefghijklmnopqrstuvwxyz0123456789",
		"HORUS_AUTH_SIGNING_KEY=-----BEGIN PRIVATE KEY-----xyz",
		"HORUS_ALERTS_KEK=deadbeef",
		"HORUS_JWT_PUBLIC_KEYS=-----BEGIN PUBLIC KEY-----abc",
		"HORUS_CLICKHOUSE_DSN=clickhouse://horus:ch-pass@clickhouse:9000/horus?password=qpass",
		"HORUS_SEED_ADMIN_EMAIL=admin@isp.example",
		"PATH=/usr/bin",
		"GOMEMLIMIT=1800MiB",
	}
	cfg := effectiveConfig(env)
	all := ""
	for k, v := range cfg {
		all += k + "=" + v + "\n"
	}
	for _, secret := range []string{"s3cr3t-pg", "n4ts-pass", "abcdefghijklmnop", "BEGIN PRIVATE", "deadbeef", "ch-pass", "qpass", "admin@isp.example"} {
		if strings.Contains(all, secret) {
			t.Errorf("secret %q leaked:\n%s", secret, all)
		}
	}
	if cfg["HORUS_ENV"] != "prod" || cfg["HORUS_POSTGRES_PASSWORD_FILE"] != "/run/secrets/postgres_password" || cfg["GOMEMLIMIT"] != "1800MiB" {
		t.Fatalf("non-secret values lost: %v", cfg)
	}
	if _, ok := cfg["PATH"]; ok {
		t.Fatal("non-Horus variables must not be included")
	}
	if !strings.HasPrefix(cfg["HORUS_NATS_URL"], "nats://horus:xxxxx@nats:4222") {
		t.Fatalf("nats url = %s", cfg["HORUS_NATS_URL"])
	}
	prev := map[string]string{"HORUS_ENV": "staging", "HORUS_GONE": "1"}
	if d := configDiff(prev, map[string]string{"HORUS_ENV": "prod", "HORUS_NEW": "x"}); strings.Join(d, ",") != "HORUS_ENV,HORUS_GONE,HORUS_NEW" {
		t.Fatalf("diff = %v", d)
	}
	if configHash(cfg) != configHash(effectiveConfig(env)) {
		t.Fatal("hash not stable")
	}
}
