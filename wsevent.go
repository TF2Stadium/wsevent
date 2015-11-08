//Copyright 2015 TF2Stadium. All rights reserved.
//Use of this source code is governed by the MIT
//that can be found in the LICENSE file.

//Package wsevent implements thread-safe event-driven communication similar to socket.IO,
//on the top of Gorilla's WebSocket implementation.
package wsevent

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"log"
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
	request  *http.Request
}

//Server
type Server struct {
	//maps room string to a list of clients in it
	rooms     map[string]([]*Client)
	roomsLock *sync.RWMutex

	//maps client IDs to the list of rooms the corresponding client has joined
	joinedRooms     map[string][]string
	joinedRoomsLock *sync.RWMutex

	//The extractor function reads the byte array and the message type
	//and returns the event represented by the message.
	Extractor func(string) string
	//Called when the websocket connection closes. The disconnected client's
	//session ID is sent as an argument
	OnDisconnect func(string)

	handlers     map[string]func(*Server, *Client, string) string
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

// Returns the first http request when established connection.
func (c *Client) Request() *http.Request {
	return c.request
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
		request:  r,
	}
	s.newClient <- client

	return client, nil
}

//A thread-safe variant of WriteMessage
func (c *Client) Emit(data string) error {
	c.connLock.Lock()
	defer c.connLock.Unlock()
	return c.conn.WriteMessage(ws.TextMessage, []byte(data))
}

//A thread-safe variant of EmitJSON
func (c *Client) EmitJSON(v interface{}) error {
	c.connLock.Lock()
	defer c.connLock.Unlock()

	js := struct {
		Id   int         `json:"id"`
		Data interface{} `json:"data"`
	}{-1, v}

	return c.conn.WriteJSON(js)
}

//Return a new server object
func NewServer() *Server {
	s := &Server{
		rooms:     make(map[string]([]*Client)),
		roomsLock: new(sync.RWMutex),

		//Maps socket ID -> list of rooms the client is in
		joinedRooms:     make(map[string][]string),
		joinedRoomsLock: new(sync.RWMutex),

		handlers:     make(map[string](func(*Server, *Client, string) string)),
		handlersLock: new(sync.RWMutex),

		newClient: make(chan *Client),
	}

	return s
}

//Add a client c to room r
func (s *Server) AddClient(c *Client, r string) {
	s.joinedRoomsLock.RLock()
	for _, clientID := range s.joinedRooms[c.id] {
		if clientID == c.id {
			//log.Printf("%s already in room %s", c.id, r)
			s.joinedRoomsLock.RUnlock()
			return
		}
	}
	s.joinedRoomsLock.RUnlock()

	s.roomsLock.Lock()
	defer s.roomsLock.Unlock()
	s.rooms[r] = append(s.rooms[r], c)

	s.joinedRoomsLock.Lock()
	defer s.joinedRoomsLock.Unlock()
	s.joinedRooms[c.id] = append(s.joinedRooms[c.id], r)
	//log.Printf("Added %s to room %s", c.id, r)
}

//Remove client c from room r
func (s *Server) RemoveClient(id, r string) {
	index := -1
	s.roomsLock.Lock()

	for i, client := range s.rooms[r] {
		if id == client.id {
			index = i
			break
		}
	}
	if index == -1 {
		//log.Printf("Client %s not found in room %s", id, r)
		s.roomsLock.Unlock()
		return
	}

	s.rooms[r] = append(s.rooms[r][:index], s.rooms[r][index+1:]...)
	s.roomsLock.Unlock()

	index = -1

	s.joinedRoomsLock.Lock()
	defer s.joinedRoomsLock.Unlock()

	for i, room := range s.joinedRooms[id] {
		if room == r {
			index = i
		}
	}
	if index == -1 {
		return
	}

	s.joinedRooms[id] = append(s.joinedRooms[id][:index], s.joinedRooms[id][index+1:]...)
}

//Send all clients in room room data with type messageType
func (s *Server) Broadcast(room string, data string) {
	wg := new(sync.WaitGroup)

	for _, client := range s.rooms[room] {
		//log.Printf("sending to %s in room %s\n", client.id, room)
		go func(c *Client) {
			wg.Add(1)
			defer wg.Done()
			c.Emit(data)
		}(client)
	}

	wg.Wait()
}

func (s *Server) BroadcastJSON(room string, v interface{}) {
	wg := new(sync.WaitGroup)

	for _, client := range s.rooms[room] {
		//log.Printf("sending to %s %s\n", client.id, room)
		wg.Add(1)
		go func(c *Client) {
			defer wg.Done()
			c.EmitJSON(v)
		}(client)
	}

	wg.Wait()

}

func (c *Client) cleanup(s *Server) {
	c.conn.Close()

	s.roomsLock.Lock()
	for _, room := range s.joinedRooms[c.id] {
		//log.Println(room)
		index := -1

		for i, client := range s.rooms[room] {
			if client.id == c.id {
				index = i
			}
		}
		if index == -1 {
			s.roomsLock.Unlock()
			return
		}

		s.rooms[room] = append(s.rooms[room][:index], s.rooms[room][index+1:]...)
	}
	s.roomsLock.Unlock()

	s.joinedRoomsLock.Lock()
	delete(s.joinedRooms, c.id)
	s.joinedRoomsLock.Unlock()

	if s.OnDisconnect != nil {
		s.OnDisconnect(c.id)
	}
}

//Returns an array of rooms the client c has been added to
func (s *Server) RoomsJoined(id string) []string {
	var rooms []string
	s.joinedRoomsLock.RLock()
	defer s.joinedRoomsLock.RUnlock()

	copy(rooms, s.joinedRooms[id])

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
					Data json.RawMessage
				}
				err = json.Unmarshal(data, &js)

				if err != nil || mtype != ws.TextMessage {
					log.Println(err)
					continue
				}

				callName := s.Extractor(string(js.Data))

				s.handlersLock.RLock()
				f, ok := s.handlers[callName]
				s.handlersLock.RUnlock()

				if !ok {
					continue
				}

				rtrn := f(s, c, string(js.Data))
				reply := struct {
					Id   string `json:"id"`
					Data string `json:"data,string"`
				}{js.Id, rtrn}

				bytes, _ := json.Marshal(reply)
				c.Emit(string(bytes))
			}
		}(c)
	}
}

//Registers a callback for the event string. The callback must take 2 arguments,
//The client from which the message was received and the string message itself.
func (s *Server) On(event string, f func(*Server, *Client, string) string) {
	s.handlersLock.Lock()
	s.handlers[event] = f
	s.handlersLock.Unlock()
}
