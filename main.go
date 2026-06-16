package main

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

// Backend representa um servidor de destino
type Backend struct {
	URL          *url.URL
	Alive        bool
	Mutex        sync.RWMutex
	ReverseProxy *httputil.ReverseProxy
	Connections  int32 // conexões ativas (atomic)
	Requests     int64 // total de requisições enviadas (atomic)
}

// ServerPool contém todos os backends e métodos para escolher
type ServerPool struct {
	backends []*Backend
	mu       sync.RWMutex // para proteger a lista durante atualizações (se adicionar/remover)
}

// Inicializa o gerador de números aleatórios
func init() {
	rand.Seed(time.Now().UnixNano())
}

// ChooseBackend implementa Power of Two Choices
func (s *ServerPool) ChooseBackend() *Backend {
	s.mu.RLock()
	backends := s.backends
	s.mu.RUnlock()

	// Filtra vivos
	var vivos []*Backend
	for _, b := range backends {
		b.Mutex.RLock()
		alive := b.Alive
		b.Mutex.RUnlock()
		if alive {
			vivos = append(vivos, b)
		}
	}

	if len(vivos) == 0 {
		return nil
	}
	if len(vivos) == 1 {
		return vivos[0]
	}

	// Sorteia dois índices diferentes
	idx1 := rand.Intn(len(vivos))
	idx2 := rand.Intn(len(vivos))
	for idx1 == idx2 {
		idx2 = rand.Intn(len(vivos))
	}

	b1 := vivos[idx1]
	b2 := vivos[idx2]

	// Compara conexões ativas
	conn1 := atomic.LoadInt32(&b1.Connections)
	conn2 := atomic.LoadInt32(&b2.Connections)

	if conn1 <= conn2 {
		return b1
	}
	return b2
}

// lbHandler é o handler principal do load balancer
func lbHandler(pool *ServerPool, w http.ResponseWriter, r *http.Request) {
	backend := pool.ChooseBackend()
	if backend == nil {
		http.Error(w, "Nenhum servidor disponível", http.StatusServiceUnavailable)
		return
	}

	// Incrementa conexões ativas e total de requisições
	atomic.AddInt32(&backend.Connections, 1)
	atomic.AddInt64(&backend.Requests, 1)

	// Garante que ao final da requisição (ou se houver panic) decrementamos
	defer atomic.AddInt32(&backend.Connections, -1)

	// Log opcional para ver qual backend foi escolhido
	log.Printf("Requisição encaminhada para %s (conexões ativas: %d)",
		backend.URL.Host, atomic.LoadInt32(&backend.Connections))

	// Encaminha a requisição
	backend.ReverseProxy.ServeHTTP(w, r)
}

// CheckHealth testa um backend individual
func (b *Backend) CheckHealth() {
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(b.URL.String() + "/health")
	b.Mutex.Lock()
	defer b.Mutex.Unlock()
	if err != nil || resp.StatusCode != http.StatusOK {
		b.Alive = false
		return
	}
	b.Alive = true
}

// HealthCheck roda periodicamente para todos os backends
func HealthCheck(pool *ServerPool, intervalo time.Duration) {
	ticker := time.NewTicker(intervalo)
	for range ticker.C {
		pool.mu.RLock()
		backends := pool.backends
		pool.mu.RUnlock()
		for _, b := range backends {
			go b.CheckHealth()
		}
	}
}

// statsHandler retorna um JSON com as estatísticas de cada backend
func statsHandler(pool *ServerPool, w http.ResponseWriter, r *http.Request) {
	pool.mu.RLock()
	backends := pool.backends
	pool.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, "{\n  \"backends\": [\n")
	for i, b := range backends {
		b.Mutex.RLock()
		alive := b.Alive
		b.Mutex.RUnlock()
		conn := atomic.LoadInt32(&b.Connections)
		reqs := atomic.LoadInt64(&b.Requests)
		fmt.Fprintf(w, "    {\"url\":\"%s\", \"alive\":%v, \"connections\":%d, \"requests\":%d}",
			b.URL.String(), alive, conn, reqs)
		if i < len(backends)-1 {
			fmt.Fprintf(w, ",")
		}
		fmt.Fprintf(w, "\n")
	}
	fmt.Fprintf(w, "  ]\n}\n")
}

func main() {
	// Lista dos backends (endereços expostos pelos containers)
	backendURLs := []string{
		"http://localhost:8081",
		"http://localhost:8082",
		"http://localhost:8083",
	}

	pool := &ServerPool{}

	for _, urlStr := range backendURLs {
		u, err := url.Parse(urlStr)
		if err != nil {
			log.Fatalf("Erro ao parsear URL %s: %v", urlStr, err)
		}
		proxy := httputil.NewSingleHostReverseProxy(u)
		// Configura timeout para evitar travar se o backend demorar
		proxy.Transport = &http.Transport{
			ResponseHeaderTimeout: 5 * time.Second,
		}
		b := &Backend{
			URL:          u,
			Alive:        true,
			ReverseProxy: proxy,
			Connections:  0,
			Requests:     0,
		}
		pool.mu.Lock()
		pool.backends = append(pool.backends, b)
		pool.mu.Unlock()
	}

	// Inicia o health check a cada 10 segundos
	go HealthCheck(pool, 10*time.Second)

	// Handlers
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		lbHandler(pool, w, r)
	})
	http.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		statsHandler(pool, w, r)
	})

	porta := "8080"
	log.Printf("Load Balancer Power of Two Choices rodando na porta %s", porta)
	log.Printf("Backends configurados: %v", backendURLs)
	log.Fatal(http.ListenAndServe(":"+porta, nil))
}
