package prom

import (
	"net/http"
	"strconv"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/moonrhythm/parapet"
)

type requests struct {
	once sync.Once
	vec  *prometheus.CounterVec
}

var _requests requests

func (p *requests) init() {
	p.once.Do(func() {
		p.vec = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "requests",
		}, []string{"host", "status", "method"})
		reg.MustRegister(p.vec)
	})
}

func (p *requests) ServeHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := prometheus.Labels{
			"method": r.Method,
			"host":   r.Host,
		}
		nw := requestTrackRW{
			ResponseWriter: w,
		}
		defer func() {
			l["status"] = strconv.Itoa(nw.status)
			counter, err := p.vec.GetMetricWith(l)
			if err != nil {
				return
			}
			counter.Inc()
		}()

		h.ServeHTTP(&nw, r)
	})
}

// Requests collects request count
func Requests() parapet.Middleware {
	_requests.init()

	return &_requests
}

type requestTrackRW struct {
	http.ResponseWriter

	wroteHeader bool
	status      int
}

func (w *requestTrackRW) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *requestTrackRW) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func (w *requestTrackRW) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
