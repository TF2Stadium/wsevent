package wsevent

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}

func newTestServer(f func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(f))
}

func connect(URL string) (*websocket.Conn, error) {
	conn, _, err := websocket.DefaultDialer.Dial("ws://"+URL[7:], nil)
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
	}

	defer conn.Close()

	rooms := server.RoomsJoined(client.Id())
	if rooms[0] != "0" {
		t.Fatalf("Client not added to room %s. Current rooms: %v", room, rooms)
	}

	server.Broadcast(room, "test")

	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}

	if string(data) != "test" {
		t.Fatalf("Received the wrong data: %s", string(data))
	}
}
