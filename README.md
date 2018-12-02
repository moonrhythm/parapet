# parapet

Reverse Proxy and API Gateway Framework

## Example

```go
package main

import (
    "fmt"
    "log"
    "net/http"
    "time"

    "github.com/moonrhythm/parapet"
    "github.com/moonrhythm/parapet/pkg/addheaders"
    "github.com/moonrhythm/parapet/pkg/hideheaders"
    "github.com/moonrhythm/parapet/pkg/host"
    "github.com/moonrhythm/parapet/pkg/logger"
    "github.com/moonrhythm/parapet/pkg/reqid"
    "github.com/moonrhythm/parapet/pkg/router"
    "github.com/moonrhythm/parapet/pkg/timeout"
    "github.com/moonrhythm/parapet/pkg/upstream"
)

func main() {
    time.Local = time.UTC

    // start mock upstream server
    {
        go http.ListenAndServe(":8081", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            fmt.Fprintf(w, "server 1\nProto: %s\n", r.Proto)
        }))
        b2 := parapet.NewBackend()
        b2.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            fmt.Fprintf(w, "server 1 api\nProto: %s\n", r.Proto)
        })
        b2.Addr = ":8082"
        go b2.ListenAndServe()
        go http.ListenAndServe(":8083", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            time.Sleep(3 * time.Second)
            w.Write([]byte("server 2"))
        }))
        go http.ListenAndServeTLS(":8084", "server.crt", "server.key", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            fmt.Fprintf(w, "server 2 api\nProto: %s\n", r.Proto)
        }))
        go http.ListenAndServe(":8085", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            w.Write([]byte("Not Found server"))
        }))
    }

    s := parapet.New()
    s.Use(&reqid.ReqID{TrustProxy: true})
    s.Use(&logger.Logger{})
    s.Use(&timeout.Timeout{Duration: 2 * time.Second, Message: "Timeout!!!"})
    s.Use(&hideheaders.Response{Headers: []string{"X-Powered-By", "Server"}})
    s.Use(&hideheaders.Upstream{Headers: []string{"Authorization"}})
    s.Use(&addheaders.Response{Headers: []addheaders.Header{{Key: "Via", Value: "parapet"}}})
    s.Use(&addheaders.Upstream{Headers: []addheaders.Header{{Key: "X-Proxy", Value: "true"}}})

    {
        web1 := host.New("web1.local")
        r := router.New()
        r.Handle("/", &upstream.Upstream{Target: "http://127.0.0.1:8081", MaxIdleConns: 1000})
        r.Handle("/api", &upstream.Upstream{Target: "h2c://127.0.0.1:8082", MaxIdleConns: 1000})
        web1.Use(r)
        s.Use(web1)
    }

    r := router.New()
    r.Handle("web2.local/", &upstream.Upstream{Target: "http://127.0.0.1:8083", TCPKeepAlive: time.Minute})
    r.Handle("web2.local/api", &upstream.Upstream{Target: "https://127.0.0.1:8084"})
    r.Handle("google.local", &upstream.Upstream{Target: "https://www.google.com", Host: "www.google.com", VerifyCA: true})
    s.Use(r)

    // fallback
    s.Use(&upstream.Upstream{Target: "http://127.0.0.1:8085", DisableKeepAlives: true})

    s.Addr = ":3000"
    err := s.ListenAndServe()
    if err != nil {
        log.Fatal(err)
    }
}
```
