package gcs_test

import (
	"context"
	"net/http"

	"cloud.google.com/go/storage"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/gcs"
)

// Serve a bucket's objects through the proxy: requests are mapped onto objects
// under "assets/" in the bucket and streamed back to the client.
func ExampleNew() {
	client, err := storage.NewClient(context.Background())
	if err != nil {
		return
	}

	s := parapet.New()
	s.Use(gcs.New(client, "my-bucket", "assets"))
}

// Configure the proxy as a single-page-application host: "/" serves index.html,
// any missing object falls back to 404.html (so client-side routing works), and
// requests outside the bucket fall through to another handler.
func ExampleGCS() {
	client, err := storage.NewClient(context.Background())
	if err != nil {
		return
	}

	m := &gcs.GCS{
		Client:       client,
		Bucket:       "my-bucket",
		BasePath:     "web",
		MainPage:     "index.html",
		NotFoundPage: "404.html",
		Fallback: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "not found", http.StatusNotFound)
		}),
	}

	s := parapet.New()
	s.Use(m)
}
