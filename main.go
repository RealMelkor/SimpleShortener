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
	"crypto/rand"
	"math"
	"os"
	"bytes"
	"errors"
	"sync"
	_ "embed"
)

const maxUrlLength = 2048
const maxAliasLength = 128
const characters = "abcdefghijklmnopqrstuvwxyz0123456789"
const saveLinksEvery = 30 // seconds
const updateLengthEvery = 90 // seconds
const maxRandLength = 16 // 2 + ln(2^64)/ln(36) < 16

type templateData struct {
	Error		string
	Url		string
	Alias		bool
	BaseURL		string
	AbuseEmail	string
}

type shortener struct {
	clients		map[string]int64
	redirects	map[string]string
	lock		sync.Mutex
	newLinks	*stack
	linkLength	int
}

var page *template.Template
var indexPage string

//go:embed static/index.html
var htmlPage string

//go:embed static/favicon.ico
var favicon string

func NewShortener() *shortener {
	return &shortener{
		map[string]int64{},
		map[string]string{},
		sync.Mutex{},
		NewStack(),
		2,
	}
}

func (s *shortener) Length() int {
	s.lock.Lock()
	length := len(s.redirects)
	s.lock.Unlock()
	return length
}

func (s *shortener) SetRedirect(key string, value string) {
	s.lock.Lock()
	s.redirects[key] = value
	s.lock.Unlock()
}

func (s *shortener) GetRedirect(key string) (string, bool) {
	s.lock.Lock()
	v, ok := s.redirects[key]
	s.lock.Unlock()
	return v, ok
}

func (s *shortener) AddRedirect(key string, value string) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	_, ok := s.redirects[key]
	if ok {
		return errors.New("This alias is already taken")
	}
	s.redirects[key] = value
	s.newLinks.Push(key + " " + value + "\n")
	return nil
}

func (s *shortener) UpdateLength() {
	l36 := math.Log(36)
	for {
		i := 2 + int(math.Log(float64(s.Length())) / l36)
		if i > maxRandLength {
			i = maxRandLength
		}
		s.linkLength = i
		time.Sleep(time.Second * updateLengthEvery)
	}
}

func (s *shortener) LoadLinks() error {
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
		s.SetRedirect(words[0], words[1])
	}
	return nil
}

func (s *shortener) SaveLinks() {
	s.newLinks = NewStack()
	for {
		if !s.newLinks.IsEmpty() {
			f, err := os.OpenFile(cfg.SaveLinks,
				os.O_APPEND | os.O_CREATE | os.O_WRONLY, 0600)
			if err != nil {
				log.Println(err)
			}
			for {
				v, err := s.newLinks.Pop()
				if err != nil {
					break
				}
				_, err = f.Write([]byte(v))
				if err != nil {
					log.Println(err)
				}
			}
			if err := f.Close(); err != nil {
				log.Println(err)
			}
		}
		time.Sleep(time.Second * saveLinksEvery)
	}
}

func randomString(n int) (string, error) {
	var random [maxRandLength]byte // n should never be above maxRandLength
	b := make([]byte, n)
	_, err := rand.Read(random[:n])
	if err != nil { return "", err }
	for i := range b {
		b[i] = characters[int64(random[i]) % int64(len(characters))]
	}
	return string(b), nil
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
	err := page.Execute(w, templateData{
		a, b, cfg.Alias, cfg.BaseURL, cfg.AbuseEmail})
	if err != nil {
		log.Println(err)
	}
}

func (s *shortener) CheckIP(req *http.Request) error {
	if cfg.RateLimit == 0 {
		return nil
	}
	i := strings.LastIndex(req.RemoteAddr, ":")
	if i < 0 {
		return errors.New("Invalid remote address")
	}
	addr := req.RemoteAddr[:i]
	// check when was the last time the ip created an url
	last, ok := s.clients[addr]
	now := time.Now().Unix()
	if ok {
		if now - last <= cfg.RateLimit {
			return errors.New("Rate limited")
		}
	}
	s.clients[addr] = time.Now().Unix()
	return nil
}

