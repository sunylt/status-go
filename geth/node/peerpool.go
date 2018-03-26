/*
Peer pool works in a following way:
1. For each configured topic it will create discovery v5 search query.
Search query will periodically do regular kademlia lookups with bucket size 16
and send a topic query to every node that is returned from kademlia lookup.
Eventually nodes with required topics will be found and we will pass them to p2p server.
2. Additional loop will be created for every topic that will synchronize
found nodes with p2p server. This loop will follow next logic:
- if node is found and max limit of peers is not reached we will add this node to
  server and assume that it is connected
- if max limit is reached we will add peer to our peer topic table for later use
- if min limit is reached - frequency will be changed to a keepalive timer, this is required cause we need
  frequent lookups only when we are looking for a peer
3. when peer is disconnected we do 3 things:
  - select new peer from peers table that was updated no longer than foundTimeout and not
    in the connected (90s)
  - set a peer as not connected
  - check how many peers do we have and in case if we went below min limit - set period to fastSync
*/

package node

import (
	"errors"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common/mclock"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/discover"
	"github.com/ethereum/go-ethereum/p2p/discv5"
	"github.com/status-im/status-go/geth/db"
	"github.com/status-im/status-go/geth/params"
)

var (
	// ErrDiscv5NotRunning returned when pool is started but discover v5 is not running or not enabled.
	ErrDiscv5NotRunning = errors.New("Discovery v5 is not running")
)

const (
	foundTimeout    = 60 * time.Minute
	defaultFastSync = 500 * time.Millisecond
	defaultSlowSync = 30 * time.Minute
)

// NewPeerPool creates instance of PeerPool
func NewPeerPool(config map[discv5.Topic]params.Limits, fastSync, slowSync time.Duration, cache *db.PeersDatabase) *PeerPool {
	return &PeerPool{
		config:   config,
		fastSync: fastSync,
		slowSync: slowSync,
		cache:    cache,
	}
}

type peerInfo struct {
	// discoveredTime last time when node was found by v5
	discoveredTime mclock.AbsTime
	// connected is true if node is added as a statis peer
	connected bool

	node *discv5.Node
}

// PeerPool manages discovered peers and connects them to p2p server
type PeerPool struct {
	// config can be set only once per pool life cycle
	config   map[discv5.Topic]params.Limits
	fastSync time.Duration
	slowSync time.Duration
	cache    *db.PeersDatabase

	mu sync.RWMutex
	// TODO split this into separate maps to avoid unnecessary locking
	peers         map[discv5.Topic]map[discv5.NodeID]*peerInfo
	syncPeriods   []chan time.Duration
	subscriptions []event.Subscription
	quit          chan struct{}

	wg sync.WaitGroup
}

// init creates data structures used by peer pool
// thread safety must be guaranteed by caller
func (p *PeerPool) init() {
	p.subscriptions = make([]event.Subscription, 0, len(p.config))
	p.syncPeriods = make([]chan time.Duration, 0, len(p.config))
	p.peers = make(map[discv5.Topic]map[discv5.NodeID]*peerInfo)
	for topic := range p.config {
		p.peers[topic] = make(map[discv5.NodeID]*peerInfo)
	}
}

