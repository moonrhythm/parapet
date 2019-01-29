# parapet

[![Build Status](https://travis-ci.org/moonrhythm/parapet.svg?branch=master)](https://travis-ci.org/moonrhythm/parapet)
[![codecov](https://codecov.io/gh/moonrhythm/parapet/branch/master/graph/badge.svg)](https://codecov.io/gh/moonrhythm/parapet)
[![Go Report Card](https://goreportcard.com/badge/github.com/moonrhythm/parapet)](https://goreportcard.com/report/github.com/moonrhythm/parapet)
[![GoDoc](https://godoc.org/github.com/moonrhythm/parapet?status.svg)](https://godoc.org/github.com/moonrhythm/parapet)

Reverse Proxy Framework

## Example

```go
package main

import (
	"log"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/body"
	"github.com/moonrhythm/parapet/pkg/compress"
	"github.com/moonrhythm/parapet/pkg/headers"
	"github.com/moonrhythm/parapet/pkg/healthz"
	"github.com/moonrhythm/parapet/pkg/host"
	"github.com/moonrhythm/parapet/pkg/hsts"
	"github.com/moonrhythm/parapet/pkg/location"
	"github.com/moonrhythm/parapet/pkg/logger"
	"github.com/moonrhythm/parapet/pkg/ratelimit"
	"github.com/moonrhythm/parapet/pkg/redirect"
	"github.com/moonrhythm/parapet/pkg/requestid"
	"github.com/moonrhythm/parapet/pkg/upstream"
	"github.com/moonrhythm/parapet/pkg/upstream/transport"
	"github.com/moonrhythm/parapet/pkg/upstream/loadbalancer"
)

func main() {
	s := parapet.New()
	s.Use(logger.Stdout())
	s.Use(requestid.New())
	s.Use(ratelimit.FixedWindowPerSecond(60))
	s.Use(ratelimit.FixedWindowPerMinute(300))
	s.Use(ratelimit.FixedWindowPerHour(2000))
	s.Use(body.LimitRequest(15 * 1024 * 1024)) // 15 MiB
	s.Use(body.BufferRequest())
	s.Use(compress.Gzip())
	s.Use(compress.Br())
	
	// sites
	s.Use(example())
	s.Use(mysite())
	s.Use(wordpress())
	
	// health check
	{
		l := location.Exact("/healthz")
		l.Use(logger.Disable())
		l.Use(healthz.New())
		s.Use(l)
	}
	
	s.Addr = ":8080"
	err := s.ListenAndServe()
	if err != nil {
		log.Fatal(err)
	}
}

func example() parapet.Middleware {
	h := host.New("example.com", "www.example.com")
	h.Use(ratelimit.FixedWindowPerSecond(20))
	h.Use(redirect.HTTPS())
	h.Use(hsts.Preload())
	h.Use(redirect.NonWWW())
	h.Use(upstream.New(loadbalancer.NewRoundRobin([]*loadbalancer.Target{
		{Host: "example.default.svc.cluster.local:8080", Transport: &transport.HTTP{}},
		{Host: "example1.default.svc.cluster.local:8080", Transport: &transport.H2C{}},
		{Host: "myexamplebackuphost.com", Transport: &transport.HTTPS{}},
	})))
	
	return h
}

func mysite() parapet.Middleware {
	var hs parapet.Middlewares
	
	{
		h := host.New("mysiteaaa.io", "www.mysiteaaa.io")
		h.Use(ratelimit.FixedWindowPerSecond(15))
		h.Use(redirect.HTTPS())
		h.Use(hsts.Preload())
		h.Use(redirect.WWW())
		h.Use(headers.DeleteResponse(
			"Server",
			"x-goog-generation",
			"x-goog-hash",
			"x-goog-meta-goog-reserved-file-mtime",
			"x-goog-metageneration",
			"x-goog-storage-class",
			"x-goog-stored-content-encoding",
			"x-goog-stored-content-length",
			"x-guploader-uploadid",
		))
		h.Use(upstream.SingleHost("storage.googleapis.com", &transport.HTTPS{}))
	
		hs.Use(h)
	}
	
	{
		h := host.New("mail.mysiteaaa.io")
		h.Use(redirect.HTTPS())
		h.Use(hsts.Preload())
		h.Use(redirect.To("https://mail.google.com/a/mysiteaaa.io", 302))
	
		hs.Use(h)
	}
	
	return hs
}

func wordpress() parapet.Middleware {
	h := host.New("myblogaaa.com", "www.myblogaaa.com")
	h.Use(ratelimit.FixedWindowPerMinute(150))
	h.Use(redirect.HTTPS())
	h.Use(hsts.Preload())
	h.Use(redirect.NonWWW())
	
	backend := upstream.SingleHost("wordpress.default.svc.cluster.local", &transport.HTTP{})
	
	l := location.RegExp(`\.(js|css|svg|png|jp(e)?g|gif)$`)
	l.Use(headers.SetResponse("Cache-Control", "public, max-age=31536000"))
	l.Use(backend)
	h.Use(l)
	
	h.Use(backend)
	
	return h
}
```

## License

MIT
