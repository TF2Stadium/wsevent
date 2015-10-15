//wsevent implements thread-safe event-driven communication similar to socket.IO,
//on the top of Gorilla's WebSocket implementation.
package wsevent

import (
	ws "github.com/gorilla/websocket"
	"sync"
)

//Client
type Client struct {
	conn      *ws.Conn
	readLock  *sync.Mutex
	writeLock *sync.Mutex
}

//Server
type Server struct {
	rooms map[string]([]*Client)

	//The extractor function reads the byte array and the message type
	//and returns the event represented by the message.
	Extractor func([]byte, int) string

	calls   map[string]func([]byte, int) ([]byte, int)
	mapLock *sync.Mutex
}

//Creates a new client from a websocket connection
func NewClient(c *ws.Conn) *Client {
	return &Client{c, new(sync.Mutex), new(sync.Mutex)}
}

//A thread-safe variant of WriteMessage
func (c *Client) Emit(data []byte, messageType int) error {
	c.writeLock.Lock()
	defer c.writeLock.Unlock()

	return c.conn.WriteMessage(messageType, data)
}

//Return a new server object
func NewServer() *Server {
	return &Server{
		rooms:   make(map[string]([]*Client)),
		calls:   make(map[string](func([]byte, int) ([]byte, int))),
		mapLock: new(sync.Mutex),
	}
}

//Add a client c to room r
func (s *Server) AddClient(c *Client, r string) {
	s.rooms[r] = append(s.rooms[r], c)
}

//Sends all clients in room data with type messageType
func (s *Server) Broadcast(room string, data []byte, messageType int) {
	clients := len(s.rooms[room])
	clientSent := make(chan bool, clients)

	for _, client := range s.rooms[room] {
		go func(c *Client) {
			c.Emit(data, messageType)
			clientSent <- true
		}(client)
	}

	for done := 0; done != clients; done++ {
		<-clientSent
	}
}

//Starts listening for events on the client sockets, and calls the registered
//event handlers. Needs to be executed once only.
func (s *Server) StartListener() {
	for _, room := range s.rooms {
		for _, client := range room {
			go func(c *Client) {
				for {
					messageType, data, err := c.conn.ReadMessage()
					if err != nil {
						continue
					}

					callName := s.Extractor(data, messageType)

					s.mapLock.Lock()
					f := s.calls[callName]
					s.mapLock.Unlock()

					c.Emit(f(data, messageType))
				}
			}(client)
		}
	}
}

//Registers a callback for the event string. The callback must take two arguments,
//a byte array it's type, and return a byte array and it's type.
func (s *Server) On(event string, f func([]byte, int) ([]byte, int)) {
	s.mapLock.Lock()
	s.calls[event] = f
	s.mapLock.Unlock()
}
