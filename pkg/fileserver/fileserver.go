package fileserver

import (
	"net/http"
	"os"
)

// FileServer serves file
type FileServer struct {
	Root          string
	ListDirectory bool
}

// New creates new file server
func New(root string) *FileServer {
	return &FileServer{Root: root}
}

// ServeHandler implements middleware interface
func (m FileServer) ServeHandler(h http.Handler) http.Handler {
	fs := http.FileServer(&fileSystem{
		root:    m.Root,
		listDir: m.ListDirectory,
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.ServeHTTP(&responseWriter{
			ResponseWriter: w,
			notFound:       func() { h.ServeHTTP(w, r) },
		}, r)
	})
}

type fileSystem struct {
	root    string
	listDir bool
}

func (fs *fileSystem) Open(name string) (http.File, error) {
	f, err := http.Dir(fs.root).Open(name)
	if err != nil {
		return nil, err
	}

	if !fs.listDir {
		fi, err := f.Stat()
		if err != nil {
			return nil, err
		}
		if fi.IsDir() {
			return nil, os.ErrNotExist
		}
	}

	return f, nil
}

type responseWriter struct {
	http.ResponseWriter
	wroteHeader bool
	header      http.Header
	noop        bool
	notFound    func()
}

func (w *responseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *responseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true

	h := w.ResponseWriter.Header()

	if code == http.StatusNotFound {
		w.noop = true
		w.notFound()
		return
	}

	for k, v := range w.header {
		for _, vv := range v {
			h.Add(k, vv)
		}
	}

	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.noop {
		return len(p), nil
	}
	return w.ResponseWriter.Write(p)
}

func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
