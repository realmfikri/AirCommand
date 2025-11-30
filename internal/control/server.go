package control

import (
	"log"
	"net/http"
	"strconv"

	"github.com/gorilla/websocket"
)

// Message is the control payload exchanged over the websocket.
type Message struct {
	Type   string `json:"type"`
	Rate   int64  `json:"rate,omitempty"`
	Runway string `json:"runway,omitempty"`
	Closed bool   `json:"closed,omitempty"`
}

// Server hosts control endpoints for updating the generator.
type Server struct {
	Generator *Generator
	Runways   *RunwayManager
	upgrader  websocket.Upgrader
}

// NewServer constructs a Server bound to the supplied generator.
func NewServer(gen *Generator, runways *RunwayManager) *Server {
	return &Server{
		Generator: gen,
		Runways:   runways,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

// HandleControl upgrades the HTTP connection to a websocket and listens for updates.
func (s *Server) HandleControl(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Send initial state to client.
	initialRate := Message{Type: "rate", Rate: s.Generator.Rate()}
	if err := conn.WriteJSON(initialRate); err != nil {
		log.Printf("send initial rate: %v", err)
		return
	}

	if s.Runways != nil {
		runwayState := Message{Type: "runway", Runway: "2L", Closed: s.Runways.IsClosed("2L")}
		if err := conn.WriteJSON(runwayState); err != nil {
			log.Printf("send initial runway: %v", err)
			return
		}
	}

	for {
		var msg Message
		if err := conn.ReadJSON(&msg); err != nil {
			log.Printf("control read error: %v", err)
			return
		}
		switch msg.Type {
		case "rate":
			s.Generator.SetRate(msg.Rate)
			if err := conn.WriteJSON(Message{Type: "rate", Rate: s.Generator.Rate()}); err != nil {
				log.Printf("control ack error: %v", err)
				return
			}
		case "runway":
			if s.Runways != nil && msg.Runway != "" {
				s.Runways.SetRunwayClosed(msg.Runway, msg.Closed)
				if err := conn.WriteJSON(Message{Type: "runway", Runway: msg.Runway, Closed: s.Runways.IsClosed(msg.Runway)}); err != nil {
					log.Printf("control runway ack error: %v", err)
					return
				}
			}
		}
	}
}

// HandleRate allows non-websocket rate updates via form/query.
func (s *Server) HandleRate(w http.ResponseWriter, r *http.Request) {
	rateStr := r.FormValue("rate")
	rate, err := strconv.ParseInt(rateStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid rate", http.StatusBadRequest)
		return
	}
	s.Generator.SetRate(rate)
	w.WriteHeader(http.StatusNoContent)
}
