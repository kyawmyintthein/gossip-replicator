package replicator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/hashicorp/memberlist"
	"github.com/kyawmyintthein/gossip-replicator/pkg/storage"
	"github.com/kyawmyintthein/gossip-replicator/rpc"
	"github.com/twitchtv/twirp"
)

type Node struct {

	// host and node ports for gossiping and api
	addr    string
	apiPort int

	// addr:port of any node in the cluster to join to; empty if it's the first node
	clusterNodeAddr string

	// Holds the node data state; it's also the Delegate used by memberlist to gossip state
	storage *storage.InMemoryStorage

	memberConfig *memberlist.Config
	memberlist   *memberlist.Memberlist

	httpServer *http.Server
}

func NewNode(name string, addr string, apiPort, gossipPort int, clusterNodeAddr string) *Node {
	config := memberlist.DefaultLocalConfig()
	config.Name = name
	config.BindAddr = addr
	config.BindPort = gossipPort
	config.AdvertisePort = config.BindPort

	md := make(map[string]string, 1)
	md["apiPort"] = strconv.Itoa(apiPort)

	backendStorage := storage.NewInMemoryDB(md)
	config.Delegate = backendStorage

	return &Node{
		addr:            addr,
		apiPort:         apiPort,
		clusterNodeAddr: clusterNodeAddr,
		storage:         backendStorage,
		memberConfig:    config,
	}
}

// Put adds config to the local store
func (n *Node) Put(ctx context.Context, req *rpc.PutEventRequest) (*rpc.Event, error) {
	key := req.GetKey()
	v := storage.V{
		Data:      []byte(req.Data),
		Version:   int(req.Version),
		IsDeleted: req.Deleted,
	}

	val, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	// update local state
	err = n.storage.Put(key, val)
	if err != nil {
		return nil, err
	}
	log.Println("succesfully put config", req.GetKey(), v)

	return &rpc.Event{Key: key, Version: int32(v.Version), Data: string(v.Data), Deleted: v.IsDeleted}, nil
}

// Get fetches config from the local store
func (n *Node) Get(ctx context.Context, req *rpc.GetEventRequest) (*rpc.Event, error) {
	key := req.GetKey()
	b, err := n.storage.Get(key)
	if err != nil {
		log.Println("failed to get from storage", key, err)
		return nil, err
	}

	var v storage.V
	err = json.Unmarshal(b, &v)
	if err != nil {
		log.Println("failed to marshal from storage", key, err)
		return nil, err
	}
	return &rpc.Event{Key: key, Version: int32(v.Version), Data: string(v.Data), Deleted: v.IsDeleted}, nil
}

// Start async runs gRPC server and joins cluster
func (n *Node) Start() chan error {
	errChan := make(chan error)
	go n.serve(errChan)
	go n.joinCluster(errChan)
	return errChan
}

// Shutdown stops gRPC server and leaves cluster
func (n *Node) Shutdown() {
	//n.twirpServer.GracefulStop()
	n.memberlist.Leave(15 * time.Second)
	n.memberlist.Shutdown()
}

func (n *Node) serve(errChan chan error) {
	n.httpServer = &http.Server{
		Addr:         fmt.Sprintf(":%d", n.apiPort),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	replicatorHandler := rpc.NewEventReplicatorServiceServer(n,
		twirp.WithServerPathPrefix("/rz"))
	n.httpServer.Handler = replicatorHandler
	go func() {
		err := n.httpServer.ListenAndServe()
		if err != nil {
			log.Println("Failed to start HTTP server on port : ", n.apiPort, err)
			os.Exit(-1)
		}
	}()
	log.Println("HTTP Server started on port : ", n.apiPort)
}

func (n *Node) joinCluster(errChan chan error) {
	var err error
	n.memberlist, err = memberlist.Create(n.memberConfig)
	if err != nil {
		log.Println("failed to init memberlist", err)
		errChan <- err
	}

	var nodeAddr string
	if n.clusterNodeAddr != "" {
		log.Printf("not the first node, joining %s...", n.clusterNodeAddr)
		nodeAddr = n.clusterNodeAddr
	} else {
		log.Println("first node of the cluster...")
		nodeAddr = fmt.Sprintf("%s:%d", n.addr, n.memberConfig.BindPort)
	}
	_, err = n.memberlist.Join([]string{nodeAddr})
	if err != nil {
		log.Println("failed to join cluster", err)
		errChan <- err
	}

	log.Println("succesfully joined cluster via", nodeAddr)
}
