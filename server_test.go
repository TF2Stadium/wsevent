package wsevent

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}

func newTestServer(f func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(f))
}

func connect(URL string) (*websocket.Conn, error) {
	u, _ := url.Parse(URL)
	u.Scheme = "ws"

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	return conn, err
}

func TestNewServer(t *testing.T) {
	s := NewServer()
	defer s.Close()
	if s == nil {
		t.Fatal("NewServer retuning nil")
	}
}

func TestClient(t *testing.T) {
	server := NewServer()
	defer server.Close()
	room := "0"

	var client *Client
	wg := new(sync.WaitGroup)

	wg.Add(1)
	ts := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		var err error

		client, err = server.NewClient(upgrader, w, r)
		if err != nil {
			t.Fatal(err)
		}
		if client == nil {
			t.Fatal("client is nil")
		}

		server.AddClient(client, room)
		wg.Done()
	})

	defer ts.Close()

	conn, err := connect(ts.URL)
	wg.Wait()
	if err != nil {
		t.Fatal(err)
		return
	}

	defer conn.Close()

	if client.Request == nil {
		t.Fatal("Request() shouldn't be nil")
		return
	}

	rooms := server.RoomsJoined(client.ID)
	if rooms[0] != "0" {
		t.Fatalf("Client not added to room %s. Current rooms: %v", room, rooms)
		return
	}

	server.Broadcast(room, "test")

	now := time.Now().UnixNano()
	_, data, err := conn.ReadMessage()
	conn.SetReadDeadline(time.Now().Add(time.Nanosecond * time.Duration(time.Now().UnixNano()-now)))
	if err != nil {
		t.Fatal(err)
		return
	}

	if string(data) != "test" {
		t.Fatalf("Received the wrong data: %s", string(data))
	}

	server.RemoveClient(client, room)
	server.Broadcast(room, "test")

	_, data, err = conn.ReadMessage()
	if len(data) != 0 {
		t.Fatalf("Shouldn't have received any data.")
	}

	netError := err.(net.Error)
	if !netError.Timeout() {
		t.Fatalf("Read should've timed out: %v", err)
	}
}

type TestObject struct{}

func (TestObject) Add(so *Client, data []byte) interface{} {
	var args struct {
		A, B int
	}

	json.Unmarshal(data, &args)

	return struct {
		Result int `json:"result"`
	}{args.A + args.B}
}

func (TestObject) Name(_ string) string {
	return "add"
}

func TestHandler(t *testing.T) {
	server := NewServer()
	server.Extractor = func(data []byte) string {
		req := make(map[string]interface{})
		json.Unmarshal(data, &req)
		return req["request"].(string)
	}

	defer server.Close()

	var client *Client
	wg := new(sync.WaitGroup)

	wg.Add(1)
	ts := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		var err error

		client, err = server.NewClient(upgrader, w, r)
		if err != nil {
			t.Fatal(err)
		}
		if client == nil {
			t.Fatal("client is nil")
		}

		wg.Done()
	})

	defer ts.Close()

	conn, err := connect(ts.URL)
	defer conn.Close()
	wg.Wait()

	if err != nil {
		t.Fatal(err)
		return
	}

	server.Register(TestObject{})
	server.On("subtract", func(_ *Client, data []byte) interface{} {
		var args struct {
			A, B int
		}
		json.Unmarshal(data, &args)

		return struct {
			Result int `json:"result"`
		}{args.A - args.B}
	})

	args := map[string]interface{}{
		"id": "1",
		"data": map[string]interface{}{
			"request": "add",
			"A":       1,
			"B":       2,
		},
	}
	conn.WriteJSON(args)

	reply := make(map[string]interface{})
	err = conn.ReadJSON(&reply)
	if err != nil {
		t.Fatal(err)
		return
	}

	data, ok := reply["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("Received invalid reply: %v\n", reply)
		return
	}

	if data["result"].(float64) != 3 {
		t.Fatalf("Result not valid: %v\n", reply)
		return
	}

	args = map[string]interface{}{
		"id": "1",
		"data": map[string]interface{}{
			"request": "subtract",
			"A":       2,
			"B":       1,
		},
	}
	conn.WriteJSON(args)

	err = conn.ReadJSON(&reply)
	data, ok = reply["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("Received invalid reply: %v\n", reply)
		return
	}

	if data["result"].(float64) != 1 {
		t.Fatalf("Result not valid: %v\n", reply)
		return
	}
}
