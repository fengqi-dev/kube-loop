package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/gateway"
	"github.com/fengqi-dev/kube-loop/internal/gateway/websocketmux"
)

var version = "dev"

func main() {
	listenAddress := flag.String("listen", ":1080", "gateway listen address")
	httpListenAddress := flag.String("http-listen", ":8080", "WebSocket Gateway listen address")
	httpPath := flag.String("http-path", websocketmux.DefaultPath, "WebSocket Gateway path")
	httpToken := flag.String("http-token", os.Getenv("KUBELOOP_GATEWAY_TOKEN"), "WebSocket Gateway bearer token (or KUBELOOP_GATEWAY_TOKEN)")
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
	server := gateway.NewServer(logger, 10*time.Second)
	errCh := make(chan error, 2)
	go func() {
		logger.Printf("kube-loop gateway %s listening on %s", version, *listenAddress)
		errCh <- server.Serve(listener)
	}()
	if *httpToken != "" {
		if !strings.HasPrefix(*httpPath, "/") {
			logger.Fatal("WebSocket Gateway path must start with /")
		}
		handler, handlerErr := websocketmux.NewHandler(websocketmux.ServerConfig{
			Token:  *httpToken,
			Logger: logger,
			Handle: server.ServeConn,
		})
		if handlerErr != nil {
			logger.Fatal(handlerErr)
		}
		httpListener, listenErr := net.Listen("tcp", *httpListenAddress)
		if listenErr != nil {
			logger.Fatal(listenErr)
		}
		mux := http.NewServeMux()
		if *httpPath != "/" {
			mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
				logger.Printf("WebSocket request rejected: remote=%s method=%s path=%s status=%d reason=path", request.RemoteAddr, request.Method, request.URL.Path, http.StatusNotFound)
				http.NotFound(writer, request)
			})
		}
		mux.Handle(*httpPath, handler)
		go func() {
			logger.Printf("WebSocket Gateway listening on %s%s", *httpListenAddress, *httpPath)
			errCh <- websocketmux.Serve(context.Background(), httpListener, mux)
		}()
	} else {
		logger.Print("WebSocket Gateway disabled: KUBELOOP_GATEWAY_TOKEN is empty")
	}
	if err := <-errCh; err != nil {
		logger.Fatal(err)
	}
}
