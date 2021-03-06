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

// Package relay implements the message relay between the Iris node and locally
// attached applications.
package relay

import (
	"bufio"
	"net"
	"sync"

	"github.com/project-iris/iris/config"
	"github.com/project-iris/iris/pool"
	"github.com/project-iris/iris/proto/iris"
)

// Message relay between the local carrier and an attached client app.
type relay struct {
	// Application layer fields
	iris *iris.Connection // Interface into the iris overlay

	reqIdx  uint64                 // Index to assign the next request
	reqPend map[uint64]chan []byte // Active requests waiting for a reply
	reqLock sync.RWMutex           // Mutex to protect the request map

	tunIdx  uint64                   // Temporary index to assign the next inbound tunnel
	tunPend map[uint64]*iris.Tunnel  // Tunnels pending app confirmation
	tunInit map[uint64]chan struct{} // Confirmation channels for the pending tunnels
	tunLive map[uint64]*tunnel       // Active tunnels
	tunLock sync.RWMutex             // Mutex to protect the tunnel maps

	// Network layer fields
	sock     net.Conn          // Network connection to the attached client
	sockBuf  *bufio.ReadWriter // Buffered access to the network socket
	sockLock sync.Mutex        // Mutex to atomise message sending

	// Quality of service fields
	workers *pool.ThreadPool // Concurrent threads handling the connection

	// Bookkeeping fields
	done chan *relay     // Channel on which to signal termination
	quit chan chan error // Quit channe to synchronize relay termination
	term chan struct{}   // Channel to signal termination to blocked go-routines
}

// Accepts an inbound relay connection, executing the initialization procedure.
func (r *Relay) acceptRelay(sock net.Conn) (*relay, error) {
	// Create the relay object
	rel := &relay{
		reqPend: make(map[uint64]chan []byte),
		tunPend: make(map[uint64]*iris.Tunnel),
		tunInit: make(map[uint64]chan struct{}),
		tunLive: make(map[uint64]*tunnel),

		// Network layer
		sock:    sock,
		sockBuf: bufio.NewReadWriter(bufio.NewReader(sock), bufio.NewWriter(sock)),

		// Quality of service
		workers: pool.NewThreadPool(config.RelayHandlerThreads),

		// Misc
		done: r.done,
		quit: make(chan chan error),
		term: make(chan struct{}),
	}
	// Lock the socket to ensure no writes pass during init
	rel.sockLock.Lock()
	defer rel.sockLock.Unlock()

	// Initialize the relay
	app, err := rel.procInit()
	if err != nil {
		rel.drop()
		return nil, err
	}
	// Connect to the Iris network
	conn, err := r.iris.Connect(app, rel)
	if err != nil {
		rel.drop()
		return nil, err
	}
	rel.iris = conn

	// Report the connection accepted
	if err := rel.sendInit(); err != nil {
		rel.drop()
		return nil, err
	}
	// Start accepting messages and return
	rel.workers.Start()
	go rel.process()
	return rel, nil
}

// Forcefully drops the relay connection. Used during irrecoverable errors.
func (r *relay) drop() {
	r.sock.Close()
}

// Fetches the closure report from the relay.
func (r *relay) report() error {
	errc := make(chan error, 1)
	r.quit <- errc
	return <-errc
}
