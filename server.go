//Copyright 2015 TF2Stadium. All rights reserved.
//Use of this source code is governed by the MIT
//that can be found in the LICENSE file.

//Package wsevent implements thread-safe event-driven communication similar to socket.IO,
//on the top of Gorilla's WebSocket implementation.
package wsevent

import (
	"log"
	"reflect"
	"sync"
)

//ServerCodec implements a codec for reading method/event names and their parameters.
type ServerCodec interface {
	//ReadName reads the received data and returns the method/event name
	ReadName([]byte) string
	//Unmarshal reads the recieved paramters in the provided object, and returns errors
	//if (any) while unmarshaling, which is then sent a reply
	Unmarshal([]byte, interface{}) error
	//Error wraps the error returned by Unmarshal into a json-marshalable object
	Error(error) interface{}
}

func (s *Server) call(client *Client, f reflect.Value, data []byte) (interface{}, error) {
	//praams is a pointer to the parameter struct for the handler
	params := reflect.New(f.Type().In(1))
	err := s.codec.Unmarshal(data, params.Interface())
	if err != nil {
		return nil, err
	}

	in := []reflect.Value{
		reflect.ValueOf(client),
		reflect.Indirect(params)}
	out := f.Call(in)[0].Interface()
	err, _ = out.(error)

	return out, err
}

//Server represents an RPC server
type Server struct {
	//maps room string to a list of clients in it
	rooms   map[string]([]*Client)
	roomsMu *sync.RWMutex

	//maps client IDs to the list of rooms the corresponding client has joined
	joinedRooms   map[string][]string
	joinedRoomsMu *sync.RWMutex

	//Called when the websocket connection closes. The disconnected client's
	//session ID is sent as an argument
	OnDisconnect func(string)

	handlers     map[string]reflect.Value
	handlersLock *sync.RWMutex

	codec          ServerCodec
	defaultHandler reflect.Value

	reqMu   *sync.Mutex
	freeReq *request

	replyMu *sync.Mutex
	freeRep *reply

	// Used to wait for all requests to complete
	Requests *sync.WaitGroup
}

func (s *Server) getRequest() *request {
	s.reqMu.Lock()
	defer s.reqMu.Unlock()
	req := s.freeReq
	if req == nil {
		req = new(request)
	} else {
		s.freeReq = req.next
		*req = request{}
	}

	return req
}

func (s *Server) freeRequest(req *request) {
	s.reqMu.Lock()
	defer s.reqMu.Unlock()

	req.next = s.freeReq
	s.freeReq = req
}

func (s *Server) getReply() *reply {
	s.replyMu.Lock()
	defer s.replyMu.Unlock()
	rep := s.freeRep
	if rep == nil {
		rep = new(reply)
	} else {
		s.freeRep = rep.next
		*rep = reply{}
	}
	return rep
}

func (s *Server) freeReply(reply *reply) {
	s.replyMu.Lock()
	defer s.replyMu.Unlock()

	reply.next = s.freeRep
	s.freeRep = reply
}

//NewServer returns a new server
func NewServer(codec ServerCodec, defaultHandler interface{}) *Server {
	value := reflect.ValueOf(defaultHandler)
	if !validHandler(value, reflect.TypeOf(defaultHandler).Name()) {
		panic("NewServer: invalid default handler")
	}

	s := &Server{
		rooms:   make(map[string]([]*Client)),
		roomsMu: new(sync.RWMutex),

		//Maps socket ID -> list of rooms the client is in
		joinedRooms:   make(map[string][]string),
		joinedRoomsMu: new(sync.RWMutex),

		handlers:     make(map[string]reflect.Value),
		handlersLock: new(sync.RWMutex),

		codec:          codec,
		defaultHandler: reflect.ValueOf(defaultHandler),

		reqMu:   new(sync.Mutex),
		replyMu: new(sync.Mutex),

		Requests: new(sync.WaitGroup),
	}

	return s
}

//Join adds a client to the given room
func (s *Server) Join(c *Client, r string) {
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

//Leave removes the client from the given room
func (s *Server) Leave(client *Client, r string) {
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

//Broadcast given data to all clients in the given room
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

//BroadcastJSON broadcasts the json encoding of v to all clients in room
func (s *Server) BroadcastJSON(room string, v interface{}) {
	s.roomsMu.RLock()
	for _, client := range s.rooms[room] {
		//log.Printf("sending to %s %s\n", client.id, room)
		go func(c *Client) {
			err := c.EmitJSON(v)
			if err != nil {
				log.Println(err)
			}
		}(client)
	}
	s.roomsMu.RUnlock()
}

//Rooms returns a map of room name -> number of clients
func (s *Server) Rooms() map[string]int {
	rooms := make(map[string]int)

	s.roomsMu.RLock()
	defer s.roomsMu.RUnlock()
	for room, clients := range s.rooms {
		rooms[room] = len(clients)
	}

	return rooms
}

//RoomsJoined returns an array of rooms the client c has been added to
func (s *Server) RoomsJoined(id string) []string {
	rooms := make([]string, len(s.joinedRooms[id]))
	s.joinedRoomsMu.RLock()
	defer s.joinedRoomsMu.RUnlock()

	copy(rooms, s.joinedRooms[id])

	return rooms
}

//On Registers a callback for the event string. It panics if the callback isn't
//valid
func (s *Server) On(event string, f interface{}) {
	value := reflect.ValueOf(f)

	if !validHandler(value, reflect.TypeOf(f).Name()) {
		panic("On: invalid callback for event " + event)
	}

	s.handlersLock.Lock()
	s.handlers[event] = value
	s.handlersLock.Unlock()
}

//A Receiver interface implements the Name method, which returns a name for the
//event, given a registered function's name.
type Receiver interface {
	Name(string) string
}

//Register is similar to net/rpc's Register, expect that rcvr needs to implement the
//Receiver interface
func (s *Server) Register(rcvr Receiver) {
	rvalue := reflect.ValueOf(rcvr)
	rtype := reflect.TypeOf(rcvr)

	for i := 0; i < rvalue.NumMethod(); i++ {
		method := rvalue.Method(i)
		name := rtype.Method(i).Name
		if name == "Name" {
			continue
		}

		if !validHandler(method, name) {
			continue
		}

		s.handlersLock.Lock()
		s.handlers[rcvr.Name(name)] = method
		s.handlersLock.Unlock()
	}
}

func validHandler(method reflect.Value, name string) bool {
	return method.Type().NumIn() == 2 &&
		method.Type().NumOut() == 1 &&
		method.Type().In(0) == reflect.TypeOf(&Client{}) &&
		method.Type().In(1).Kind() == reflect.Struct
}
