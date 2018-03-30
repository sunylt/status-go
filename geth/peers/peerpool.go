package peers

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
	"github.com/status-im/status-go/geth/params"
)

var (
	// ErrDiscv5NotRunning returned when pool is started but discover v5 is not running or not enabled.
	ErrDiscv5NotRunning = errors.New("Discovery v5 is not running")
)

const (
	// expirationPeriod is a amount of time while peer is considered as a connectable
	expirationPeriod = 60 * time.Minute
	// DefaultFastSync is a recommended value for aggressive peers search.
	DefaultFastSync = 3 * time.Second
	// DefaultSlowSync is a recommended value for slow (background) peers search.
	DefaultSlowSync = 30 * time.Minute
)

// NewPeerPool creates instance of PeerPool
func NewPeerPool(config map[discv5.Topic]params.Limits, fastSync, slowSync time.Duration, cache *Cache) *PeerPool {
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
	// connected is true if node is added as a static peer
	connected bool

	node *discv5.Node
}

// PeerPool manages discovered peers and connects them to p2p server
type PeerPool struct {
	// config can be set only once per pool life cycle
	config   map[discv5.Topic]params.Limits
	fastSync time.Duration
	slowSync time.Duration
	cache    *Cache

	mu sync.RWMutex
	// TODO split this into separate maps to avoid unnecessary locking
	peers         map[discv5.Topic]map[discv5.NodeID]*peerInfo
	syncPeriods   []chan time.Duration
	subscriptions []event.Subscription
	quit          chan struct{}

	consumerWG sync.WaitGroup
	discvWG    sync.WaitGroup
}

// init creates data structures used by peer pool
// thread safety must be guaranteed by caller
func (p *PeerPool) init() {
	p.quit = make(chan struct{})
	p.subscriptions = make([]event.Subscription, 0, len(p.config))
	// sync periods are stored because we need to close them once pool is stopped
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
	p.init()

	p.consumerWG.Add(len(p.config))
	p.discvWG.Add(len(p.config))
	for topic, limits := range p.config {
		topic := topic // bind topic to be used in goroutine
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
			p.discvWG.Done()
		}()

		go func() {
			p.handlePeersFromTopic(server, topic, period, found, lookup, events)
			p.consumerWG.Done()
		}()
	}
	return nil
}

func (p *PeerPool) handlePeersFromTopic(server *p2p.Server, topic discv5.Topic, period chan<- time.Duration, found <-chan *discv5.Node, lookup <-chan bool, events <-chan *p2p.PeerEvent) {
	limits := p.config[topic]
	fast := true
	period <- p.fastSync
	connected := 0
	selfID := discv5.NodeID(server.Self().ID)
	for {
		select {
		case <-p.quit:
			log.Debug("exited consumer", "topic", topic)
			return
		case node := <-found:
			log.Debug("found node with", "ID", node.ID, "topic", topic)
			if node.ID == selfID {
				continue
			}
			p.processFoundNode(server, connected, topic, node)
		case <-lookup:
			// just drain this channel for now, it can be used to intelligently
			// limit number of kademlia lookups
		case event := <-events:
			if event.Type == p2p.PeerEventTypeDrop {
				log.Debug("node dropped", "ID", event.Peer, "topic", topic)
				if p.processDroppedNode(server, topic, event.Peer, event.Error) {
					connected--
					if !fast && connected < limits[0] {
						log.Debug("switch to fast sync", "topic", topic, "sync", p.fastSync)
						period <- p.fastSync
						fast = true
					}
				}
			} else if event.Type == p2p.PeerEventTypeAdd {
				if p.processAddedNode(server, connected, event.Peer, topic) {
					connected++
					log.Debug("currently connected", "topic", topic, "N", connected)
					if fast && connected >= limits[0] {
						log.Debug("switch to slow sync", "topic", topic, "sync", p.slowSync)
						period <- p.slowSync
						fast = false
					}
				}
			}
		}
	}
}

