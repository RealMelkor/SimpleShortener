package main

import (
	"log"
	"time"
	"strconv"
	"strings"
	"net"
        "net/url"
        "net/http"
        "net/http/fcgi"
	"text/template"
	"math/rand"
	"math"
	"os"
	"bytes"
	"errors"
	"container/list"
	_ "embed"
)

const maxUrlLength = 2048
const characters = "abcdefghijklmnopqrstuvwxyz0123456789"
const saveLinksEvery = 30 // seconds
const updateLengthEvery = 90 // seconds
const limitPerIP = 3 // seconds between creation of link per ip
const maxRandLength = 64

type templateData struct {
	Error	string
	Url	string
}

var page *template.Template
var clients = map[string]int64{}
var redirects = map[string]string{}
var newLinks *list.List
var linkLength = 3
var indexPage string

//go:embed static/index.html
var htmlPage string

//go:embed static/favicon.ico
var favicon string

func updateLength() {
	for {
		i := 3 + int(math.Sqrt(float64(len(redirects)))) / 70
		if i > maxRandLength {
			i = maxRandLength
		}
		linkLength = i
		time.Sleep(time.Second * updateLengthEvery)
	}
}

func loadLinks() error {
	data, err := os.ReadFile(cfg.SaveLinks)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		words := strings.Split(line, " ")
		if len(words) != 2 {
			continue
		}
		redirects[words[0]] = words[1]
	}
	return nil
}

func saveLinks() {
	newLinks = list.New()
	for {
		if newLinks.Len() > 0 {
			f, err := os.OpenFile(cfg.SaveLinks,
				os.O_APPEND | os.O_CREATE | os.O_WRONLY, 0600)
			if err != nil {
				log.Println(err)
			}
			elements := []*list.Element{}
			for e := newLinks.Front(); e != nil; e = e.Next() {
				res, ok := e.Value.(string)
				if !ok {
					continue
				}
				_, err := f.Write([]byte(res))
				if err != nil {
					log.Println(err)
				}
				elements = append(elements, e)
			}
			for _, v := range elements {
				newLinks.Remove(v)
			}
			if err := f.Close(); err != nil {
				log.Println(err)
			}
		}
		time.Sleep(time.Second * saveLinksEvery)
	}
}

func randomString(n int) string {
	var random [maxRandLength]byte // n should never be above maxRandLength
	b := make([]byte, n)
	rand.Read(random[:n])
	for i := range b {
		b[i] = characters[int64(random[i]) % int64(len(characters))]
	}
	return string(b)
}

func response(w http.ResponseWriter, str string, code int) {
	w.WriteHeader(code)
	a := ""
	b := ""
	if code == 200 {
		b = str
	} else {
		a = str
	}
	if err := page.Execute(w, templateData{a, b}); err != nil {
		log.Println(err)
	}
}

func check(req *http.Request) error {
	i := strings.LastIndex(req.RemoteAddr, ":")
	if i < 0 {
		return errors.New("Invalid remote address")
	}
	addr := req.RemoteAddr[:i]
	// check when was the last time the ip created an url
	last, ok := clients[addr]
	now := time.Now().Unix()
	if ok {
		if now - last < limitPerIP {
			return errors.New("Rate limited")
		}
	}
	clients[addr] = time.Now().Unix()
	return nil
}

func create(u *url.URL, req *http.Request) string {
	var str string
	// check if the link is already taken
	for i := 0; ; i++ {
		str = randomString(linkLength + i)
		_, ok := redirects[str]
		if !ok {
			break
		}
	}
	redirects[str] = u.String()
	newLinks.PushBack(str + " " + u.String() + "\n")
	return req.URL.String() + str
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
			log.Println(req.RemoteAddr, "url too long")
			response(w, "URL is too long", 400)
			return
		}
		u, err := url.ParseRequestURI(urlValue)
		if err != nil {
			log.Println(req.RemoteAddr, err)
			response(w, "Invalid URL", 400)
			return
		}
		if u.Host == req.Host {
			log.Println(req.RemoteAddr,
				"tried to create redirect on current host")
			response(w, "Invalid URL", 400)
			return
		}
		if err := check(req); err != nil {
			log.Println(req.RemoteAddr, err)
			response(w, err.Error(), 400)
			return
		}
		str := create(u, req)
		response(w, str, 200)
		log.Println(req.RemoteAddr, "created a new url", str,
				"redirecting to", u.String())
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
	page, err = template.New("html").Parse(htmlPage)
	if err != nil {
		log.Fatalln(err)
	}
	var buf bytes.Buffer
	if err := page.Execute(&buf, templateData{"", ""}); err != nil {
		log.Fatalln(err)
	}
	indexPage = buf.String()

	go updateLength()
	if cfg.SaveLinks != "" {
		go saveLinks()
	}
	
        b := new(FastCGIServer)
        if err := fcgi.Serve(listener, b); err != nil {
                log.Fatalln(err)
        }
}
