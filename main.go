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
	Domain		string
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

func removePort(v string) string {
	i := strings.LastIndex(v, ":")
	if i == -1 { return v }
	return v[:i]
}

func RemoteIP(req *http.Request) string {
	if cfg.Network.Fcgi { return removePort(req.RemoteAddr) }
	ip := req.Header.Get("X-Real-IP")
	if ip == "" {
		s := req.RemoteAddr
		s = strings.Replace(s, "[", "", 1)
		s = strings.Replace(s, "]", "", 1)
		return removePort(s)
	}
	return ip
}

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
	if ok { return errors.New("This alias is already taken") }
	s.redirects[key] = value
	s.newLinks.Push(key + " " + value + "\n")
	return nil
}

func (s *shortener) UpdateLength() {
	l36 := math.Log(36)
	for {
		i := 2 + int(math.Log(float64(s.Length() + 1)) / l36)
		if i > maxRandLength {
			i = maxRandLength
		}
		s.linkLength = i
		time.Sleep(time.Second * updateLengthEvery)
	}
}

func (s *shortener) LoadLinks() error {
	data, err := os.ReadFile(cfg.SaveLinks)
	if err != nil { return err }
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		words := strings.Split(line, " ")
		if len(words) != 2 { continue }
		s.SetRedirect(words[0], words[1])
	}
	return nil
}

func (s *shortener) SaveLinks() error {
	if s.newLinks.IsEmpty() { return nil }
	f, err := os.OpenFile(cfg.SaveLinks,
			os.O_APPEND | os.O_CREATE | os.O_WRONLY, 0600)
	if err != nil { return err }
	for {
		v, err := s.newLinks.Pop()
		if err != nil { break }
		_, err = f.Write([]byte(v))
		if err != nil { return err }
	}
	return f.Close()
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
	alias := ""
	info := ""
	if code == 200 { alias = str } else { info = str }
	err := page.Execute(w, templateData{
		info, alias, cfg.Alias,
		cfg.BaseURL, cfg.AbuseEmail, cfg.Domain,
	})
	if err != nil { log.Println(err) }
}

func (s *shortener) CheckIP(req *http.Request) error {
	if cfg.RateLimit == 0 { return nil }
	addr := RemoteIP(req)
	// check when was the last time the ip created an url
	last, ok := s.clients[addr]
	now := time.Now().Unix()
	if ok && now - last <= cfg.RateLimit {
		return errors.New("Rate limited")
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
	return req.URL.RequestURI() + alias, nil
}

func (s *shortener) randomLink(u *url.URL, req *http.Request) (string, error) {
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
	return req.URL.RequestURI() + str, nil
}

func (s *shortener) ServePOST(w http.ResponseWriter, req *http.Request) error {
	if cfg.CSProtection { // check for cross-site requests
		origin := req.Header.Get("Origin")
		if origin != "" {
			u, err := url.ParseRequestURI(origin)
			if err != nil {
				return errors.New("Invalid origin header")
			}
			if req.Host != u.Host {
				return errors.New("Invalid cross-site request")
			}
		}
	}
	if req.URL.Path != cfg.BaseURL { return errors.New("Page not found") }
	// check if url is a valid url
	urlValue := strings.Trim(req.FormValue("url"), " ")
	if urlValue == "" { return errors.New("Missing URL") }
	if len(urlValue) >= maxUrlLength {
		return errors.New("URL is too long")
	}
	u, err := url.Parse(urlValue)
	if err != nil { return errors.New("Invalid URL") }
	if u.Host == "" || u.Host == req.Host {
		return errors.New("Invalid self-redirection")
	}
	if err := s.CheckIP(req); err != nil { return err }
	var str string
	alias := ""
	if cfg.Alias { alias = req.FormValue("alias") }
	if alias == "" {
		var err error
		str, err = s.randomLink(u, req)
		if err != nil { return err }
	} else {
		str, err = s.Create(u, req, alias)
		if err != nil { return err }
	}
	response(w, str, 200)
	log.Println("[" + RemoteIP(req) + "]", "created a new url", str,
			"redirecting to", u.String())
	return nil
}

func (s *shortener) ServeGET(w http.ResponseWriter, req *http.Request) error {
	if req.URL.Path == "/favicon.ico" {
		w.WriteHeader(200)
		w.Write([]byte(favicon))
		return nil
	}
	if req.URL.Path == cfg.BaseURL {
		w.WriteHeader(200)
		w.Write([]byte(indexPage))
		return nil
	}
	url, ok := s.GetRedirect(req.URL.Path[1:])
	if !ok { return errors.New("Page not found") }
	if cfg.Network.Fcgi {
		w.WriteHeader(302)
		w.Header().Set("Location", url)
	} else {
		http.Redirect(w, req, url, http.StatusSeeOther)
	}
	return nil
}

func (s *shortener) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var err error
	if req.Method == "POST" {
		err = s.ServePOST(w, req)
	} else if req.Method == "GET" {
		err = s.ServeGET(w, req)
	} else {
		err = errors.New("Invalid method")
	}
	if err != nil {
		log.Println("[" + RemoteIP(req) + "]", err)
		response(w, err.Error(), 400)
	}
}

func start() error {
	shortener := NewShortener()

        if err := load(); err != nil { return err }

        var listener net.Listener
        if cfg.Network.Type == "tcp" {
                addr := cfg.Network.Address + ":" +
                                        strconv.Itoa(cfg.Network.Port)
                l, err := net.Listen("tcp", addr)
                if err != nil { return err }
                listener = l
                log.Println("Listening on", addr)
        } else if cfg.Network.Type == "unix" {
                os.Remove(cfg.Network.Unix)
                unixAddr, err := net.ResolveUnixAddr("unix", cfg.Network.Unix)
                if err != nil { return err }
                l, err := net.ListenUnix("unix", unixAddr)
                if err != nil { return err }
                listener = l
                log.Println("Listening on unix:" + cfg.Network.Unix)
        } else {
		return errors.New("invalid network type " + cfg.Network.Type)
        }

	if cfg.SaveLinks != "" {
		if err := shortener.LoadLinks(); err != nil {
			log.Println(err)
		}
	}

	var err error
	page, err = template.New("html").Parse(htmlPage)
	if err != nil { return err }
	var buf bytes.Buffer
	data := templateData{
		"", "", cfg.Alias, cfg.BaseURL, cfg.AbuseEmail, cfg.Domain,
	}
	if err := page.Execute(&buf, data); err != nil { return err }
	indexPage = buf.String()

	go shortener.UpdateLength()
	if cfg.SaveLinks != "" {
		go func() {
			shortener.newLinks = NewStack()
			for {
				err := shortener.SaveLinks()
				if err != nil { log.Println(err) }
				time.Sleep(time.Second * saveLinksEvery)
			}
		}()
	}
	
	if cfg.Network.Fcgi { return fcgi.Serve(listener, shortener) }
	return http.Serve(listener, shortener)
}

func main() {
	if err := start(); err != nil { log.Println(err) }
}
