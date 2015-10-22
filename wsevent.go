//Copyright 2015 Vibhav Pant. All rights reserved.
//Use of this source code is governed by the MIT
//that can be found in the LICENSE file.

//Package wsevent implements thread-safe event-driven communication similar to socket.IO,
//on the top of Gorilla's WebSocket implementation.
package wsevent

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	ws "github.com/gorilla/websocket"
)

//Client
type Client struct {
	//Session ID
	id string

	conn     *ws.Conn
	connLock *sync.RWMutex
}

//Server
type Server struct {
	rooms     map[string]([]*Client)
	roomsLock *sync.RWMutex

	//maps client IDs tothe list of rooms the corresponding client has joined
	joinedRooms     map[string][]string
	joinedRoomsLock *sync.RWMutex

	//The extractor function reads the byte array and the message type
	//and returns the event represented by the message.
	Extractor func(string) string
	//Called when the websocket connection closes. The disconnected client's
	//session ID is sent as an argument
	OnDisconnect func(string)

	handlers     map[string]func(*Client, string) string
	handlersLock *sync.RWMutex

	newClient chan *Client
}

func genID(r *http.Request) string {
	hash := fmt.Sprintf("%s%d", r.RemoteAddr, time.Now().UnixNano())
	return fmt.Sprintf("%x", sha1.Sum([]byte(hash)))
}

//Returns the client's unique session ID
func (c *Client) Id() string {
	return c.id
}

func (s *Server) NewClient(upgrader ws.Upgrader, w http.ResponseWriter, r *http.Request) (*Client, error) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return nil, err
	}

	client := &Client{
		id:       genID(r),
		conn:     conn,
		connLock: new(sync.RWMutex),
	}
	s.newClient <- client

	return client, nil
}

//A thread-safe variant of WriteMessage
func (c *Client) Emit(data []byte, messageType int) error {
	c.connLock.Lock()
	defer c.connLock.Unlock()

	return c.conn.WriteMessage(messageType, data)
}

//Return a new server object
func NewServer() *Server {
	s := &Server{
		rooms:     make(map[string]([]*Client)),
		roomsLock: new(sync.RWMutex),

		joinedRooms:     make(map[string][]string),
		joinedRoomsLock: new(sync.RWMutex),

		handlers:     make(map[string](func(*Client, string) string)),
		handlersLock: new(sync.RWMutex),

		newClient: make(chan *Client),
	}

	return s
}

//Add a client c to room r
func (s *Server) AddClient(c *Client, r string) {
	s.roomsLock.Lock()
	defer s.roomsLock.Unlock()
	s.rooms[r] = append(s.rooms[r], c)

	s.joinedRoomsLock.Lock()
	defer s.joinedRoomsLock.Unlock()
	s.joinedRooms[c.id] = append(s.joinedRooms[c.id], r)
}

//Send all clients in room room data with type messageType
func (s *Server) Broadcast(room string, data []byte, messageType int) {
	wg := new(sync.WaitGroup)

	for _, client := range s.rooms[room] {
		go func(c *Client) {
			wg.Add(1)
			defer wg.Done()
			c.Emit(data, messageType)
		}(client)
	}

	wg.Wait()
}

func (c *Client) cleanup(s *Server) {
	c.conn.Close()
	s.joinedRoomsLock.RLock()
	defer s.joinedRoomsLock.RUnlock()

	for _, room := range s.joinedRooms[c.id] {
		s.roomsLock.Lock()
		delete(s.rooms, room)
		s.roomsLock.Unlock()
	}

	if s.OnDisconnect != nil {
		s.OnDisconnect(c.id)
	}
}

//Returns an array of rooms the client c has been added to
func (s *Server) RoomsJoined(c *Client) []string {
	var rooms []string
	s.joinedRoomsLock.RLock()
	defer s.joinedRoomsLock.RUnlock()

	for _, room := range s.joinedRooms[c.id] {
		rooms = append(rooms, room)
	}

	return rooms
}

//Starts listening for events on added sockets. Needs to be called only once.
func (s *Server) Listener() {
	for {
		c := <-s.newClient
		go func(c *Client) {
			for {
				mtype, data, err := c.conn.ReadMessage()
				if err != nil {
					c.cleanup(s)
					return
				}

				var js struct {
					Id   string
					Data string
				}

				err = json.Unmarshal(data, &js)

				if err != nil || mtype != ws.TextMessage {
					continue
				}

				callName := s.Extractor(js.Data)

				s.handlersLock.RLock()
				f, ok := s.handlers[callName]
				s.handlersLock.RUnlock()

				if !ok {
					continue
				}

				rtrn := f(c, js.Data)
				reply := struct {
					Id   string `json:"id"`
					Data string `json:"data,string"`
				}{js.Id, rtrn}

				bytes, _ := json.Marshal(reply)
				c.Emit(bytes, ws.TextMessage)
			}
		}(c)
	}
}

//Registers a callback for the event string. The callback must take three arguments,
//The cline object from which the message wwas received, byte array it's type,
//and return a byte array and it's type.
func (s *Server) On(event string, f func(*Client, string) string) {
	s.handlersLock.Lock()
	s.handlers[event] = f
	s.handlersLock.Unlock()
}
