//Copyright 2015 TF2Stadium. All rights reserved.
//Use of this source code is governed by the MIT
//that can be found in the LICENSE file.

//Package wsevent implements thread-safe event-driven communication similar to socket.IO,
//on the top of Gorilla's WebSocket implementation.
package wsevent

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"sync"

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
	Extractor func([]byte) string
	//Called when the websocket connection closes. The disconnected client's
	//session ID is sent as an argument
	OnDisconnect func(string)
	//Called when no event handler for a specific event exists
	DefaultHandler func(*Server, *Client, []byte) []byte

	handlers     map[string]func(*Server, *Client, []byte) []byte
	handlersLock *sync.RWMutex

	newClient chan *Client
}

func genID() string {
	bytes := make([]byte, 32)
	rand.Read(bytes)

	return base64.URLEncoding.EncodeToString(bytes)
}

//Returns the client's unique session ID
func (c *Client) Id() string {
	return c.id
}

// Returns the first http request when established connection.
func (c *Client) Request() *http.Request {
	return c.request
}

func (s *Server) NewClientWithID(upgrader ws.Upgrader, w http.ResponseWriter, r *http.Request, id string) (*Client, error) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return nil, err
	}

	client := &Client{
		id:       id,
		conn:     conn,
		connLock: new(sync.RWMutex),
		request:  r,
	}
	s.newClient <- client

	return client, nil
}

func (s *Server) NewClient(upgrader ws.Upgrader, w http.ResponseWriter, r *http.Request) (*Client, error) {
	return s.NewClientWithID(upgrader, w, r, genID())
}

func (c *Client) Close() error {
	c.connLock.Lock()
	defer c.connLock.Unlock()
	return c.conn.Close()
}

//A thread-safe variant of WriteMessage
func (c *Client) Emit(data string) error {
	c.connLock.Lock()
	defer c.connLock.Unlock()
	return c.conn.WriteMessage(ws.TextMessage, []byte(data))
}

type emitJS struct {
	Id   int         `json:"id"`
	Data interface{} `json:"data"`
}

var emitPool = &sync.Pool{New: func() interface{} { return emitJS{} }}

//A thread-safe variant of EmitJSON
func (c *Client) EmitJSON(v interface{}) error {
	c.connLock.Lock()
	defer c.connLock.Unlock()

	js := emitPool.Get().(emitJS)
	defer emitPool.Put(js)

	js.Id = -1
	js.Data = v

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

		handlers:     make(map[string](func(*Server, *Client, []byte) []byte)),
		handlersLock: new(sync.RWMutex),

		newClient: make(chan *Client),
	}

	go s.listener()
	return s
}

//Add a client c to room r
func (s *Server) AddClient(c *Client, r string) {
	s.joinedRoomsLock.RLock()
	for _, room := range s.joinedRooms[c.id] {
		if r == room {
			//log.Printf("%s already in room %s", c.id, r)
			s.joinedRoomsLock.RUnlock()
			return
		}
	}
	s.joinedRoomsLock.RUnlock()

	s.roomsLock.Lock()
	s.rooms[r] = append(s.rooms[r], c)
	s.roomsLock.Unlock()

	s.joinedRoomsLock.Lock()
	defer s.joinedRoomsLock.Unlock()
	s.joinedRooms[c.id] = append(s.joinedRooms[c.id], r)
	//log.Printf("Added %s to room %s", c.id, r)
}

//Remove client c from room r
func (s *Server) RemoveClient(id, r string) {
	index := -1
	s.roomsLock.RLock()
	for i, client := range s.rooms[r] {
		if id == client.id {
			index = i
			break
		}
	}
	s.roomsLock.RUnlock()
	if index == -1 {
		//log.Printf("Client %s not found in room %s", id, r)
		return
	}

	s.roomsLock.Lock()
	s.rooms[r] = append(s.rooms[r][:index], s.rooms[r][index+1:]...)
	s.roomsLock.Unlock()

	index = -1
	s.joinedRoomsLock.RLock()
	for i, room := range s.joinedRooms[id] {
		if room == r {
			index = i
		}
	}
	s.joinedRoomsLock.RUnlock()
	if index == -1 {
		return
	}

	s.joinedRoomsLock.Lock()
	defer s.joinedRoomsLock.Unlock()

	s.joinedRooms[id] = append(s.joinedRooms[id][:index], s.joinedRooms[id][index+1:]...)
}

