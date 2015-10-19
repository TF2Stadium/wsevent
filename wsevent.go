//wsevent implements thread-safe event-driven communication similar to socket.IO,
//on the top of Gorilla's WebSocket implementation.
package wsevent

import (
	ws "github.com/gorilla/websocket"
	"net/http"
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
	rooms     map[string]([]*Client)
	roomsLock *sync.Mutex

	//The extractor function reads the byte array and the message type
	//and returns the event represented by the message.
	Extractor func([]byte, int) string

	calls   map[string]func([]byte, int) ([]byte, int)
	mapLock *sync.Mutex

	newClient chan *Client
	once      *sync.Once
}

func (s *Server) NewClient(upgrader ws.Upgrader, w http.ResponseWriter, r *http.Request) (*Client, error) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return nil, err
	}

	client := &Client{conn, new(sync.Mutex), new(sync.Mutex)}
	s.newClient <- client

	return client, nil
}

//A thread-safe variant of WriteMessage
func (c *Client) Emit(data []byte, messageType int) error {
	c.writeLock.Lock()
	defer c.writeLock.Unlock()

	return c.conn.WriteMessage(messageType, data)
}

//Return a new server object
func NewServer() *Server {
	s := &Server{
		rooms:     make(map[string]([]*Client)),
		roomsLock: new(sync.Mutex),
		calls:     make(map[string](func([]byte, int) ([]byte, int))),
		mapLock:   new(sync.Mutex),
		newClient: make(chan *Client),
		once:      new(sync.Once),
	}

	return s
}

//Add a client c to room r
func (s *Server) AddClient(c *Client, r string) {
	s.roomsLock.Lock()
	s.roomsLock.Unlock()
	s.rooms[r] = append(s.rooms[r], c)
}

//Sends all clients in room data with type messageType
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

func (s *Server) Listener() {
	c := <-s.newClient
	go func(c *Client) {
		for {
			messageType, data, err := c.conn.ReadMessage()
			if err != nil {
				return
			}

			callName := s.Extractor(data, messageType)

			s.mapLock.Lock()
			f, ok := s.calls[callName]
			s.mapLock.Unlock()

			if !ok {
				continue
			}
			c.Emit(f(data, messageType))
		}
	}(c)

}

//Registers a callback for the event string. The callback must take two arguments,
//a byte array it's type, and return a byte array and it's type.
func (s *Server) On(event string, f func([]byte, int) ([]byte, int)) {
	s.mapLock.Lock()
	s.calls[event] = f
	s.mapLock.Unlock()
}
