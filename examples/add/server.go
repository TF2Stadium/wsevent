//Copyright 2015 Vibhav Pant. All rights reserved.
//Use of this source code is governed by the MIT
//that can be found in the LICENSE file.

// +build ignore

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

func getEvent(data string) string {
	var js struct {
		Event string
	}
	json.Unmarshal([]byte(data), &js)
	return js.Event
}

func setHandlers() {
	server.On("add", func(_ *wsevent.Server, c *wsevent.Client, data string) string {
		var args struct {
			Num1 int
			Num2 int
		}
		json.Unmarshal([]byte(data), &args)
		log.Printf("From %s: %d + %d = %d", c.Id(), args.Num1, args.Num2, uint64(args.Num1+args.Num2))
		resp := struct {
			Result uint64
		}{uint64(args.Num1 + args.Num2)}
		bytes, _ := json.Marshal(resp)
		return string(bytes)
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
