//Copyright 2015 Vibhav Pant. All rights reserved.
//Use of this source code is governed by the MIT
//that can be found in the LICENSE file.

// +build ignore

package main

import (
	"flag"
	"log"
	"math/rand"
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
	rand.Seed(time.Now().Unix())
	go func() {
		var resp struct {
			Result uint64
		}
		defer c.Close()
		for {
			err := c.ReadJSON(&resp)
			if err != nil {
				log.Println("read:", err)
				break
			}
			log.Printf("success: %d", resp.Result)
		}
	}()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for _ = range ticker.C {
		data := struct {
			Event string
			Num1  int
			Num2  int
		}{"add", rand.Int(), rand.Int()}

		err := c.WriteJSON(data)
		if err != nil {
			log.Fatal("write:", err)
		}
	}
}
