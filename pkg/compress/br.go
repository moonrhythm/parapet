// +build cbrotli

package compress

import (
	"io"

	"github.com/google/brotli/go/cbrotli"
)

// Br creates new brotli compress middleware
func Br() Compress {
	return Compress{
		New: func() Compressor {
			return &brWriter{quality: 4}
		},
		Encoding:  "br",
		Vary:      defaultCompressVary,
		Types:     defaultCompressTypes,
		MinLength: defaultCompressMinLength,
	}
}

type brWriter struct {
	quality int
	*cbrotli.Writer
}

func (w *brWriter) Reset(p io.Writer) {
	w.Writer = cbrotli.NewWriter(p, cbrotli.WriterOptions{Quality: w.quality})
}
