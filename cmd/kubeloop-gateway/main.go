package main

import (
	"flag"
	"log"
	"net"
	"os"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/gateway"
)

var version = "dev"

func main() {
	listenAddress := flag.String("listen", ":1080", "gateway listen address")
	printResolvConf := flag.Bool("print-resolv-conf", false, "print the Pod DNS configuration and exit")
	flag.Parse()
	if *printResolvConf {
		content, err := os.ReadFile("/etc/resolv.conf")
		if err != nil {
			log.Fatal(err)
		}
		if _, err := os.Stdout.Write(content); err != nil {
			log.Fatal(err)
		}
		return
	}

	logger := log.New(os.Stdout, "gateway: ", log.LstdFlags|log.LUTC)
	listener, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		logger.Fatal(err)
	}
	logger.Printf("kube-loop gateway %s listening on %s", version, *listenAddress)
	server := gateway.NewServer(logger, 10*time.Second)
	if err := server.Serve(listener); err != nil {
		logger.Fatal(err)
	}
}
