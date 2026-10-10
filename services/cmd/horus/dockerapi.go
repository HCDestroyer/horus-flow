package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// dockerAPI es un cliente mínimo de la API de Docker por su socket unix
// (solo lectura: listar contenedores, inspeccionar y leer logs). Evita
// enlazar el SDK de Docker en el binario de producción.
type dockerAPI struct {
	hc *http.Client
}

func newDockerAPI(socket string) *dockerAPI {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}}
	return &dockerAPI{hc: &http.Client{Transport: tr, Timeout: 60 * time.Second}}
}

func (d *dockerAPI) get(ctx context.Context, path string, q url.Values) (*http.Response, error) {
	u := "http://docker/v1.43" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err //nolint:wrapcheck // error de net/http autoexplicativo
	}
	resp, err := d.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("docker %s: HTTP %d %s", path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return resp, nil
}

type dockerContainer struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	State  string            `json:"State"`
	Status string            `json:"Status"`
	Labels map[string]string `json:"Labels"`
}

func (c dockerContainer) name() string {
	if len(c.Names) > 0 {
		return strings.TrimPrefix(c.Names[0], "/")
	}
	return c.ID[:12]
}

// containers lista los contenedores (también parados) del proyecto compose.
func (d *dockerAPI) containers(ctx context.Context, project string) ([]dockerContainer, error) {
	filters, _ := json.Marshal(map[string][]string{"label": {"com.docker.compose.project=" + project}})
	resp, err := d.get(ctx, "/containers/json", url.Values{"all": {"1"}, "filters": {string(filters)}})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out []dockerContainer
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("docker containers: %w", err)
	}
	return out, nil
}

// inspect devuelve el estado del contenedor sin su configuración (el
// entorno puede llevar datos sensibles): estado, reinicios, OOM, salud.
func (d *dockerAPI) inspect(ctx context.Context, id string) (map[string]any, error) {
	resp, err := d.get(ctx, "/containers/"+id+"/json", nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var full struct {
		Name         string         `json:"Name"`
		Created      string         `json:"Created"`
		RestartCount int            `json:"RestartCount"`
		Image        string         `json:"Image"`
		State        map[string]any `json:"State"`
		Config       struct {
			Image  string            `json:"Image"`
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
		HostConfig struct {
			RestartPolicy map[string]any `json:"RestartPolicy"`
			LogConfig     map[string]any `json:"LogConfig"`
			Memory        int64          `json:"Memory"`
		} `json:"HostConfig"`
		LogPath string `json:"LogPath"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&full); err != nil {
		return nil, fmt.Errorf("docker inspect: %w", err)
	}
	return map[string]any{"name": strings.TrimPrefix(full.Name, "/"), "created": full.Created, "restart_count": full.RestartCount,
		"image": full.Config.Image, "image_id": full.Image, "state": full.State, "restart_policy": full.HostConfig.RestartPolicy,
		"log_config": full.HostConfig.LogConfig, "memory_limit": full.HostConfig.Memory, "log_path": full.LogPath,
		"compose_service": full.Config.Labels["com.docker.compose.service"], "compose_project": full.Config.Labels["com.docker.compose.project"]}, nil
}

// logs escribe en w las líneas de log del contenedor desde since (stdout y
// stderr, con marca de tiempo de Docker), como mucho tail líneas.
func (d *dockerAPI) logs(ctx context.Context, id string, since time.Duration, tail int, w io.Writer) error {
	q := url.Values{"stdout": {"1"}, "stderr": {"1"}, "timestamps": {"1"}, "tail": {strconv.Itoa(tail)}}
	if since > 0 {
		q.Set("since", strconv.FormatInt(time.Now().Add(-since).Unix(), 10))
	}
	resp, err := d.get(ctx, "/containers/"+id+"/logs", q)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if strings.Contains(resp.Header.Get("Content-Type"), "multiplexed") {
		return demux(resp.Body, w)
	}
	_, err = io.Copy(w, resp.Body)
	return err //nolint:wrapcheck // error de io autoexplicativo
}

// demux separa el flujo multiplexado de Docker (cabecera de 8 bytes por trama).
func demux(r io.Reader, w io.Writer) error {
	br := bufio.NewReader(r)
	hdr := make([]byte, 8)
	for {
		if _, err := io.ReadFull(br, hdr); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err //nolint:wrapcheck // error de io autoexplicativo
		}
		n := binary.BigEndian.Uint32(hdr[4:])
		if _, err := io.CopyN(w, br, int64(n)); err != nil {
			return err //nolint:wrapcheck // error de io autoexplicativo
		}
	}
}
