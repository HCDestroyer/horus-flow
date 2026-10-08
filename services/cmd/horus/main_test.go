package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func TestParseRoles(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr bool
	}{
		{name: "vacío = todos", in: "", want: availableRoles},
		{name: "all", in: "all", want: availableRoles},
		{name: "lista", in: "gateway, devices", want: []string{"gateway", "devices"}},
		{name: "duplicados", in: "collector,collector", want: []string{"collector"}},
		{name: "desconocido", in: "gateway,foo", wantErr: true},
		{name: "solo comas", in: ",,", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseRoles(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseRoles(%q) error = %v, wantErr %v", tc.in, err, tc.wantErr)
			}
			if !tc.wantErr && !slices.Equal(got, tc.want) {
				t.Fatalf("parseRoles(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestRunPrintsVersionAndRoles(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := run(&buf, "collector"); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"horus " + version, "wg-agent", "roles seleccionados (HORUS_ROLES): collector"} {
		if !strings.Contains(out, want) {
			t.Errorf("salida sin %q:\n%s", want, out)
		}
	}
}
