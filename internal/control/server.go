package control

import (
	"log"
	"net/http"
	"strconv"

	"github.com/gorilla/websocket"
)

// Message is the control payload exchanged over the websocket.
type Message struct {
	Type string `json:"type"`
	Rate int64  `json:"rate,omitempty"`
}

// Server hosts control endpoints for updating the generator.
type Server struct {
	Generator *Generator
	upgrader  websocket.Upgrader
}

// NewServer constructs a Server bound to the supplied generator.
func NewServer(gen *Generator) *Server {
	return &Server{
		Generator: gen,
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

	// Send initial rate to client.
	initial := Message{Type: "rate", Rate: s.Generator.Rate()}
	if err := conn.WriteJSON(initial); err != nil {
		log.Printf("send initial rate: %v", err)
		return
	}

	for {
		var msg Message
		if err := conn.ReadJSON(&msg); err != nil {
			log.Printf("control read error: %v", err)
			return
		}
		if msg.Type == "rate" {
			s.Generator.SetRate(msg.Rate)
			if err := conn.WriteJSON(Message{Type: "rate", Rate: s.Generator.Rate()}); err != nil {
				log.Printf("control ack error: %v", err)
				return
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
