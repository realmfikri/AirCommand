package main

import (
	"context"
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

	runwayDefs := []control.RunwayDefinition{{Name: "2L", Heading: 20}, {Name: "2R", Heading: 20}}
	metrics := control.NewSchedulerMetrics([]string{"2L", "2R"})
	runways := control.NewRunwayManager(runwayDefs, metrics)
	runways.SetWind(8, 20)
	go runways.Run(ctx, flights)

	server := control.NewServer(generator, runways, metrics)

	mux := http.NewServeMux()
	mux.HandleFunc("/control", server.HandleControl)
	mux.HandleFunc("/rate", server.HandleRate)
	mux.HandleFunc("/metrics", server.HandleMetrics)
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
