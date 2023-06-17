package main

import (
	"log"
	"time"
	"fmt"
	"strconv"
	"net"
        "net/url"
        "net/http"
        "net/http/fcgi"
	"text/template"
	"math/rand"
	"math"
	"os"
	"encoding/json"
	_ "embed"
)

var responseTemplate *template.Template
const maxUrlLength = 2048
const newLink = "Your shortened URL link is : <a href=\"%s\">%s</a>"
const saveLinksEvery = 60 // seconds
var redirects = map[string]string{}
var linkLength = 3

//go:embed static/index.html
var indexPage string

//go:embed static/response.html
var responsePage string

//go:embed static/favicon.ico
var favicon string

func updateLength() {
	for {
		linkLength = 3 + int(math.Sqrt(float64(len(redirects)))) / 70
		time.Sleep(time.Second * 60)
	}
}

func loadLinks() error {
	data, err := os.ReadFile(cfg.SaveLinks)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &redirects)
}

func saveLinks() {
	for {
		data, err := json.Marshal(redirects)
		if err != nil {
			log.Println(err)
		} else {
			err := os.WriteFile(cfg.SaveLinks, data, 0600)
			if err != nil {
				log.Println(err)
			}
		}
		time.Sleep(time.Second * saveLinksEvery)
	}
}

func randomString(n int) string {
	table := "abcdefghijklmnopqrstuvwxyz0123456789"
	str := ""
	for i := 0; i < n; i++ {
		str += string(table[rand.Int() % len(table)])
	}
	return str
}

func response(w http.ResponseWriter, str string, code int) {
	w.WriteHeader(code)
	if err := responseTemplate.Execute(w, str); err != nil {
		log.Println(err)
	}
}

type FastCGIServer struct{}
func (s FastCGIServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method == "POST" {
		if req.URL.Path != cfg.BaseURL {
			response(w, "Page not found", 404)
			return
		}
		// check if url is a valid url
		urlValue := req.FormValue("url")
		if len(urlValue) >= maxUrlLength {
			log.Println("url too long")
			response(w, "URL is too long", 400)
			return
		}
		u, err := url.ParseRequestURI(urlValue)
		if err != nil {
			log.Println(err)
			response(w, "Invalid URL", 400)
			return
		}
		if u.Host == req.Host {
			log.Println("tried to create redirect on current host")
			response(w, "Invalid URL", 400)
			return
		}
		// check if the link is already taken
		var str string
		for i := 0; ; i++ {
			str = randomString(linkLength + i)
			_, ok := redirects[str]
			if !ok {
				break
			}
		}
		redirects[str] = u.String()
		str = req.URL.String() + str
		response(w, fmt.Sprintf(newLink, str, str), 200)
		log.Println(str, "new url created redirecting to", u.String())
		return
	} else if req.Method == "GET" {
		if req.URL.Path == "/favicon.ico" {
			w.WriteHeader(200)
			w.Write([]byte(favicon))
			return
		}
		if req.URL.Path == cfg.BaseURL {
			w.WriteHeader(200)
			w.Write([]byte(indexPage))
			return
		}
		url, ok := redirects[req.URL.Path[1:]]
		if !ok {
			response(w, "Page not found", 404)
			return
		}
		w.WriteHeader(302)
		w.Header().Set("Location", url)
		return
	}
}

func main() {

	rand.Seed(time.Now().UnixNano())

        if err := load(); err != nil {
                log.Fatalln(err)
        }

        var listener net.Listener
        if cfg.Network.Type == "tcp" {
                addr := cfg.Network.Address + ":" +
                                        strconv.Itoa(cfg.Network.Port)
                l, err := net.Listen("tcp", addr)
                if err != nil {
                        log.Fatalln(err)
                }
                listener = l
                log.Println("Listening on", addr)
        } else if cfg.Network.Type == "unix" {
                os.Remove(cfg.Network.Unix)
                unixAddr, err := net.ResolveUnixAddr("unix", cfg.Network.Unix)
                if err != nil {
                        log.Fatalln(err)
                }
                l, err := net.ListenUnix("unix", unixAddr)
                if err != nil {
                        log.Fatalln(err)
                }
                listener = l
                log.Println("Listening on unix:" + cfg.Network.Unix)
        } else {
                log.Fatalln("invalid network type", cfg.Network.Type)
        }

	if cfg.SaveLinks != "" {
		if err := loadLinks(); err != nil {
			log.Println(err)
		}
	}

	var err error
	responseTemplate, err = template.New("response").Parse(responsePage)
	if err != nil {
		log.Fatalln(err)
	}

	go updateLength()
	if cfg.SaveLinks != "" {
		go saveLinks()
	}
	
        b := new(FastCGIServer)
        if err := fcgi.Serve(listener, b); err != nil {
                log.Fatalln(err)
        }
}
