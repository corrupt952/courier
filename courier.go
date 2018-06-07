package main

import (
	"context"
	"bytes"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"regexp"
	"runtime"
	"strings"
	"errors"
	"fmt"

	"github.com/docker/docker/client"
)

const (
	FORWARDED_FOR_HEADER_NAME = "X-Forwarded-For"
	FORWARDED_HOST_HEADER_NAME = "X-Forwarded-Host"
)

type Proxy struct {
	ProxyHost   string
	DockerClient *client.Client
}

func (proxy *Proxy) resolveContainer(request *http.Request) (string, error) {
	re := regexp.MustCompile(`^([a-zA-Z0-9-]+)\.`)
	group := re.FindSubmatch([]byte(request.Host))
	if len(group) < 2 {
		return "", errors.New(fmt.Sprintf("Can't match regexp: %s", request.Host))
	}

	name := string(group[1])
	container, err := proxy.DockerClient.ContainerInspect(context.Background(), name)
	if err != nil {
		return "", errors.New(fmt.Sprintf("Container not found. %s", name))
	}
	// get first ports
	var containerPort string
	for _, port := range container.NetworkSettings.Ports {
		if 1 <= len(port) {
			containerPort = port[0].HostPort
			break
		}
	}
	if &containerPort == nil {
		return "", errors.New(fmt.Sprintf("Container has't port mappings. %s", name))
	}

	// port, err := nat.NewPort("tcp", string(group[2]))
	// if err != nil {
	// 	log.Printf("Container not found. %s", name)
	// 	// TODO: error handling
	// 	return ""
	// }
	// containerPort := container.NetworkSettings.Ports[port][0].HostPort

	return proxy.ProxyHost + ":" + containerPort, nil
}

func (proxy *Proxy) director(request *http.Request) {
	url := *request.URL
	url.Scheme = "http"

	host, err := proxy.resolveContainer(request)
	if err != nil {
		// TODO: error handling
		log.Println(err.Error())
		return
	}
	url.Host = host

	var buffer []byte
	if request.Body != nil {
		buffer, err = ioutil.ReadAll(request.Body)
		if err != nil {
			log.Fatal(err.Error())
		}
	}
	proxyRequest, err := http.NewRequest(request.Method, url.String(), bytes.NewBuffer(buffer))
	if err != nil {
		log.Fatal(err.Error())
	}
	proxyRequest.Header = request.Header

	if requestHost, _, err := net.SplitHostPort(request.RemoteAddr); err == nil {
		if values, ok := proxyRequest.Header[FORWARDED_FOR_HEADER_NAME]; ok {
			requestHost = strings.Join(values, ", ") + ", " + requestHost
		}
		proxyRequest.Header.Set(FORWARDED_FOR_HEADER_NAME, requestHost)
	}
	proxyRequest.Header.Set(FORWARDED_HOST_HEADER_NAME, request.Host)

	*request = *proxyRequest
}

func main() {
	dockerClient, err := client.NewEnvClient()
	if err != nil {
		log.Fatal("Err: %v", err)
	}

	ProxyHost := os.Getenv("COURIER_PROXY_HOST")
	if ProxyHost == "" {
		ProxyHost = "localhost"
	}

	port := os.Getenv("COURIER_PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Listen %s", port)
	runtime.GOMAXPROCS(runtime.NumCPU())

	proxy := &Proxy{ProxyHost:ProxyHost,DockerClient: dockerClient,}
	reverseProxy := &httputil.ReverseProxy{Director: proxy.director,}
	server := http.Server{Addr: ":" + port, Handler: reverseProxy,}
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err.Error())
	}
}
