package wsevent

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dgrijalva/jwt-go"
	ws "github.com/gorilla/websocket"
)

//Client represents a server-side client
type Client struct {
	ID      string        //Session ID
	Request *http.Request //http Request when connection was upgraded
	Token   *jwt.Token    //if any

	writeMu *sync.Mutex
	conn    *ws.Conn
	server  *Server
	closed  *int32
}

type request struct {
	ID   string          `json:"id"`
	Data json.RawMessage `json:"data"`

	next *request
}

type reply struct {
	ID   string      `json:"id"`
	Data interface{} `json:"data"`

	next *reply
}

func genID(r *http.Request) string {
	buff := make([]byte, 5)
	rand.Read(buff)
	b := bytes.NewBuffer(buff)
	b.WriteString(r.RemoteAddr)

	return base64.URLEncoding.EncodeToString(b.Bytes())
}

func (s *Server) NewClientWithID(upgrader ws.Upgrader, w http.ResponseWriter, r *http.Request, id string) (*Client, error) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return nil, err
	}

	client := &Client{
		ID:      id,
		Request: r,

		writeMu: new(sync.Mutex),
		conn:    conn,
		server:  s,
		closed:  new(int32),
	}

	go client.listener(s)

	return client, nil
}

func (s *Server) NewClient(upgrader ws.Upgrader, w http.ResponseWriter, r *http.Request) (*Client, error) {
	return s.NewClientWithID(upgrader, w, r, genID(r))
}

//A thread-safe variant of WriteMessage
func (c *Client) Emit(data string) {
	c.writeMu.Lock()
	c.conn.WriteMessage(ws.TextMessage, []byte(data))
	c.writeMu.Unlock()
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

	c.Emit(string(bytes))
	return nil
}

func (c *Client) Close() {
	atomic.AddInt64(c.server.clients, -1)
	c.conn.Close()
}

func (c *Client) cleanup(s *Server) {
	if atomic.LoadInt32(c.closed) == 1 {
		return
	}

	atomic.StoreInt32(c.closed, 1)
	c.Close()

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
		s.OnDisconnect(c.ID, c.Token)
	}
}

func (c *Client) listener(s *Server) {
	atomic.AddInt64(s.clients, 1)

	tick := time.NewTicker(time.Millisecond * 10)
	defer func() {
		if err := recover(); err != nil {
			buf := make([]byte, 64<<10)
			buf = buf[:runtime.Stack(buf, false)]
			println("wsevent: panic serving " + c.Request.RemoteAddr + " ")
			println(fmt.Sprintf("http: panic serving %s: %v\n%s", c.Request.RemoteAddr, err, buf))
			c.cleanup(s)
		}
	}()

	for {
		<-tick.C
		_, data, err := c.conn.ReadMessage()
		if atomic.LoadInt32(s.closed) == 1 {
			return
		}

		if err != nil {
			c.cleanup(s)
			tick.Stop()
			return
		}

		req := s.getRequest()

		if err := json.Unmarshal(data, &req); err != nil {
			continue
		}

		callName := s.codec.ReadName(req.Data)

		s.handlersLock.RLock()
		f, ok := s.handlers[callName]
		s.handlersLock.RUnlock()

		var defaultHandler bool

		if !ok {
			if s.defaultHandler == nil {
				continue
			}
			f = s.defaultHandler
			defaultHandler = true
		}

		s.Requests.Add(1)
		reply := s.getReply()
		reply.ID = req.ID
		if defaultHandler {
			reply.Data, err = s.call(c, f, []byte("{}"))
		} else {
			reply.Data, err = s.call(c, f, req.Data)
		}
		if err != nil {
			reply.Data = s.codec.Error(err)
		}
		s.Requests.Done()

		go func() {
			bytes, _ := json.Marshal(reply)

			c.Emit(string(bytes))
			s.freeRequest(req)
			s.freeReply(reply)
		}()

	}
}
