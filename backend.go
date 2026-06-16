package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

func main() {
	porta := os.Getenv("PORTA")
	if porta == "" {
		porta = "8080"
	}

	latenciaStr := os.Getenv("LATENCIA_MS")
	latenciaMs, _ := strconv.Atoi(latenciaStr)
	if latenciaMs < 0 {
		latenciaMs = 0
	}

	id := os.Getenv("ID")
	if id == "" {
		id = "unknown"
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if latenciaMs > 0 {
			time.Sleep(time.Duration(latenciaMs) * time.Millisecond)
		}
		fmt.Fprintf(w, "Resposta do backend %s (latência %dms)", id, latenciaMs)
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "OK")
	})
	
	log.Printf("Backend: %s | Porta: %s | Latência: %dms", id, porta, latenciaMs)
	log.Fatal(http.ListenAndServe(":"+porta, nil))
}
