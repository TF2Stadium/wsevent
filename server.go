//Copyright 2015 TF2Stadium. All rights reserved.
//Use of this source code is governed by the MIT
//that can be found in the LICENSE file.

//Package wsevent implements thread-safe event-driven communication similar to socket.IO,
//on the top of Gorilla's WebSocket implementation.
package wsevent

import (
	"reflect"
	"sync"
)

type Handler func(*Client, []byte) interface{}

//Server
type Server struct {
	//maps room string to a list of clients in it
	rooms   map[string]([]*Client)
	roomsMu *sync.RWMutex

	//maps client IDs to the list of rooms the corresponding client has joined
	joinedRooms   map[string][]string
	joinedRoomsMu *sync.RWMutex

	//The extractor function reads the byte array and the message type
	//and returns the event represented by the message.
	Extractor func([]byte) string
	//Called when the websocket connection closes. The disconnected client's
	//session ID is sent as an argument
	OnDisconnect func(string)
	//Called when no event handler for a specific event exists
	DefaultHandler Handler

	handlers     map[string]Handler
	handlersLock *sync.RWMutex

	newClient chan *Client
	stop      chan struct{}
}

//Return a new server object
func NewServer() *Server {
	s := &Server{
		rooms:   make(map[string]([]*Client)),
		roomsMu: new(sync.RWMutex),

		//Maps socket ID -> list of rooms the client is in
		joinedRooms:   make(map[string][]string),
		joinedRoomsMu: new(sync.RWMutex),

		handlers:     make(map[string]Handler),
		handlersLock: new(sync.RWMutex),

		newClient: make(chan *Client),
		stop:      make(chan struct{}),
	}

	go s.listener()
	return s
}

func (s *Server) Close() {
	s.stop <- struct{}{}
}

//Add a client c to room r
func (s *Server) AddClient(c *Client, r string) {
	s.joinedRoomsMu.RLock()
	for _, room := range s.joinedRooms[c.ID] {
		if r == room {
			//log.Printf("%s already in room %s", c.id, r)
			s.joinedRoomsMu.RUnlock()
			return
		}
	}
	s.joinedRoomsMu.RUnlock()

	s.roomsMu.Lock()
	s.rooms[r] = append(s.rooms[r], c)
	s.roomsMu.Unlock()

	s.joinedRoomsMu.Lock()
	defer s.joinedRoomsMu.Unlock()
	s.joinedRooms[c.ID] = append(s.joinedRooms[c.ID], r)
	//log.Printf("Added %s to room %s", c.id, r)
}

//Remove client c from room r
func (s *Server) RemoveClient(client *Client, r string) {
	s.roomsMu.Lock()
	for i, joinedClient := range s.rooms[r] {
		if client.ID == joinedClient.ID {
			clients := s.rooms[r]
			clients[i] = clients[len(clients)-1]
			clients[len(clients)-1] = nil
			s.rooms[r] = clients[:len(clients)-1]
			if len(s.rooms[r]) == 0 {
				delete(s.rooms, r)
			}
			break
		}
	}
	s.roomsMu.Unlock()

	s.joinedRoomsMu.Lock()
	for i, room := range s.joinedRooms[client.ID] {
		if room == r {
			s.joinedRooms[client.ID] = append(s.joinedRooms[client.ID][:i], s.joinedRooms[client.ID][i+1:]...)
			if len(s.joinedRooms[client.ID]) == 0 {
				delete(s.joinedRooms, client.ID)
			}
		}
	}
	s.joinedRoomsMu.Unlock()

}

//Send all clients in room room data
func (s *Server) Broadcast(room string, data string) {
	s.roomsMu.RLock()
	for _, client := range s.rooms[room] {
		//log.Printf("sending to %s in room %s\n", client.id, room)
		go func(c *Client) {
			c.Emit(data)
		}(client)
	}
	s.roomsMu.RUnlock()
}

func (s *Server) BroadcastJSON(room string, v interface{}) {
	s.roomsMu.RLock()
	for _, client := range s.rooms[room] {
		//log.Printf("sending to %s %s\n", client.id, room)
		go func(c *Client) {
			c.EmitJSON(v)
		}(client)
	}
	s.roomsMu.RUnlock()
}

//Returns a map of room name -> number of clients
func (s *Server) Rooms() map[string]int {
	rooms := make(map[string]int)

	s.roomsMu.RLock()
	defer s.roomsMu.RUnlock()
	for room, clients := range s.rooms {
		rooms[room] = len(clients)
	}

	return rooms
}

//Returns an array of rooms the client c has been added to
func (s *Server) RoomsJoined(id string) []string {
	rooms := make([]string, len(s.joinedRooms[id]))
	s.joinedRoomsMu.RLock()
	defer s.joinedRoomsMu.RUnlock()

	copy(rooms, s.joinedRooms[id])

	return rooms
}
func (s *Server) listener() {
	for {
		select {
		case c := <-s.newClient:
			go c.listener(s)
		case <-s.stop:
			return
		}
	}
}

//Registers a callback for the event string. The callback must take 2 arguments,
//The client from which the message was received and the string message itself.
func (s *Server) On(event string, f Handler) {
	s.handlersLock.Lock()
	s.handlers[event] = f
	s.handlersLock.Unlock()
}

//A Receiver interface implements the Name method, which returns a name for the
//event, given a Handler's name
type Receiver interface {
	Name(string) string
}

//Similar to net/rpc's Register, expect that rcvr needs to implement the
//Receiver interface
func (s *Server) Register(rcvr Receiver) {
	rtype := reflect.TypeOf(rcvr)

	for i := 0; i < rtype.NumMethod(); i++ {
		method := rtype.Method(i)
		if method.Name == "Name" {
			continue
		}

		s.On(rcvr.Name(method.Name), func(c *Client, b []byte) interface{} {
			in := []reflect.Value{
				reflect.ValueOf(rcvr),
				reflect.ValueOf(c),
				reflect.ValueOf(b)}

			rtrn := method.Func.Call(in)
			return rtrn[0].Interface()
		})

	}
}
