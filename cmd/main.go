package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kyawmyintthein/gossip-replicator/pkg/replicator"
)

func main() {
	var errors []chan error

	node1 := replicator.NewNode("node1", 1, 3, "127.0.0.1", 9000, 7900, "")
	errors = append(errors, node1.Start())

	// give first node a break
	time.Sleep(1 * time.Second)

	node2 := replicator.NewNode("node2", 2, 3, "127.0.0.1", 9001, 7901, "localhost:7900")
	errors = append(errors, node2.Start())

	node3 := replicator.NewNode("node3", 3, 3, "127.0.0.1", 9002, 7902, "localhost:7901")
	errors = append(errors, node3.Start())

	// agregate nodes errors into a single channel
	agg := make(chan error)
	for _, errChan := range errors {
		go func(c chan error) {
			for msg := range c {
				agg <- msg
			}
		}(errChan)
	}

	shutdown := make(chan os.Signal)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-shutdown
		log.Println("shutting down...")
		node1.Shutdown()
		node2.Shutdown()
		node3.Shutdown()
		log.Println("all nodes shutdown... exiting now.")
		os.Exit(0)
	}()

	select {
	case err := <-agg:
		fmt.Println(err)
		node1.Shutdown()
		node2.Shutdown()
		node3.Shutdown()
		os.Exit(1)
	}
}
