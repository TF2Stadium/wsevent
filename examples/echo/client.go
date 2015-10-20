//Copyright 2015 Vibhav Pant. All rights reserved.
//Use of this source code is governed by the MIT
//that can be found in the LICENSE file.

// +build ignore

package main

import (
	"flag"
	"log"
	"net/url"

	ws "github.com/gorilla/websocket"
	"time"
)

func main() {
	addr := flag.String("addr", "localhost:8081", "http service address")
	u := url.URL{Scheme: "ws", Host: *addr, Path: "/"}
	c, _, err := ws.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("dial", err)
	}
	go func() {
		var resp struct {
			Success bool
		}
		defer c.Close()
		for {
			err := c.ReadJSON(&resp)
			if err != nil {
				log.Println("read:", err)
				break
			}
			log.Printf("success: %t", resp.Success)
		}
	}()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for t := range ticker.C {
		data := struct {
			Event string
			Str   string
		}{"print", t.String()}

		err := c.WriteJSON(data)
		if err != nil {
			log.Fatal("write:", err)
		}
	}
}
