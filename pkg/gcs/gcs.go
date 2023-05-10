package gcs

import (
	"context"
	"io"
	"log"
	"net/http"
	"path"
	"strings"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"github.com/moonrhythm/parapet/pkg/internal/pool"
)

// New creates new gcs backend
func New(client *storage.Client, bucket string, basePath string) *GCS {
	return &GCS{
		Client:   client,
		Bucket:   bucket,
		BasePath: basePath,
	}
}

// GCS proxies request to google cloud storage
//
//nolint:govet
type GCS struct {
	Client       *storage.Client
	Bucket       string
	BasePath     string
	MainPage     string
	NotFoundPage string
	Fallback     http.Handler
}

// ServeHandler implements middleware interface
func (m GCS) ServeHandler(h http.Handler) http.Handler {
	// default fallback
	if m.Fallback == nil {
		m.Fallback = http.NotFoundHandler()
	}

	// short-circuit no bucket
	if m.Bucket == "" {
		return m.Fallback
	}

	ctx := context.Background()

	if m.Client == nil {
		// use default application credential
		m.Client, _ = storage.NewClient(ctx)
	}

	if m.Client == nil {
		// use anonymous account
		m.Client, _ = storage.NewClient(ctx, option.WithoutAuthentication())
	}

	if m.Client == nil {
		log.Println("gcs: can not init storage client")
		return m.Fallback
	}

	// normalize base path
	m.BasePath = strings.TrimPrefix(m.BasePath, "/")
	m.BasePath = strings.TrimSuffix(m.BasePath, "/")

	// normalize pages
	m.MainPage = strings.TrimSpace(m.MainPage)
	m.NotFoundPage = strings.TrimSpace(m.NotFoundPage)

	bucket := m.Client.Bucket(m.Bucket)

	var serve func(http.ResponseWriter, *http.Request, string, bool)
	serve = func(w http.ResponseWriter, r *http.Request, p string, fallbackNotFound bool) {
		obj := bucket.Object(strings.TrimPrefix(path.Join(m.BasePath, p), "/"))

		reader, err := obj.NewReader(r.Context())
		if err != nil {
			if fallbackNotFound && m.NotFoundPage != "" {
				serve(w, r, m.NotFoundPage, false)
				return
			}
			m.Fallback.ServeHTTP(w, r)
			return
		}
		defer reader.Close()

		h := w.Header()
		if v := reader.Attrs.ContentType; v != "" {
			h.Set("Content-Type", v)
		}
		if v := reader.Attrs.CacheControl; v != "" {
			h.Set("Cache-Control", v)
		}

		b := pool.Get()
		defer pool.Put(b)
		io.CopyBuffer(w, reader, *b)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if r.URL.Path == "/" && m.MainPage != "" {
			p = m.MainPage
		}
		serve(w, r, p, true)
	})
}
