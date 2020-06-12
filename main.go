package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// Attempts is the number of attempts per backend
	Attempts int = iota
	// Retry is the retry state of a backend
	Retry
)

// Backend holds the data about a server
type Backend struct {
	URL          *url.URL
	Alive        bool
	mux          sync.RWMutex
	ReverseProxy *httputil.ReverseProxy
}

// Config holds the configuration read from file
type Config struct {
	Port     int
	Backends []string
}

// SetAlive for this backend
func (b *Backend) SetAlive(alive bool) {
	b.mux.Lock()
	b.Alive = alive
	b.mux.Unlock()
}

// IsAlive returns true when backend is alive
func (b *Backend) IsAlive() (alive bool) {
	b.mux.RLock()
	alive = b.Alive
	b.mux.RUnlock()
	return
}

// ServerPool holds information about reachable backends
type ServerPool struct {
	backends []*Backend
	current  uint64
}

// AddBackend to the server pool
func (s *ServerPool) AddBackend(backend *Backend) {
	s.backends = append(s.backends, backend)
}

// NextIndex atomically increase the counter and return an index
func (s *ServerPool) NextIndex() int {
	return int(atomic.AddUint64(&s.current, uint64(1)) % uint64(len(s.backends)))
}

// MarkBackendStatus changes a status of a backend
func (s *ServerPool) MarkBackendStatus(backendURL *url.URL, alive bool) {
	for _, b := range s.backends {
		if b.URL.String() == backendURL.String() {
			b.SetAlive(alive)
			break
		}
	}
}

// GetNextPeer returns next active peer to take a connection
func (s *ServerPool) GetNextPeer() *Backend {
	// loop entire backends to find out an Alive backend
	next := s.NextIndex()
	l := len(s.backends) + next // start from next and move a full cycle
	for i := next; i < l; i++ {
		idx := i % len(s.backends)     // take an index by modding
		if s.backends[idx].IsAlive() { // if we have an alive backend, use it and store if its not the original one
			if i != next {
				atomic.StoreUint64(&s.current, uint64(idx))
			}
			return s.backends[idx]
		}
	}
	return nil
}

// HealthCheck pings the backends and update the status
func (s *ServerPool) HealthCheck() {
	for _, b := range s.backends {
		status := "up"
		alive := isBackendAlive(b.URL)
		b.SetAlive(alive)
		if !alive {
			status = "down"
		}
		log.Printf("%s [%s]\n", b.URL, status)
	}
}

// GetAttemptsFromContext returns the attempts for request
func GetAttemptsFromContext(r *http.Request) int {
	if attempts, ok := r.Context().Value(Attempts).(int); ok {
		return attempts
	}
	return 1
}

// GetRetryFromContext returns the attempts for request
func GetRetryFromContext(r *http.Request) int {
	if retry, ok := r.Context().Value(Retry).(int); ok {
		return retry
	}
	return 0
}

// lb load balances the incoming request
func lb(w http.ResponseWriter, r *http.Request) {
	attempts := GetAttemptsFromContext(r)
	if attempts > 3 {
		log.Printf("%s(%s) Max attempts reached, terminating\n", r.RemoteAddr, r.URL.Path)
		http.Error(w, "Service not available", http.StatusServiceUnavailable)
		return
	}

	peer := serverPool.GetNextPeer()
	if peer != nil {
		peer.ReverseProxy.ServeHTTP(w, r)
		return
	}
	http.Error(w, "Service not available", http.StatusServiceUnavailable)
}

// isAlive checks whether a backend is Alive by establishing a TCP connection
func isBackendAlive(u *url.URL) bool {
	timeout := 2 * time.Second
	conn, err := net.DialTimeout("tcp", u.Host, timeout)
	if err != nil {
		log.Println("Site unreachable, error: ", err)
		return false
	}
	_ = conn.Close()
	return true
}

// healthCheck runs a routine for check status of the backends every 2 mins
func healthCheck() {
	t := time.NewTicker(time.Minute * 2)
	for {
		select {
		case <-t.C:
			log.Println("Starting health check...")
			serverPool.HealthCheck()
			log.Println("Health check completed")
		}
	}
}

var serverPool ServerPool

func main() {

	var config Config = Config{
		3030,
		[]string{},
	}

	configString, err := ioutil.ReadFile("config.json")

	if err == nil {
		err = json.Unmarshal(configString, &config)

		if err == nil {
			if len(config.Backends) == 0 {
				log.Fatal("Please provide one or more backends to load balance")
			}

			// used to wrap and generate context key
			type contextKey string

			// parse servers
			tokens := config.Backends
			for _, tok := range tokens {
				serverURL, err := url.Parse(tok)
				if err != nil {
					log.Fatal(err)
				}

				proxy := httputil.NewSingleHostReverseProxy(serverURL)
				proxy.ErrorHandler = func(writer http.ResponseWriter, request *http.Request, e error) {
					log.Printf("[%s] %s\n", serverURL.Host, e.Error())
					retries := GetRetryFromContext(request)
					if retries < 3 {
						select {
						case <-time.After(10 * time.Millisecond):
							ctx := context.WithValue(request.Context(), contextKey(Retry), retries+1)
							proxy.ServeHTTP(writer, request.WithContext(ctx))
						}
						return
					}

					// after 3 retries, mark this backend as down
					serverPool.MarkBackendStatus(serverURL, false)

					// if the same request routing for few attempts with different backends, increase the count
					attempts := GetAttemptsFromContext(request)
					log.Printf("%s(%s) Attempting retry %d\n", request.RemoteAddr, request.URL.Path, attempts)
					ctx := context.WithValue(request.Context(), contextKey(Attempts), attempts+1)
					lb(writer, request.WithContext(ctx))
				}

				serverPool.AddBackend(&Backend{
					URL:          serverURL,
					Alive:        true,
					ReverseProxy: proxy,
				})
				log.Printf("Configured server: %s\n", serverURL)
			}

			// create http server
			server := http.Server{
				Addr:    fmt.Sprintf(":%d", config.Port),
				Handler: http.HandlerFunc(lb),
			}

			// start health checking
			go healthCheck()

			log.Printf("Load Balancer started at :%d\n", config.Port)
			if err := server.ListenAndServe(); err != nil {
				log.Fatal(err)
			}
		} else {
			log.Fatal("Invalid configuration file")
		}
	} else {
		log.Fatal("Could not load configuration file")
	}
}
