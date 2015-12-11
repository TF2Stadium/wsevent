package wsevent

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
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

func (c *Client) listener(s *Server) {
	throttle := time.NewTicker(time.Millisecond * 10)
	defer throttle.Stop()
	for {
		<-throttle.C
		mtype, data, err := c.conn.ReadMessage()
		if err != nil {
			c.cleanup(s)
			return
		}
		if mtype != ws.TextMessage {
			c.conn.Close()
			return
		}

		js := reqPool.Get().(request)

		if err := json.Unmarshal(data, &js); err != nil {
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
