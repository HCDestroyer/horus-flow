package datasets

import (
	"bufio"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
)

// Decompress detecta por los primeros bytes si r viene en gzip, bzip2 o zstd
// y devuelve un lector del contenido plano (o r tal cual). Los datasets se
// guardan tal como se descargan y se descomprimen al validarlos o compilarlos.
func Decompress(r io.Reader) (io.ReadCloser, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	head, _ := br.Peek(4)
	switch {
	case bytes.HasPrefix(head, []byte{0x1f, 0x8b}):
		zr, err := gzip.NewReader(br)
		if err != nil {
			return nil, fmt.Errorf("gzip: %w", err)
		}
		return zr, nil
	case bytes.HasPrefix(head, []byte("BZh")):
		return io.NopCloser(bzip2.NewReader(br)), nil
	case bytes.HasPrefix(head, []byte{0x28, 0xb5, 0x2f, 0xfd}):
		zr, err := zstd.NewReader(br)
		if err != nil {
			return nil, fmt.Errorf("zstd: %w", err)
		}
		return zr.IOReadCloser(), nil
	default:
		return io.NopCloser(br), nil
	}
}