// Start creates discovery search query for each topic and consumes peers found in that topic
// in separate loop.
func (p *PeerPool) Start(server *p2p.Server) error {
	if server.DiscV5 == nil {
		return ErrDiscv5NotRunning
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.quit = make(chan struct{})
	// sync periods are stored because we need to close them once pool is stopped
	p.init()

	// 2 goroutines per each topic
	p.wg.Add(len(p.config) * 2)
	for topic, limits := range p.config {
		topic := topic
		period := make(chan time.Duration, 2)
		p.syncPeriods = append(p.syncPeriods, period)
		found := make(chan *discv5.Node, 10)
		if p.cache != nil {
			for _, peer := range p.cache.GetPeersRange(topic, 5) {
				log.Debug("adding a peer from cache", "peer", peer)
				found <- peer
			}
		}
		lookup := make(chan bool, 100)
		events := make(chan *p2p.PeerEvent, 20)
		subscription := server.SubscribeEvents(events)
		p.subscriptions = append(p.subscriptions, subscription)
		log.Debug("running peering for", "topic", topic, "limits", limits)
		go func() {
			server.DiscV5.SearchTopic(topic, period, found, lookup)
			p.wg.Done()
		}()

		go func() {
			p.handlePeersFromTopic(server, topic, period, found, lookup, events)
			p.wg.Done()
		}()
	}
	return nil
}

func (p *PeerPool) handlePeersFromTopic(server *p2p.Server, topic discv5.Topic, period chan time.Duration, found chan *discv5.Node, lookup chan bool, events chan *p2p.PeerEvent) {
	limits := p.config[topic]
	fast := true
	period <- p.fastSync
	connected := 0
	selfID := discv5.NodeID(server.Self().ID)
	for {
		select {
		case <-p.quit:
			return
		case node := <-found:
			log.Debug("found node with", "ID", node.ID, "topic", topic)
			if node.ID == selfID {
				continue
			}
			if p.processFoundNode(server, connected, topic, node) {
				connected++
			}
			// switch period only once
			if fast && connected >= limits[0] {
				log.Debug("switch to slow sync", "topic", topic, "sync", p.slowSync)
				period <- p.slowSync
				fast = false
			}
		case <-lookup:
			// just drain this channel for now, it can be used to intellegently
			// limit number of kademlia lookups
		case event := <-events:
			if event.Type == p2p.PeerEventTypeDrop {
				log.Debug("node dropped", "ID", event.Peer, "topic", topic)
				if !p.processDisconnectedNode(server, topic, event.Peer) {
					connected--
				}
				// switch period only once
				if !fast && connected < limits[0] {
					log.Debug("switch to fast sync", "topic", topic, "sync", p.fastSync)
					period <- p.fastSync
					fast = true
				}
			} else if event.Type == p2p.PeerEventTypeAdd {
				// TODO take into account inbound connections
			}
		}
	}
}

// processFoundNode called when node is discovered by kademlia search query
// 2 important conditions
// 1. every time when node is processed we need to update discoveredTime and reset dropped boolean.
//    peer will be considered as valid later only if it was discovered < 90s ago and wasn't dropped recently
// 2. if peer is connected or if max limit is reached we are not a adding peer to p2p server
func (p *PeerPool) processFoundNode(server *p2p.Server, currentlyConnected int, topic discv5.Topic, node *discv5.Node) (connected bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	peersTable := p.peers[topic]
	limits := p.config[topic]
	if info, exist := peersTable[node.ID]; exist {
		info.discoveredTime = mclock.Now()
	} else {
		peersTable[node.ID] = &peerInfo{
			discoveredTime: mclock.Now(),
			node:           node,
		}
	}
	if currentlyConnected < limits[1] && !peersTable[node.ID].connected {
		log.Debug("peer connected", "ID", node.ID, "topic", topic)
		server.AddPeer(discover.NewNode(
			discover.NodeID(peersTable[node.ID].node.ID),
			peersTable[node.ID].node.IP,
			peersTable[node.ID].node.UDP,
			peersTable[node.ID].node.TCP,
		))
		peersTable[node.ID].connected = true
		connected = true
		if p.cache != nil {
			if err := p.cache.AddPeer(node, topic); err != nil {
				log.Error("failed to persist a peer", "error", err)
			}
		}
	}
	return connected
}

// processDisconnectedNode is called when node was disconnected by p2p server
// - removes a peer, cause p2p server now relies on peer pool to maintain required connections number
// - if there is a valid peer in peer table add it to a p2p server
//   peer is valid if it wasn't dropped recently, it is not stale (90s) and not currently connected
func (p *PeerPool) processDisconnectedNode(server *p2p.Server, topic discv5.Topic, nodeID discover.NodeID) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	peersTable := p.peers[topic]
	// either inbound or connected from another topic
	if _, exist := peersTable[discv5.NodeID(nodeID)]; !exist {
		return true
	}
	p.removePeer(server, nodeID, topic)
	// TODO use a heap queue and always get a peer that was discovered recently
	for _, info := range peersTable {
		if !info.connected && mclock.Now() < info.discoveredTime+mclock.AbsTime(foundTimeout) {
			p.addPeer(server, info, topic)
			return true
		}
	}
	return false
}

func (p *PeerPool) addPeer(server *p2p.Server, info *peerInfo, topic discv5.Topic) {
	server.AddPeer(discover.NewNode(
		discover.NodeID(info.node.ID),
		info.node.IP,
		info.node.UDP,
		info.node.TCP,
	))
	info.connected = true
	if p.cache != nil {
		if err := p.cache.AddPeer(info.node, topic); err != nil {
			log.Error("failed to persist a peer", "error", err)
		}
	}
}

func (p *PeerPool) removePeer(server *p2p.Server, nodeID discover.NodeID, topic discv5.Topic) {
	info := p.peers[topic][nodeID]
	server.RemovePeer(discover.NewNode(
		discover.NodeID(info.node.ID),
		info.node.IP,
		info.node.UDP,
		info.node.TCP,
	))
	delete(p.peers[topic].peersTable, discv5.NodeID(nodeID))
	if p.cache != nil {
		if err := p.cache.RemovePeer(discv5.NodeID(nodeID), topic); err != nil {
			log.Error("failed to remove peer from cache", "error", err)
		}
	}
}

// Stop closes pool quit channel and all channels that are watched by search queries
// and waits till all goroutines will exit.
func (p *PeerPool) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	// pool wasn't started
	if p.quit == nil {
		return
	}
	select {
	case <-p.quit:
		return
	default:
	}
	close(p.quit)
	for _, period := range p.syncPeriods {
		close(period)
	}
	for _, sub := range p.subscriptions {
		sub.Unsubscribe()
	}
	p.wg.Wait()
}
