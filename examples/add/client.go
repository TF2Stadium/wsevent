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

	"encoding/json"
	ws "github.com/gorilla/websocket"
	"strconv"
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
		defer c.Close()
		for {
			_, bytes, err := c.ReadMessage()
			if err != nil {
				log.Println("read:", err)
				break
			}
			log.Printf("%s", string(bytes))
		}
	}()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var i int
	for _ = range ticker.C {
		type event struct {
			Event string `json:"event"`
			Num1  int    `json:"num1"`
			Num2  int    `json:"num2"`
		}

		data := struct {
			Id   string `json:"id"`
			Data event  `json:"data"`
		}{strconv.Itoa(i), event{"add", rand.Int(), rand.Int()}}

		err := c.WriteJSON(data)

		bytes, _ := json.Marshal(data)
		log.Println(string(bytes))
		if err != nil {
			log.Fatal("write:", err)
		}
		i++
	}
}
