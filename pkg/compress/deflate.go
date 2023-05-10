package compress

import (
	"compress/flate"
	"io"
)

// Deflate creates new deflate compress middleware
func Deflate() *Compress {
	return &Compress{
		New: func() Compressor {
			g, err := flate.NewWriter(io.Discard, flate.DefaultCompression)
			if err != nil {
				panic(err)
			}
			return g
		},
		Encoding:  "deflate",
		Vary:      defaultCompressVary,
		Types:     defaultCompressTypes,
		MinLength: defaultCompressMinLength,
	}
}
