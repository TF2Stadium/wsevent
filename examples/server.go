package main

import (
	"encoding/json"
	"flag"
	ws "github.com/gorilla/websocket"
	"github.com/vibhavp/wsevent"
	"log"
	"net/http"
)

var upgrader = ws.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
var server = wsevent.NewServer()

func handler(w http.ResponseWriter, r *http.Request) {
	client, err := server.NewClient(upgrader, w, r)
	if err != nil {
		log.Println(err)
		return
	}

	server.AddClient(client, "0")
	//	log.Printf("connection %d: %s", connections, client.ID)

}

func getEvent(data []byte, _ int) string {
	var js struct {
		Event string
	}
	json.Unmarshal(data, &js)
	return js.Event
}

func setHandlers() {
	server.On("print", func(c *wsevent.Client, data []byte, _ int) ([]byte, int) {
		var args struct {
			Str interface{}
		}
		json.Unmarshal(data, &args)
		log.Printf("From %s: %v", c.Id(), args.Str)
		return []byte(`{"success": true}`), ws.TextMessage
	})
}

func main() {
	addr := flag.String("addr", "localhost:8081", "http service address")
	server.Extractor = getEvent
	setHandlers()

	go server.Listener()
	http.HandleFunc("/", handler)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
