// Iris - Decentralized cloud messaging
// Copyright (c) 2014 Project Iris. All rights reserved.
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

// Package iris implements the iris communication primitives on top of scribe.
package iris

import (
	"crypto/rsa"
	"fmt"
	"log"
	"net"
	"sync"

	"github.com/project-iris/iris/proto/scribe"
)

// The overlay implementation, receiving the overlay events and processing
// them according to the iris protocol.
type Overlay struct {
	scribe *scribe.Overlay // Overlay network to route the messages with

	autoid uint64                 // Id to assign to the next connection
	conns  map[uint64]*Connection // Live client connections

	subLive map[string][]uint64     // Live members of each subscribed topic
	subLock map[string]sync.RWMutex // Locks protecting the individual topics

	tunAddrs []string          // Listener addresses for the tunnel endpoints
	tunQuits []chan chan error // Quit channels for the tunnel acceptors

	lock sync.RWMutex // Protects the overlay state
}

// Creates a new iris overlay.
func New(overId string, key *rsa.PrivateKey) *Overlay {
	// Create and initialize the overlay
	o := &Overlay{
		autoid:  1, // Zero's a special case with gob, skip it
		conns:   make(map[uint64]*Connection),
		subLive: make(map[string][]uint64),
		subLock: make(map[string]sync.RWMutex),
	}
	o.scribe = scribe.New(overId, key, o)
	return o
}

// Boots the overlay, returning the number of remote peers.
func (o *Overlay) Boot() (int, error) {
	// Boot the underlay and wait until it converges
	peers, err := o.scribe.Boot()
	if err != nil {
		return 0, err
	}
	// Start a tunnel acceptor on each network interface
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return 0, err
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok {
			if !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
				// Create a quit channel
				quit := make(chan chan error)
				o.tunQuits = append(o.tunQuits, quit)

				// Start and sync the acceptor
				live := make(chan struct{})
				go o.tunneler(ipnet, live, quit)
				<-live
			}
		}
	}
	return peers, nil
}

// Terminates the overlay and all lower layer network primitives.
func (o *Overlay) Shutdown() error {
	errs := []error{}
	errc := make(chan error)

	// Close the tunnel listeners to prevent new connections
	for _, quit := range o.tunQuits {
		quit <- errc
	}
	for i := 0; i < len(o.tunQuits); i++ {
		if err := <-errc; err != nil {
			errs = append(errs, err)
		}
	}
	// Terminate the scribe underlay
	if err := o.scribe.Shutdown(); err != nil {
		errs = append(errs, err)
	}
	// Report the errors and return
	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	default:
		return fmt.Errorf("%v", errs)
	}
}

// Subscribes to a new topic, or adds the current connection to the list of live
// subscriptions.
func (o *Overlay) subscribe(id uint64, topic string) error {
	cascade := false

	// Create a new subscription if non existed (mark as so)
	o.lock.Lock()
	if lock, ok := o.subLock[topic]; !ok {
		o.subLive[topic] = []uint64{id}
		o.subLock[topic] = sync.RWMutex{}
		cascade = true
	} else {
		// Lock the existing subscription and add the current connection
		lock.Lock()
		o.subLive[topic] = append(o.subLive[topic], id)
		lock.Unlock()
	}
	o.lock.Unlock()

	// If a new subscription was requested, do it
	if cascade {
		return o.scribe.Subscribe(topic)
	}
	return nil
}

// Unsubscribes a client from a topic, removing the scribe subscription too if
// the last client.
func (o *Overlay) unsubscribe(id uint64, topic string) error {
	o.lock.Lock()
	defer o.lock.Unlock()

	// Create a new subscription if non existed (mark as so)
	cascade := false
	if lock, ok := o.subLock[topic]; !ok {
		// This should *not* happen
		log.Printf("iris: unsubscribe from non-existent topic: %v.", topic)
		return ErrNotSubscribed
	} else {
		// Remove the subscription
		lock.Lock()
		subs := o.subLive[topic]
		done := false
		for i, subId := range subs {
			if id == subId {
				subs = append(subs[:i], subs[i+1:]...)
				done = true
				break
			}
		}
		o.subLive[topic] = subs
		lock.Unlock()

		// Actually check if anything was removed, just in case
		if !done {
			log.Printf("iris: remove non-existent subscription: %v:%v.", topic, id)
			return ErrNotSubscribed
		}
		if len(subs) == 0 {
			delete(o.subLive, topic)
			delete(o.subLock, topic)
			cascade = true
		}
	}
	// Dump the topic if all subscriptions are gone
	if cascade {
		return o.scribe.Unsubscribe(topic)
	}
	return nil
}