// processFoundNode called when node is discovered by kademlia search query
// 2 important conditions
// 1. every time when node is processed we need to update discoveredTime and reset dropped boolean.
//    peer will be considered as valid later only if it was discovered < 60m ago and wasn't dropped recently
// 2. if peer is connected or if max limit is reached we are not a adding peer to p2p server
func (p *PeerPool) processFoundNode(server *p2p.Server, currentlyConnected int, topic discv5.Topic, node *discv5.Node) {
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
		log.Debug("peer found", "ID", node.ID, "topic", topic)
		p.addPeer(server, peersTable[node.ID], topic)
	}
	return
}

func (p *PeerPool) processAddedNode(server *p2p.Server, currentlyConnected int, nodeID discover.NodeID, topic discv5.Topic) bool {
	log.Debug("new connection confirmed", "ID", nodeID)
	p.mu.Lock()
	defer p.mu.Unlock()
	peersTable := p.peers[topic]
	limits := p.config[topic]
	// inbound connection
	peer, exist := peersTable[discv5.NodeID(nodeID)]
	if !exist {
		return false
	}
	// established connection means that the node is a viable candidate for a connection and can be cached
	if p.cache != nil {
		if err := p.cache.AddPeer(peer.node, topic); err != nil {
			log.Error("failed to persist a peer", "error", err)
		}
	}
	// when max limit is reached drop every peer after
	if currentlyConnected == limits[1] {
		log.Debug("max limit is reached drop the peer", "ID", nodeID, "topic", topic)
		p.removePeer(server, peer, topic)
		return false
	}
	// don't count same peer twice
	if !peer.connected {
		log.Debug("marking as connected", "ID", nodeID)
		peer.connected = true
		return true
	}
	return false
}

// processDroppedNode is called when node was dropped by p2p server
// - removes a peer, cause p2p server now relies on peer pool to maintain required connections number
// - if there is a valid peer in peer table add it to a p2p server
//   peer is valid if it wasn't dropped recently, it is not stale (90s) and not currently connected
// returns true if dropped node should decrement connected counter, e.g. false if:
// - peer connected with different topic
// - peer is inbound connection with unknown topic
// - we requested disconnect
func (p *PeerPool) processDroppedNode(server *p2p.Server, topic discv5.Topic, nodeID discover.NodeID, reason string) (dropped bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// either inbound or connected from another topic
	peer, exist := p.peers[topic][discv5.NodeID(nodeID)]
	if !exist {
		return false
	}
	log.Debug("disconnect reason", "peer", nodeID, "reason", reason)
	// if requested - we don't need to remove peer from cache and look for a replacement
	if reason == p2p.DiscRequested.Error() {
		return false
	}
	p.removePeer(server, peer, topic)
	delete(p.peers[topic], discv5.NodeID(nodeID))
	if p.cache != nil {
		if err := p.cache.RemovePeer(discv5.NodeID(nodeID), topic); err != nil {
			log.Error("failed to remove peer from cache", "error", err)
		}
	}
	// TODO use a heap queue and always get a peer that was discovered recently
	for _, peer := range p.peers[topic] {
		if !peer.connected && mclock.Now() < peer.discoveredTime+mclock.AbsTime(expirationPeriod) {
			p.addPeer(server, peer, topic)
			return true
		}
	}
	return true
}

func (p *PeerPool) addPeer(server *p2p.Server, info *peerInfo, topic discv5.Topic) {
	server.AddPeer(discover.NewNode(
		discover.NodeID(info.node.ID),
		info.node.IP,
		info.node.UDP,
		info.node.TCP,
	))
}

func (p *PeerPool) removePeer(server *p2p.Server, info *peerInfo, topic discv5.Topic) {
	server.RemovePeer(discover.NewNode(
		discover.NodeID(info.node.ID),
		info.node.IP,
		info.node.UDP,
		info.node.TCP,
	))
}

// Stop closes pool quit channel and all channels that are watched by search queries
// and waits till all goroutines will exit.
func (p *PeerPool) Stop() {
	// pool wasn't started
	if p.quit == nil {
		return
	}
	select {
	case <-p.quit:
		return
	default:
		log.Debug("started closing peer pool")
		close(p.quit)
	}
	log.Debug("waiting for consumer goroutines to exit")
	p.consumerWG.Wait()
	for _, period := range p.syncPeriods {
		close(period)
	}
	for _, sub := range p.subscriptions {
		sub.Unsubscribe()
	}
	log.Debug("waiting for discovery requests to exit")
	p.discvWG.Wait()
}
