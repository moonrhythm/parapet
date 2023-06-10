//go:build cbrotli

package compress

import (
	"io"

	"github.com/google/brotli/go/cbrotli"
)

// Br creates new brotli compress middleware
func Br() *Compress {
	return BrWithOption(cbrotli.WriterOptions{Quality: 4})
}

func BrWithQuality(quality int) *Compress {
	return BrWithOption(cbrotli.WriterOptions{Quality: quality})
}

func BrWithOption(opt cbrotli.WriterOptions) *Compress {
	return &Compress{
		New: func() Compressor {
			return &brWriter{opt: &opt}
		},
		Encoding:  "br",
		Vary:      defaultCompressVary,
		Types:     defaultCompressTypes,
		MinLength: defaultCompressMinLength,
	}
}

type brWriter struct {
	*cbrotli.Writer

	opt *cbrotli.WriterOptions
}

func (w *brWriter) Reset(p io.Writer) {
	w.Writer = cbrotli.NewWriter(p, *w.opt)
}