func (s *shortener) Create(u *url.URL, req *http.Request, alias string) (
		string, error) {
	if len(alias) >= maxAliasLength {
		return "", errors.New("This alias is too long")
	}
	alias = strings.ToLower(alias)
	for _, v := range []byte(alias) {
		if (v < 'a' || v > 'z') && (v < '0' || v > '9') && v != '_' {
			return "", errors.New("Invalid alias")
		}
	}
	if err := s.AddRedirect(alias, u.String()); err != nil {
		return "", err
	}
	return req.URL.String() + alias, nil
}

func (s *shortener) CreateRandom(u *url.URL, req *http.Request) (string, error) {
	var str string
	// check if the link is already taken
	for i := 0; ; i++ {
		var err error
		str, err = randomString(s.linkLength + i)
		if err != nil { return "", err }
		if err := s.AddRedirect(str, u.String()); err == nil {
			break
		}
	}
	return req.URL.String() + str, nil
}

func (s *shortener) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method == "POST" {
		if cfg.CSProtection {
			// prevent cross-site request
			u, err := url.ParseRequestURI(req.Header.Get("Origin"))
			if err != nil {
				log.Println(req.RemoteAddr, err)
				response(w, "Invalid origin header", 400)
				return
			}
			if req.Host != u.Host {
				log.Println(req.RemoteAddr,
					"attempted a cross-site request",
					"(" + u.Host + ")")
				response(w, "Cross-Site request detected", 400)
				return
			}
		}
		if req.URL.Path != cfg.BaseURL {
			response(w, "Page not found", 404)
			return
		}
		// check if url is a valid url
		urlValue := req.FormValue("url")
		if urlValue == "" {
			log.Println(req.RemoteAddr, "missing url")
			response(w, "Missing URL", 400)
			return
		}
		if len(urlValue) >= maxUrlLength {
			log.Println(req.RemoteAddr, "url too long")
			response(w, "URL is too long", 400)
			return
		}
		u, err := url.Parse(urlValue)
		if err != nil {
			log.Println(req.RemoteAddr, err)
			response(w, "Invalid URL", 400)
			return
		}
		if u.Host == "" || u.Host == req.Host {
			log.Println(req.RemoteAddr,
				"tried to create redirect on current host")
			response(w, "Invalid URL", 400)
			return
		}
		if err := s.CheckIP(req); err != nil {
			log.Println(req.RemoteAddr, err)
			response(w, err.Error(), 400)
			return
		}
		var str string
		alias := ""
		if cfg.Alias {
			alias = req.FormValue("alias")
		}
		if alias == "" {
			var err error
			str, err = s.CreateRandom(u, req)
			if err != nil {
				log.Println(req.RemoteAddr, err)
				response(w, err.Error(), 400)
				return
			}
		} else {
			str, err = s.Create(u, req, alias)
			if err != nil {
				log.Println(req.RemoteAddr, err)
				response(w, err.Error(), 400)
				return
			}
		}
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
		url, ok := s.GetRedirect(req.URL.Path[1:])
		if !ok {
			response(w, "Page not found", 404)
			return
		}
		if cfg.Network.Fcgi {
			w.WriteHeader(302)
			w.Header().Set("Location", url)
		} else {
			http.Redirect(w, req, url, http.StatusSeeOther)
		}
	}
}

func main() {

	shortener := NewShortener()

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
		if err := shortener.LoadLinks(); err != nil {
			log.Println(err)
		}
	}

	var err error
	page, err = template.New("html").Parse(htmlPage)
	if err != nil {
		log.Fatalln(err)
	}
	var buf bytes.Buffer
	data := templateData{"", "", cfg.Alias, cfg.BaseURL, cfg.AbuseEmail}
	if err := page.Execute(&buf, data); err != nil {
		log.Fatalln(err)
	}
	indexPage = buf.String()

	go shortener.UpdateLength()
	if cfg.SaveLinks != "" {
		go shortener.SaveLinks()
	}
	
	if cfg.Network.Fcgi {
		if err := fcgi.Serve(listener, shortener); err != nil {
			log.Fatalln(err)
		}
	} else {
		if err := http.Serve(listener, shortener); err != nil {
			log.Fatalln(err)
		}
	}
}
