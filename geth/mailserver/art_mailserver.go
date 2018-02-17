// Copyright 2017 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package mailserver

import (
	"log"
	"net/http"
	"sync"

	"github.com/ethereum/go-ethereum/whisper/mailserver"
	whisper "github.com/ethereum/go-ethereum/whisper/whisperv5"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{}

type ArtMailServer struct {
	*mailserver.WMailServer

	mu   sync.RWMutex
	conn []*websocket.Conn
}

func New() *ArtMailServer {
	var mailServer mailserver.WMailServer
	return &ArtMailServer{WMailServer: &mailServer}
}

func (s *ArtMailServer) Archive(env *whisper.Envelope) {
	s.WMailServer.Archive(env)

	s.mu.Lock()
	defer s.mu.Unlock()

	healthy := s.conn[:0]
	for _, conn := range s.conn {
		err := conn.WriteMessage(websocket.TextMessage, []byte("1"))
		if err != nil {
			log.Println("art_mailserver: write message:", err)
			conn.Close()
			continue
		}

		healthy = append(healthy, conn)
	}
	s.conn = healthy
}

func (s *ArtMailServer) ListenAndServe(addr string) error {
	mux := http.NewServeMux()
	mux.Handle("/stream", http.HandlerFunc(s.handle))

	return http.ListenAndServe(addr, mux)
}

func (s *ArtMailServer) handle(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Print("art_mailserver: upgrade:", err)
		return
	}

	s.conn = append(s.conn, c)
}
