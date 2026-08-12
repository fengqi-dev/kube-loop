package main

import (
	"flag"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:18443", "TLS listen address")
	targetValue := flag.String("target", "http://127.0.0.1:18080", "upstream URL")
	certificate := flag.String("cert", "", "TLS certificate file")
	privateKey := flag.String("key", "", "TLS private key file")
	flag.Parse()

	target, err := url.Parse(*targetValue)
	if err != nil {
		log.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	server := &http.Server{
		Addr:              *listen,
		Handler:           proxy,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	log.Printf("serving %s through %s", target, *listen)
	if err := server.ListenAndServeTLS(*certificate, *privateKey); err != nil {
		log.Fatal(err)
	}
}
