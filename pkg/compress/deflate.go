package compress

import (
	"compress/flate"
	"io/ioutil"
)

// Deflate creates new deflate compress middleware
func Deflate() *Compress {
	return &Compress{
		New: func() Compressor {
			g, err := flate.NewWriter(ioutil.Discard, flate.DefaultCompression)
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
