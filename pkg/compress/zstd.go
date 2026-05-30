package compress

import (
	"io"

	"github.com/klauspost/compress/zstd"
)

// Zstd creates new zstd compress middleware
func Zstd() *Compress {
	return ZstdWithLevel(zstd.SpeedDefault)
}

func ZstdWithLevel(level zstd.EncoderLevel) *Compress {
	return &Compress{
		New: func() Compressor {
			g, err := zstd.NewWriter(io.Discard, zstd.WithEncoderLevel(level))
			if err != nil {
				panic(err)
			}
			return g
		},
		Encoding:  "zstd",
		Vary:      defaultCompressVary,
		Types:     defaultCompressTypes,
		MinLength: defaultCompressMinLength,
	}
}