//Send all clients in room room data
func (s *Server) Broadcast(room string, data string) {
	s.roomsLock.RLock()
	for _, client := range s.rooms[room] {
		//log.Printf("sending to %s in room %s\n", client.id, room)
		go func(c *Client) {
			c.Emit(data)
		}(client)
	}
	s.roomsLock.RUnlock()
}

func (s *Server) BroadcastJSON(room string, v interface{}) {
	s.roomsLock.RLock()
	for _, client := range s.rooms[room] {
		//log.Printf("sending to %s %s\n", client.id, room)
		go func(c *Client) {
			c.EmitJSON(v)
		}(client)
	}
	s.roomsLock.RUnlock()
}

func (c *Client) cleanup(s *Server) {
	c.conn.Close()

	s.joinedRoomsLock.RLock()
	for _, room := range s.joinedRooms[c.id] {
		//log.Println(room)
		index := -1

		s.roomsLock.Lock()
		for i, client := range s.rooms[room] {
			if client.id == c.id {
				index = i
			}
		}

		s.rooms[room] = append(s.rooms[room][:index], s.rooms[room][index+1:]...)
		s.roomsLock.Unlock()
	}
	s.joinedRoomsLock.RUnlock()

	s.joinedRoomsLock.Lock()
	delete(s.joinedRooms, c.id)
	s.joinedRoomsLock.Unlock()

	if s.OnDisconnect != nil {
		s.OnDisconnect(c.id)
	}
}

//Returns an array of rooms the client c has been added to
func (s *Server) RoomsJoined(id string) []string {
	rooms := make([]string, len(s.joinedRooms[id]))
	s.joinedRoomsLock.RLock()
	defer s.joinedRoomsLock.RUnlock()

	copy(rooms, s.joinedRooms[id])

	return rooms
}

type request struct {
	Id   string
	Data json.RawMessage
}

type reply struct {
	Id   string `json:"id"`
	Data string `json:"data,string"`
}

var (
	reqPool   = &sync.Pool{New: func() interface{} { return request{} }}
	replyPool = &sync.Pool{New: func() interface{} { return reply{} }}
)

func (c *Client) listener(s *Server) {
	for {
		mtype, data, err := c.conn.ReadMessage()
		if err != nil {
			c.cleanup(s)
			return
		}

		js := reqPool.Get().(request)
		err = json.Unmarshal(data, &js)

		if err != nil || mtype != ws.TextMessage {
			log.Println(err)
			continue
		}

		callName := s.Extractor(js.Data)

		s.handlersLock.RLock()
		f, ok := s.handlers[callName]
		s.handlersLock.RUnlock()

		if !ok {
			if s.DefaultHandler != nil {
				f = s.DefaultHandler
				goto call
			}
			continue
		}
	call:
		go func() {
			rtrn := f(s, c, js.Data)

			replyJs := replyPool.Get().(reply)
			replyJs.Id = js.Id
			replyJs.Data = string(rtrn)

			bytes, _ := json.Marshal(replyJs)
			c.Emit(string(bytes))

			reqPool.Put(js)
			replyPool.Put(replyJs)
		}()
	}
}

func (s *Server) listener() {
	for {
		c := <-s.newClient
		go c.listener(s)
	}
}

//Registers a callback for the event string. The callback must take 2 arguments,
//The client from which the message was received and the string message itself.
func (s *Server) On(event string, f func(*Server, *Client, []byte) []byte) {
	s.handlersLock.Lock()
	s.handlers[event] = f
	s.handlersLock.Unlock()
}
