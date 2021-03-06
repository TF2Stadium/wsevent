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

type JSONCodec struct{}

func (JSONCodec) ReadName(data []byte) string {
	var body struct {
		Request string
	}
	json.Unmarshal(data, &body)
	return body.Request
}

func (JSONCodec) Unmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

func (JSONCodec) Error(err error) interface{} {
	return err
}

func (JSONCodec) MarshalError(err error) []byte {
	e := map[string]string{
		"error": err.Error(),
	}

	bytes, _ := json.Marshal(e)
	return bytes
}

func defaultHandler(*Client, struct{}) interface{} { return "" }

func TestNewServer(t *testing.T) {
	s := NewServer(JSONCodec{}, defaultHandler)
	if s == nil {
		t.Fatal("NewServer retuning nil")
	}
}

func TestClient(t *testing.T) {
	server := NewServer(JSONCodec{}, defaultHandler)
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

		server.Join(client, room)
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

	server.Leave(client, room)
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

func (TestObject) Add(so *Client, args struct {
	A, B int
}) interface{} {

	return struct {
		Result int `json:"result"`
	}{args.A + args.B}
}

func (TestObject) Name(n string) string {
	return "add"
}

func TestHandler(t *testing.T) {
	var called bool
	wait := new(sync.WaitGroup)
	server := NewServer(JSONCodec{}, func(_ *Client, _ struct{}) interface{} {
		called = true
		wait.Done()
		return struct{}{}
	})

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

	//test default handler
	wait.Add(1)
	args["data"].(map[string]interface{})["request"] = "foobar"
	conn.WriteJSON(args)

	wait.Wait()
	if !called {
		t.Fatalf("Default handler not called!")
		return
	}
}
