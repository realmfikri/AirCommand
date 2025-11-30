package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"aircommand/internal/control"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	generator := control.NewGenerator(5) // default 5 planes/minute
	flights := make(chan control.Flight, 16)
	go generator.Run(ctx, flights)
	go logFlights(ctx, flights)

	server := control.NewServer(generator)

	mux := http.NewServeMux()
	mux.HandleFunc("/control", server.HandleControl)
	mux.HandleFunc("/rate", server.HandleRate)
	mux.HandleFunc("/", serveIndex)

	srv := &http.Server{Addr: ":8080", Handler: mux}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("server shutdown: %v", err)
		}
	}()

	log.Println("AirCommand control server listening on :8080")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

func logFlights(ctx context.Context, flights <-chan control.Flight) {
	for {
		select {
		case <-ctx.Done():
			return
		case f, ok := <-flights:
			if !ok {
				return
			}
			log.Printf("spawned flight %d (%s)", f.ID, f.Call)
		}
	}
}

func serveIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := os.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "missing ui", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.Write(data)
}

// Helper for debugging JSON payloads in logs.
func logJSON(label string, v any) {
	b, _ := json.Marshal(v)
	log.Printf("%s: %s", label, b)
}
