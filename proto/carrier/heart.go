// Iris - Decentralized Messaging Framework
// Copyright 2013 Peter Szilagyi. All rights reserved.
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
//
// Author: peterke@gmail.com (Peter Szilagyi)

// This file contains the heartbeat event handlers and the related load repoting
// logic.

package carrier

import (
	"github.com/karalabe/iris/config"
	"log"
	"math/big"
)

// Load report between two carrier nodes.
type report struct {
	Tops []*big.Int // Topics shared between two carrier nodes
	Caps []int      // Capacity reports related to the topics above
}

// Adds the node within the topic to the list of monitored entities.
func (c *carrier) monitor(topic *big.Int, node *big.Int) error {
	id := new(big.Int).Add(new(big.Int).Lsh(topic, uint(config.OverlaySpace)), node)
	return c.heart.Monitor(id)
}

// Remove the node of a specific topic from the list of monitored entities.
func (c *carrier) unmonitor(topic *big.Int, node *big.Int) error {
	id := new(big.Int).Add(new(big.Int).Lsh(topic, uint(config.OverlaySpace)), node)
	return c.heart.Unmonitor(id)
}

// Updates the last ping time of a node within a topic.
func (c *carrier) ping(topic *big.Int, node *big.Int) error {
	id := new(big.Int).Add(new(big.Int).Lsh(topic, uint(config.OverlaySpace)), node)
	return c.heart.Ping(id)
}

// Implements the heart.Callback.Beat method. At each heartbeat, the load stats
// of all the topics are gathered, mapped to destination nodes and sent out. In
// addition, each root topic sends a subscription message to disconver newly
// added roots.
func (c *carrier) Beat() {
	c.lock.RLock()
	defer c.lock.RUnlock()

	// Collect and assemble load reports
	reports := make(map[string]*report)
	for _, top := range c.topics {
		ids, caps := top.GenerateReport()
		for i, id := range ids {
			sid := id.String()
			rep, ok := reports[id.String()]
			if !ok {
				rep = &report{[]*big.Int{}, []int{}}
				reports[sid] = rep
			}
			rep.Tops = append(rep.Tops, top.Self())
			rep.Caps = append(rep.Caps, caps[i])
		}
		top.Cycle()
	}
	// Distribute the load reports to the remote carriers
	for sid, rep := range reports {
		if id, ok := new(big.Int).SetString(sid, 10); ok {
			go c.sendReport(id, rep)
		} else {
			panic("failed to extract node id.")
		}
	}
	// Subscribe all root topics
	for _, top := range c.topics {
		if top.Parent() == nil {
			go c.sendSubscribe(top.Self())
		}
	}
}

// Implements the heat.Callback.Dead method, monitoring the death events of
// topic member nodes.
func (c *carrier) Dead(id *big.Int) {
	// Split the id into topic and node parts
	topic := new(big.Int).Rsh(id, uint(config.OverlaySpace))
	node := new(big.Int).Sub(id, new(big.Int).Lsh(topic, uint(config.OverlaySpace)))

	// Depending on whether it was the topic parent or a child reown or unsub
	c.lock.RLock()
	top, ok := c.topics[topic.String()]
	c.lock.RUnlock()

	if ok {
		parent := top.Parent()
		if parent != nil && parent.Cmp(node) == 0 {
			// Make sure it's out of the heartbeat mechanism
			if err := c.heart.Unmonitor(id); err != nil {
				log.Printf("carrier: failed to unmonitor parent %v from topic %v: %v.", node, topic, err)
			}
			// Reassign topic rendes-vous point
			top.Reown(nil)
		} else {
			c.handleUnsubscribe(node, topic, false)
		}
	} else {
		log.Printf("carrier: topic %v already dead.", topic)
	}
}
