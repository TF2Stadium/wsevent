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
	ID      string
	Request *http.Request

	conn *ws.Conn
	send chan []byte
	recv chan []byte

	close chan struct{}
}

type request struct {
	Id   string
	Data json.RawMessage
}

type reply struct {
	Id   string      `json:"id"`
	Data interface{} `json:"data"`
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

func (s *Server) NewClientWithID(upgrader ws.Upgrader, w http.ResponseWriter, r *http.Request, id string) (*Client, error) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return nil, err
	}

	client := &Client{
		ID:      id,
		Request: r,

		conn: conn,
		send: make(chan []byte),
		recv: make(chan []byte),

		close: make(chan struct{}, 3),
	}
	s.newClient <- client

	return client, nil
}

func (s *Server) NewClient(upgrader ws.Upgrader, w http.ResponseWriter, r *http.Request) (*Client, error) {
	return s.NewClientWithID(upgrader, w, r, genID())
}

//A thread-safe variant of WriteMessage
func (c *Client) Emit(data string) {
	c.send <- []byte(data)
}

type emitJS struct {
	Id   int         `json:"id"`
	Data interface{} `json:"data"`
}

//A thread-safe variant of EmitJSON
func (c *Client) EmitJSON(v interface{}) error {
	js := emitJS{}
	js.Id = -1
	js.Data = v

	bytes, err := json.Marshal(js)
	if err != nil {
		return err
	}

	c.send <- bytes
	return nil
}

func (c *Client) Close() {
	for i := 0; i < 3; i++ {
		c.close <- struct{}{}
	}

	c.conn.Close()
}

func (c *Client) cleanup(s *Server) {
	c.conn.Close()

	s.joinedRoomsMu.RLock()
	for _, room := range s.joinedRooms[c.ID] {
		//log.Println(room)
		s.roomsMu.Lock()
		for i, client := range s.rooms[room] {
			if client.ID == c.ID {
				clients := s.rooms[room]
				clients[i] = clients[len(clients)-1]
				clients[len(clients)-1] = nil
				s.rooms[room] = clients[:len(clients)-1]
				if len(s.rooms[room]) == 0 {
					delete(s.rooms, room)
				}
				break
			}
		}

		s.roomsMu.Unlock()
	}
	s.joinedRoomsMu.RUnlock()

	s.joinedRoomsMu.Lock()
	delete(s.joinedRooms, c.ID)
	s.joinedRoomsMu.Unlock()

	if s.OnDisconnect != nil {
		s.OnDisconnect(c.ID)
	}
}

func (c *Client) listener(s *Server) {
	tick := time.NewTicker(time.Millisecond * 10)
	go func() {
		for {
			select {
			case data := <-c.send:
				c.conn.WriteMessage(ws.TextMessage, data)

			case <-c.close:
				return
			}
		}
	}()
	go func() {
		for {
			select {
			case <-tick.C:
				mtype, data, err := c.conn.ReadMessage()
				if err != nil {
					c.cleanup(s)
					c.recv <- []byte{}
					c.close <- struct{}{}
					c.close <- struct{}{}
					tick.Stop()
					return
				}
				if mtype != ws.TextMessage {
					continue
				}

				c.recv <- data

			case <-c.close:
				return
			}
		}
	}()

	for {
		select {
		case data := <-c.recv:
			if len(data) == 0 {
				return
			}

			req := reqPool.Get().(request)

			if err := json.Unmarshal(data, &req); err != nil {
				continue
			}

			callName := s.Extractor(req.Data)

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
				rtrn := f(c, req.Data)
				reply := replyPool.Get().(reply)
				reply.Id = req.Id
				reply.Data = rtrn

				bytes, _ := json.Marshal(reply)
				c.send <- bytes

				reqPool.Put(req)
				replyPool.Put(reply)
			}()

		case <-c.close:
			return
		}
	}
}
