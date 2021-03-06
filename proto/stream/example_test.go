// Iris - Decentralized cloud messaging
// Copyright (c) 2013 Project Iris. All rights reserved.
//
// Iris is dual licensed: you can redistribute it and/or modify it under the
// terms of the GNU General Public License as published by the Free Software
// Foundation, either version 3 of the License, or (at your option) any later
// version.
//
// The framework is distributed in the hope that it will be useful, but WITHOUT
// ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
// FITNESS FOR A PARTICULAR PURPOSE.  See the GNU General Public License for
// more details.
//
// Alternatively, the Iris framework may be used in accordance with the terms
// and conditions contained in a signed written agreement between you and the
// author(s).

package stream_test

import (
	"fmt"
	"net"
	"time"

	"github.com/karalabe/iris/proto/stream"
)

var host = "localhost"
var port = 55555

// Stream example of an echo server and client using streams.
func Example_usage() {
	live := make(chan struct{})
	quit := make(chan struct{})
	data := make(chan string)
	msg := "Hello Stream!"

	go server(live, quit)
	<-live
	go client(msg, data)

	fmt.Println("Input message:", msg)
	fmt.Println("Output message:", <-data)

	close(quit)
	// Output:
	// Input message: Hello Stream!
	// Output message: Hello Stream!
}

func server(live, quit chan struct{}) {
	// Open a TCP port to accept incoming stream connections
	addr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		fmt.Println("Failed to resolve local address:", err)
		return
	}
	sock, err := stream.Listen(addr)
	if err != nil {
		fmt.Println("Failed to listen for incoming streams:", err)
		return
	}
	sock.Accept(time.Second)
	live <- struct{}{}

	// While not exiting, process stream connections
	for {
		select {
		case <-quit:
			if err = sock.Close(); err != nil {
				fmt.Println("Failed to terminate stream listener:", err)
			}
			return
		case strm := <-sock.Sink:
			defer strm.Close()

			// Receive and echo back a string
			var data string
			if err = strm.Recv(&data); err != nil {
				fmt.Println("Failed to receive a string object:", err)
				continue
			}
			if err = strm.Send(&data); err != nil {
				fmt.Println("Failed to send back a string object:", err)
				continue
			}
			if err = strm.Flush(); err != nil {
				fmt.Println("Failed to flush the response:", err)
				return
			}
		}
	}
}

func client(msg string, ch chan string) {
	// Open a TCP connection to the stream server
	addr := fmt.Sprintf("%s:%d", host, port)
	strm, err := stream.Dial(addr, time.Second)
	if err != nil {
		fmt.Println("Failed to connect to stream server:", err)
		return
	}
	defer strm.Close()

	// Send the message and receive a reply
	if err = strm.Send(msg); err != nil {
		fmt.Println("Failed to send the message:", err)
		return
	}
	if err = strm.Flush(); err != nil {
		fmt.Println("Failed to flush the message:", err)
		return
	}
	if err = strm.Recv(&msg); err != nil {
		fmt.Println("Failed to receive the reply:", err)
		return
	}
	// Return the reply to the caller and terminate
	ch <- msg
}
