package main

import(
	"os"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"regexp"

	"github.com/fsouza/go-dockerclient"
	"github.com/joho/godotenv"
)

var ignoredHeaderNames = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailers",
	"Transfer-Encoding",
	"Upgrade",
}

type Proxy struct {
	RequestConverter func(r, proxyRequest *http.Request)
	Transport http.RoundTripper
}

func NewProxyWithHostConverter(hostConverter func(string) string) *Proxy {
	return &Proxy{
		RequestConverter: func(r, proxyRequest *http.Request) {
			proxyRequest.URL.Host = hostConverter(r.Host)
		},
		Transport: http.DefaultTransport,
	}
}

func (proxy *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	proxyRequest := proxy.copyRequest(r)
	proxy.RequestConverter(r, proxyRequest)

	res, err := proxy.Transport.RoundTrip(proxyRequest)
	if err != nil {
		log.Printf("Unknown host %s", r.Host)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	defer res.Body.Close()
	for key, values := range res.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	w.WriteHeader(res.StatusCode)
	io.Copy(w, res.Body)
}

func (proxy *Proxy) copyRequest(r *http.Request) *http.Request {
	proxyRequest := new(http.Request)
	*proxyRequest = *r
	proxyRequest.Proto = "HTTP/1.1"
	proxyRequest.ProtoMajor = 1
	proxyRequest.ProtoMinor = 1
	proxyRequest.Close = false
	proxyRequest.Header = make(http.Header)
	proxyRequest.URL.Scheme = "http"
	proxyRequest.URL.Path = r.URL.Path

	for key, values := range r.Header {
		for _, value := range values {
			proxyRequest.Header.Add(key, value)
		}
	}
	for _, headerName := range ignoredHeaderNames {
		proxyRequest.Header.Del(headerName)
	}
	if requestHost, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		if values, ok := proxyRequest.Header["X-Forwarded-For"]; ok {
			requestHost = strings.Join(values, ", ") + ", " + requestHost
		}
		proxyRequest.Header.Set("X-Forwarded-For", requestHost)
	}

	return proxyRequest
}

func main() {
	if godotenv.Load() != nil {
		log.Fatal("Error loading .env file")
	}

	client, err := docker.NewClientFromEnv()
	if err != nil {
		log.Fatal("Err: %v", err)
	}
	dockerHost := os.Getenv("COURIER_DOCKER_HOST")
	if dockerHost == "" {
		dockerHost = "localhost"
	}
	re := regexp.MustCompile(`^([a-zA-Z0-9]+)\.([0-9]+)\.`)

	converter := func(originalHost string) string {
		group := re.FindSubmatch([]byte(originalHost))
		name := string(group[1])
		container, err := client.InspectContainer(name)
		if err != nil {
			log.Printf("Container not found. %s", name)
		}
		log.Printf("Host: %v", originalHost)
		_port := docker.Port(group[2]) + "/tcp"
		containerPort := container.NetworkSettings.Ports[_port][0].HostPort
		return dockerHost + ":" + containerPort
	}

	proxy := NewProxyWithHostConverter(converter)

	port := os.Getenv("COURIER_PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Listen %s", port)
	http.ListenAndServe(":" + port, proxy)
}
