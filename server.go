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

type Handler func(*Server, *Client, []byte) interface{}

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
	DefaultHandler Handler

	handlers     map[string]Handler
	handlersLock *sync.RWMutex

	newClient chan *Client
}

//Return a new server object
func NewServer() *Server {
	s := &Server{
		rooms:     make(map[string]([]*Client)),
		roomsLock: new(sync.RWMutex),

		//Maps socket ID -> list of rooms the client is in
		joinedRooms:     make(map[string][]string),
		joinedRoomsLock: new(sync.RWMutex),

		handlers:     make(map[string]Handler),
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

//Returns an array of rooms the client c has been added to
func (s *Server) RoomsJoined(id string) []string {
	rooms := make([]string, len(s.joinedRooms[id]))
	s.joinedRoomsLock.RLock()
	defer s.joinedRoomsLock.RUnlock()

	copy(rooms, s.joinedRooms[id])

	return rooms
}
func (s *Server) listener() {
	for {
		c := <-s.newClient
		go c.listener(s)
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

		s.On(rcvr.Name(method.Name), func(_ *Server, c *Client, b []byte) interface{} {
			in := []reflect.Value{
				reflect.ValueOf(rcvr),
				reflect.ValueOf(s),
				reflect.ValueOf(c),
				reflect.ValueOf(b)}

			rtrn := method.Func.Call(in)
			return rtrn[0].Interface()
		})

	}
}
